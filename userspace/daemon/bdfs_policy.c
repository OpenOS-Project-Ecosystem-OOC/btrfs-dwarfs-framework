// SPDX-License-Identifier: GPL-2.0-or-later
/*
 * bdfs_policy.c - Auto-demote policy engine
 *
 * Background thread that periodically scans BTRFS-backed partitions and
 * demotes subvolumes matching configured rules to DwarFS images.
 *
 * Scan algorithm per rule:
 *   1. List all subvolumes on the target partition via BDFS_IOC_LIST_BTRFS_SUBVOLS.
 *   2. For each subvolume:
 *      a. Check name against name_pattern (fnmatch).
 *      b. stat() the subvolume path to get atime/mtime.
 *      c. If max(atime, mtime) is older than age_days, check size.
 *      d. If size >= min_size_bytes, enqueue a BDFS_JOB_EXPORT_TO_DWARFS job.
 *      e. If delete_after_demote, set BDFS_DEMOTE_DELETE_SUBVOL on the job.
 *   3. Update statistics.
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <fnmatch.h>
#include <syslog.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/ioctl.h>

#include "bdfs_policy.h"

/* ── Background scan thread ─────────────────────────────────────────────── */

static void *policy_thread(void *arg)
{
	struct bdfs_policy_engine *pe = arg;

	syslog(LOG_INFO, "bdfs_policy: engine started (interval=%us)",
	       pe->scan_interval_s);

	while (pe->running) {
		/* Sleep in 1-second increments so shutdown is responsive */
		uint32_t slept = 0;
		while (slept < pe->scan_interval_s && pe->running) {
			sleep(1);
			slept++;
		}
		if (!pe->running)
			break;

		bdfs_policy_scan(pe);
	}

	syslog(LOG_INFO, "bdfs_policy: engine stopped");
	return NULL;
}

/* ── Subvolume age check ─────────────────────────────────────────────────── */

/*
 * subvol_last_access - Return the most recent of atime and mtime for a
 * subvolume path.  Returns 0 on stat failure (treated as "very old").
 */
static time_t subvol_last_access(const char *path)
{
	struct stat st;
	if (stat(path, &st) < 0)
		return 0;
	return st.st_atime > st.st_mtime ? st.st_atime : st.st_mtime;
}

/*
 * subvol_size_bytes - Return the apparent size of a subvolume directory
 * using du(1).  Returns 0 on failure.
 */
static uint64_t subvol_size_bytes(const char *path)
{
	char cmd[BDFS_PATH_MAX + 32];
	FILE *fp;
	uint64_t kb = 0;

	snprintf(cmd, sizeof(cmd), "du -sk %s 2>/dev/null", path);
	fp = popen(cmd, "r");
	if (!fp)
		return 0;
	fscanf(fp, "%llu", (unsigned long long *)&kb);
	pclose(fp);
	return kb * 1024;
}

/* ── Rule matching ───────────────────────────────────────────────────────── */

static bool rule_matches_subvol(const struct bdfs_policy_rule *rule,
				const struct bdfs_btrfs_subvol *sv,
				time_t now)
{
	time_t last_access;
	uint64_t size;

	if (!rule->enabled)
		return false;

	/* Name pattern filter */
	if (rule->name_pattern[0]) {
		if (fnmatch(rule->name_pattern, sv->name, 0) != 0)
			return false;
	}

	/* Age check */
	last_access = subvol_last_access(sv->path);
	if (last_access == 0) {
		/* Can't stat — skip rather than accidentally demoting */
		return false;
	}
	double age_days = difftime(now, last_access) / 86400.0;
	if (age_days < (double)rule->age_days)
		return false;

	/* Size check */
	if (rule->min_size_bytes > 0) {
		size = subvol_size_bytes(sv->path);
		if (size < rule->min_size_bytes)
			return false;
	}

	return true;
}

/* ── Scan ────────────────────────────────────────────────────────────────── */

int bdfs_policy_scan(struct bdfs_policy_engine *pe)
{
	struct bdfs_policy_rule *rule;
	time_t now = time(NULL);
	int total_demoted = 0;

	syslog(LOG_INFO, "bdfs_policy: starting scan");
	pe->last_scan_time = now;

	pthread_mutex_lock(&pe->rules_lock);

	TAILQ_FOREACH(rule, &pe->rules, entry) {
		struct bdfs_ioctl_list_btrfs_subvols list_arg;
		struct bdfs_btrfs_subvol *subvols = NULL;
		uint32_t cap = 64, i;

		if (!rule->enabled)
			continue;

		/* Fetch subvolume list for this partition */
		subvols = calloc(cap, sizeof(*subvols));
		if (!subvols)
			continue;

		memset(&list_arg, 0, sizeof(list_arg));
		memcpy(list_arg.partition_uuid, rule->partition_uuid, 16);
		list_arg.count   = cap;
		list_arg.subvols = subvols;

		if (ioctl(pe->daemon->ctl_fd,
			  BDFS_IOC_LIST_BTRFS_SUBVOLS, &list_arg) < 0) {
			syslog(LOG_WARNING,
			       "bdfs_policy: list_subvols failed: %s",
			       strerror(errno));
			free(subvols);
			continue;
		}

		if (list_arg.total > cap) {
			free(subvols);
			cap = list_arg.total;
			subvols = calloc(cap, sizeof(*subvols));
			if (!subvols)
				continue;
			list_arg.count   = cap;
			list_arg.subvols = subvols;
			ioctl(pe->daemon->ctl_fd,
			      BDFS_IOC_LIST_BTRFS_SUBVOLS, &list_arg);
		}

		for (i = 0; i < list_arg.count; i++) {
			struct bdfs_btrfs_subvol *sv = &subvols[i];
			struct bdfs_job *job;
			char image_name[BDFS_NAME_MAX + 1];
			char ts[32];
			struct tm *tm_info;
			time_t t = now;

			if (!rule_matches_subvol(rule, sv, now))
				continue;

			/* Build a timestamped image name */
			tm_info = localtime(&t);
			strftime(ts, sizeof(ts), "%Y%m%d_%H%M%S", tm_info);
			snprintf(image_name, sizeof(image_name),
				 "%s_auto_%s", sv->name, ts);

			syslog(LOG_INFO,
			       "bdfs_policy: demoting subvol '%s' (rule %llu, "
			       "age threshold %u days) → %s",
			       sv->name,
			       (unsigned long long)rule->rule_id,
			       rule->age_days,
			       image_name);

			job = bdfs_job_alloc(BDFS_JOB_EXPORT_TO_DWARFS);
			if (!job)
				continue;

			memcpy(job->partition_uuid, rule->partition_uuid, 16);
			job->object_id = sv->subvol_id;
			job->export_to_dwarfs.subvol_id   = sv->subvol_id;
			job->export_to_dwarfs.compression = rule->compression;
			job->export_to_dwarfs.worker_threads = 2;
			strncpy(job->export_to_dwarfs.btrfs_mount,
				sv->path, BDFS_PATH_MAX - 1);
			strncpy(job->export_to_dwarfs.image_name,
				image_name, BDFS_NAME_MAX);

			if (rule->delete_after_demote)
				job->export_to_dwarfs.flags |=
					BDFS_EXPORT_INCREMENTAL; /* reuse flag slot */

			bdfs_daemon_enqueue(pe->daemon, job);
			total_demoted++;
			pe->total_demotes++;
		}

		free(subvols);
	}

	pthread_mutex_unlock(&pe->rules_lock);

	if (total_demoted > 0)
		syslog(LOG_INFO, "bdfs_policy: scan complete, %d demote(s) queued",
		       total_demoted);
	else
		syslog(LOG_DEBUG, "bdfs_policy: scan complete, nothing to demote");

	return total_demoted;
}

/* ── Lifecycle ───────────────────────────────────────────────────────────── */

int bdfs_policy_init(struct bdfs_policy_engine *pe, struct bdfs_daemon *d)
{
	memset(pe, 0, sizeof(*pe));
	pe->daemon           = d;
	pe->scan_interval_s  = BDFS_POLICY_DEFAULT_INTERVAL_S;
	pe->next_rule_id     = 1;
	pe->running          = true;

	TAILQ_INIT(&pe->rules);
	pthread_mutex_init(&pe->rules_lock, NULL);

	if (pthread_create(&pe->thread, NULL, policy_thread, pe) != 0) {
		syslog(LOG_ERR, "bdfs_policy: failed to start thread: %m");
		return -errno;
	}

	syslog(LOG_INFO, "bdfs_policy: engine initialised");
	return 0;
}

void bdfs_policy_shutdown(struct bdfs_policy_engine *pe)
{
	struct bdfs_policy_rule *rule, *tmp;

	pe->running = false;
	pthread_join(pe->thread, NULL);

	pthread_mutex_lock(&pe->rules_lock);
	TAILQ_FOREACH_SAFE(rule, &pe->rules, entry, tmp) {
		TAILQ_REMOVE(&pe->rules, rule, entry);
		free(rule);
	}
	pthread_mutex_unlock(&pe->rules_lock);
	pthread_mutex_destroy(&pe->rules_lock);
}

/* ── Rule management ─────────────────────────────────────────────────────── */

uint64_t bdfs_policy_add_rule(struct bdfs_policy_engine *pe,
			      const struct bdfs_policy_rule *template)
{
	struct bdfs_policy_rule *rule;

	rule = calloc(1, sizeof(*rule));
	if (!rule)
		return 0;

	memcpy(rule, template, sizeof(*rule));

	pthread_mutex_lock(&pe->rules_lock);
	rule->rule_id = pe->next_rule_id++;
	rule->enabled = true;
	TAILQ_INSERT_TAIL(&pe->rules, rule, entry);
	pthread_mutex_unlock(&pe->rules_lock);

	syslog(LOG_INFO,
	       "bdfs_policy: rule %llu added (age=%u days, pattern='%s')",
	       (unsigned long long)rule->rule_id,
	       rule->age_days,
	       rule->name_pattern[0] ? rule->name_pattern : "*");

	return rule->rule_id;
}

int bdfs_policy_remove_rule(struct bdfs_policy_engine *pe, uint64_t rule_id)
{
	struct bdfs_policy_rule *rule;
	int ret = -ENOENT;

	pthread_mutex_lock(&pe->rules_lock);
	TAILQ_FOREACH(rule, &pe->rules, entry) {
		if (rule->rule_id == rule_id) {
			TAILQ_REMOVE(&pe->rules, rule, entry);
			free(rule);
			ret = 0;
			break;
		}
	}
	pthread_mutex_unlock(&pe->rules_lock);
	return ret;
}

int bdfs_policy_list_rules(struct bdfs_policy_engine *pe,
			   struct bdfs_policy_rule *out,
			   uint32_t capacity,
			   uint32_t *count_out)
{
	struct bdfs_policy_rule *rule;
	uint32_t n = 0;

	pthread_mutex_lock(&pe->rules_lock);
	TAILQ_FOREACH(rule, &pe->rules, entry) {
		if (n < capacity)
			memcpy(&out[n], rule, sizeof(*rule));
		n++;
	}
	pthread_mutex_unlock(&pe->rules_lock);

	*count_out = n;
	return 0;
}
