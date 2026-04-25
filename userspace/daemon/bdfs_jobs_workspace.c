// SPDX-License-Identifier: GPL-2.0-or-later
/*
 * bdfs_jobs_workspace.c - Workspace shutdown lifecycle hook
 *
 * Implements BDFS_JOB_WORKSPACE_SHUTDOWN, which runs the appropriate bdfs
 * operation before a workspace container is stopped:
 *
 *   BDFS_WS_SHUTDOWN_PAUSE  — take a read-only BTRFS snapshot of the
 *                             workspace subvolume so the state at pause
 *                             time is recoverable. Optionally prunes old
 *                             snapshots keeping the N most recent.
 *
 *   BDFS_WS_SHUTDOWN_DELETE — demote the workspace subvolume to a DwarFS
 *                             archive before the container is destroyed.
 *
 *   BDFS_WS_SHUTDOWN_STOP   — no-op; the workspace is stopping normally
 *                             with no state preservation required.
 *
 * All operations are best-effort: failures are logged via syslog but do
 * not propagate to the caller. The workspace must stop regardless.
 */

#define _GNU_SOURCE
#include <errno.h>
#include <fnmatch.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <syslog.h>
#include <time.h>
#include <unistd.h>

#include "bdfs_daemon.h"

/* Maximum time (seconds) allowed for a workspace demote operation.
 * Large workspaces on slow disks can take tens of minutes to compress. */
#define WS_DEMOTE_TIMEOUT_S  (45 * 60)

/* ── Helpers ─────────────────────────────────────────────────────────────── */

/*
 * derive_image_path - build the .dwarfs archive path from the workspace path.
 *
 * If job->workspace_shutdown.image_path is non-empty it is used as-is.
 * Otherwise the archive is placed alongside the subvolume as
 * "<workspace_path>.dwarfs".
 */
static void derive_image_path(const struct bdfs_job *job,
			      char *out, size_t outsz)
{
	const char *explicit = job->workspace_shutdown.image_path;
	if (explicit[0] != '\0') {
		snprintf(out, outsz, "%s", explicit);
	} else {
		snprintf(out, outsz, "%s.dwarfs",
			 job->workspace_shutdown.workspace_path);
	}
}

/*
 * snapshot_name_for_pause - generate a timestamped snapshot name.
 *
 * Format: ws-snap-<unix_timestamp>
 * Placed as a sibling of the workspace subvolume:
 *   <parent_dir>/ws-snap-<ts>
 */
static void snapshot_path_for_pause(const char *workspace_path,
				    char *out, size_t outsz)
{
	char parent[BDFS_PATH_MAX];
	time_t now = time(NULL);

	/* Extract parent directory */
	snprintf(parent, sizeof(parent), "%s", workspace_path);
	char *slash = strrchr(parent, '/');
	if (slash && slash != parent)
		*slash = '\0';
	else
		snprintf(parent, sizeof(parent), ".");

	snprintf(out, outsz, "%s/ws-snap-%" PRId64,
		 parent, (int64_t)now);
}

/* ── Pause: BTRFS snapshot ───────────────────────────────────────────────── */

static int workspace_do_pause(struct bdfs_daemon *d,
			      const struct bdfs_job *job)
{
	const char *ws = job->workspace_shutdown.workspace_path;
	char snap_path[BDFS_PATH_MAX];
	int ret;

	snapshot_path_for_pause(ws, snap_path, sizeof(snap_path));

	syslog(LOG_INFO, "bdfs: workspace pause: snapshotting %s → %s",
	       ws, snap_path);

	ret = bdfs_exec_btrfs_snapshot(d, ws, snap_path, /*readonly=*/true);
	if (ret) {
		syslog(LOG_WARNING,
		       "bdfs: workspace pause: snapshot failed (non-fatal): %d",
		       ret);
		return ret;
	}

	syslog(LOG_INFO, "bdfs: workspace pause: snapshot complete → %s",
	       snap_path);

	/* Prune old snapshots if configured */
	uint32_t keep = job->workspace_shutdown.prune_keep;
	if (keep > 0) {
		struct bdfs_job *prune_job = bdfs_job_alloc(BDFS_JOB_PRUNE);
		if (!prune_job) {
			syslog(LOG_WARNING,
			       "bdfs: workspace pause: prune alloc failed (non-fatal)");
			return 0; /* snapshot succeeded; prune failure is non-fatal */
		}

		/* Derive parent directory of the workspace subvolume */
		char parent[BDFS_PATH_MAX];
		snprintf(parent, sizeof(parent), "%s", ws);
		char *slash = strrchr(parent, '/');
		if (slash && slash != parent)
			*slash = '\0';
		else
			snprintf(parent, sizeof(parent), ".");

		snprintf(prune_job->prune.btrfs_mount,
			 sizeof(prune_job->prune.btrfs_mount), "%s", parent);
		snprintf(prune_job->prune.name_pattern,
			 sizeof(prune_job->prune.name_pattern), "ws-snap-*");
		prune_job->prune.keep_count = keep;
		prune_job->prune.flags = 0;
		prune_job->prune.demote_compression =
			job->workspace_shutdown.compression;

		int prune_ret = bdfs_job_prune(d, prune_job);
		bdfs_job_free(prune_job);
		if (prune_ret)
			syslog(LOG_WARNING,
			       "bdfs: workspace pause: prune failed (non-fatal): %d",
			       prune_ret);
	}

	return 0;
}

/* ── Delete: bdfs demote ─────────────────────────────────────────────────── */

static int workspace_do_delete(struct bdfs_daemon *d,
			       const struct bdfs_job *job)
{
	const char *ws = job->workspace_shutdown.workspace_path;
	char image_path[BDFS_PATH_MAX];

	derive_image_path(job, image_path, sizeof(image_path));

	syslog(LOG_INFO,
	       "bdfs: workspace delete: demoting %s → %s (compression=%u)",
	       ws, image_path, job->workspace_shutdown.compression);

	/*
	 * Reuse the export-to-dwarfs job machinery: build a synthetic job
	 * targeting the workspace subvolume and dispatch it synchronously.
	 */
	struct bdfs_job *exp = bdfs_job_alloc(BDFS_JOB_EXPORT_TO_DWARFS);
	if (!exp) {
		syslog(LOG_ERR, "bdfs: workspace delete: job alloc failed");
		return -ENOMEM;
	}

	snprintf(exp->export_to_dwarfs.image_path,
		 sizeof(exp->export_to_dwarfs.image_path), "%s", image_path);
	snprintf(exp->export_to_dwarfs.image_name,
		 sizeof(exp->export_to_dwarfs.image_name), "workspace");
	exp->export_to_dwarfs.compression =
		job->workspace_shutdown.compression;
	exp->export_to_dwarfs.worker_threads = 2;

	/*
	 * btrfs_mount: parent directory of the workspace subvolume.
	 * subvol_id: 0 — the export handler will use btrfs_mount directly
	 * when subvol_id is 0, treating btrfs_mount as the source path.
	 */
	snprintf(exp->export_to_dwarfs.btrfs_mount,
		 sizeof(exp->export_to_dwarfs.btrfs_mount), "%s", ws);
	exp->export_to_dwarfs.subvol_id = 0;

	int ret = bdfs_job_export_to_dwarfs(d, exp);
	bdfs_job_free(exp);

	if (ret) {
		syslog(LOG_WARNING,
		       "bdfs: workspace delete: demote failed (non-fatal): %d",
		       ret);
		return ret;
	}

	syslog(LOG_INFO, "bdfs: workspace delete: demote complete → %s",
	       image_path);
	return 0;
}

/* ── Entry point ─────────────────────────────────────────────────────────── */

int bdfs_job_workspace_shutdown(struct bdfs_daemon *d, struct bdfs_job *job)
{
	const char *ws = job->workspace_shutdown.workspace_path;

	if (ws[0] == '\0') {
		syslog(LOG_WARNING,
		       "bdfs: workspace_shutdown: empty workspace_path, skipping");
		return 0;
	}

	switch (job->workspace_shutdown.reason) {
	case BDFS_WS_SHUTDOWN_PAUSE:
		return workspace_do_pause(d, job);

	case BDFS_WS_SHUTDOWN_DELETE:
		return workspace_do_delete(d, job);

	case BDFS_WS_SHUTDOWN_STOP:
		syslog(LOG_DEBUG,
		       "bdfs: workspace_shutdown: reason=stop, no-op for %s", ws);
		return 0;

	default:
		syslog(LOG_WARNING,
		       "bdfs: workspace_shutdown: unknown reason %u for %s",
		       job->workspace_shutdown.reason, ws);
		return 0;
	}
}
