"""
bdfs_transfer.py — btrfs-dwarfs-framework integration for btr-fs-git

Provides helpers to export a BTRFS snapshot to a DwarFS compressed image
(demote) and import a DwarFS image back to a BTRFS subvolume (promote), as
well as bdfs-aware push/pull wrappers that compress snapshots in transit.

All functions are no-ops (returning None) when the bdfs binary is absent,
so btr-fs-git continues to work on systems without bdfs installed.
"""

import logging
import os
import shutil
import subprocess
import time
from pathlib import Path
from typing import Optional

log = logging.getLogger("bfg.bdfs")


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _bdfs_bin() -> Optional[str]:
    """Return the path to the bdfs binary, or None if not found."""
    return shutil.which("bdfs")


def _run(cmd: list, check: bool = True) -> subprocess.CompletedProcess:
    log.debug("bdfs_transfer: %s", " ".join(str(c) for c in cmd))
    return subprocess.run(cmd, check=check, capture_output=True, text=True)


# ---------------------------------------------------------------------------
# Core export / import
# ---------------------------------------------------------------------------

def export_to_dwarfs(subvol_path: str, output_path: Optional[str] = None) -> Optional[str]:
    """Demote a read-only BTRFS snapshot to a DwarFS compressed image.

    Parameters
    ----------
    subvol_path:
        Path to the read-only BTRFS subvolume to compress.
    output_path:
        Destination path for the .dwarfs file.  Defaults to
        ``<subvol_path>.dwarfs`` alongside the subvolume.

    Returns
    -------
    str or None
        Path of the created .dwarfs file, or None if bdfs is unavailable
        or the operation failed.
    """
    bdfs = _bdfs_bin()
    if bdfs is None:
        log.debug("bdfs not found; skipping export_to_dwarfs")
        return None

    subvol_path = str(Path(subvol_path).resolve())
    if output_path is None:
        output_path = subvol_path + ".dwarfs"

    try:
        _run([bdfs, "snapshot", "demote", "--to-dwarfs", subvol_path, "--output", output_path])
        log.info("bdfs: exported %s -> %s", subvol_path, output_path)
        return output_path
    except subprocess.CalledProcessError as exc:
        log.warning("bdfs demote failed: %s", exc.stderr.strip())
        return None


def import_from_dwarfs(dwarfs_path: str, target_subvol: str) -> Optional[str]:
    """Promote a DwarFS image back to a writable BTRFS subvolume.

    Parameters
    ----------
    dwarfs_path:
        Path to the .dwarfs file to import.
    target_subvol:
        Destination path for the new BTRFS subvolume.

    Returns
    -------
    str or None
        Path of the created subvolume, or None if bdfs is unavailable or
        the operation failed.
    """
    bdfs = _bdfs_bin()
    if bdfs is None:
        log.debug("bdfs not found; skipping import_from_dwarfs")
        return None

    dwarfs_path = str(Path(dwarfs_path).resolve())

    try:
        _run([bdfs, "snapshot", "promote", "--from-dwarfs", dwarfs_path, "--output", target_subvol])
        log.info("bdfs: imported %s -> %s", dwarfs_path, target_subvol)
        return target_subvol
    except subprocess.CalledProcessError as exc:
        log.warning("bdfs promote failed: %s", exc.stderr.strip())
        return None


# ---------------------------------------------------------------------------
# bdfs-aware push / pull
# ---------------------------------------------------------------------------

def bdfs_push(bfg_instance, subvol: str, snapshot: str, remote_subvol: str,
              parent=None, clonesrcs=None, compress: bool = True):
    """Push a snapshot to the remote, optionally compressing it via bdfs first.

    When ``compress=True`` and bdfs is available:
    1. Export the snapshot to a temporary .dwarfs file.
    2. Transfer the .dwarfs file via rsync/scp (falls back to plain btrfs-send
       if transfer of the .dwarfs file fails).
    3. On the remote, import the .dwarfs file back to a BTRFS subvolume.

    When bdfs is unavailable or ``compress=False``, delegates directly to
    ``bfg_instance.push()``.

    Parameters
    ----------
    bfg_instance:
        A BtrFsGit instance (provides ``push()``, ``_sshstr``, ``_remote_cmd``).
    subvol, snapshot, remote_subvol, parent, clonesrcs:
        Forwarded to ``bfg_instance.push()`` when not compressing.
    compress:
        Whether to attempt DwarFS compression.

    Returns
    -------
    Result of the underlying push operation.
    """
    if clonesrcs is None:
        clonesrcs = []

    if not compress or _bdfs_bin() is None:
        log.debug("bdfs_push: falling back to plain btrfs-send push")
        return bfg_instance.push(subvol, snapshot, remote_subvol, parent, clonesrcs)

    # Step 1: export snapshot to a temporary .dwarfs file
    ts = int(time.time())
    tmp_dwarfs = f"/tmp/bfg_bdfs_{Path(snapshot).name}_{ts}.dwarfs"
    archive = export_to_dwarfs(snapshot, tmp_dwarfs)

    if archive is None:
        log.warning("bdfs_push: export failed; falling back to plain push")
        return bfg_instance.push(subvol, snapshot, remote_subvol, parent, clonesrcs)

    try:
        # Step 2: transfer the .dwarfs file to the remote
        remote_tmp = f"/tmp/bfg_bdfs_{Path(snapshot).name}_{ts}.dwarfs"
        scp_cmd = bfg_instance._sshstr.split()  # e.g. ['ssh', 'user@host']
        host = scp_cmd[-1] if scp_cmd else None

        if host:
            transfer_ok = _run(
                ["scp", archive, f"{host}:{remote_tmp}"], check=False
            ).returncode == 0
        else:
            # Local "remote" — just copy
            shutil.copy2(archive, remote_tmp)
            transfer_ok = True

        if not transfer_ok:
            log.warning("bdfs_push: scp of .dwarfs failed; falling back to plain push")
            return bfg_instance.push(subvol, snapshot, remote_subvol, parent, clonesrcs)

        # Step 3: import on the remote side
        bdfs = _bdfs_bin()
        remote_snapshot_name = Path(snapshot).name
        remote_snapshot_path = str(
            Path(remote_subvol).parent / ".bfg_snapshots" / remote_snapshot_name
        )
        bfg_instance._remote_cmd([
            bdfs, "snapshot", "promote",
            "--from-dwarfs", remote_tmp,
            "--output", remote_snapshot_path,
        ])
        # Clean up remote temp file
        bfg_instance._remote_cmd(["rm", "-f", remote_tmp])

        log.info("bdfs_push: compressed push complete -> %s", remote_snapshot_path)
        from btrfsgit.btrfsgit import Res  # local import to avoid circular deps
        return Res(remote_snapshot_path)

    finally:
        # Always clean up local temp archive
        try:
            os.unlink(archive)
        except OSError:
            pass


def bdfs_pull(bfg_instance, remote_snapshot: str, local_subvol: str,
              parent=None, clonesrcs=None, compress: bool = True):
    """Pull a snapshot from the remote, optionally via DwarFS compression.

    Mirror of ``bdfs_push`` for the pull direction.  Falls back to
    ``bfg_instance.pull()`` when bdfs is unavailable or ``compress=False``.
    """
    if clonesrcs is None:
        clonesrcs = []

    if not compress or _bdfs_bin() is None:
        log.debug("bdfs_pull: falling back to plain btrfs-send pull")
        return bfg_instance.pull(remote_snapshot, local_subvol, parent, clonesrcs)

    ts = int(time.time())
    remote_tmp = f"/tmp/bfg_bdfs_{Path(remote_snapshot).name}_{ts}.dwarfs"

    try:
        # Step 1: export on the remote side
        bdfs = _bdfs_bin()
        bfg_instance._remote_cmd([
            bdfs, "snapshot", "demote",
            "--to-dwarfs", remote_snapshot,
            "--output", remote_tmp,
        ])

        # Step 2: transfer the .dwarfs file locally
        local_tmp = f"/tmp/bfg_bdfs_{Path(remote_snapshot).name}_{ts}.dwarfs"
        scp_cmd = bfg_instance._sshstr.split()
        host = scp_cmd[-1] if scp_cmd else None

        if host:
            transfer_ok = _run(
                ["scp", f"{host}:{remote_tmp}", local_tmp], check=False
            ).returncode == 0
        else:
            shutil.copy2(remote_tmp, local_tmp)
            transfer_ok = True

        if not transfer_ok:
            log.warning("bdfs_pull: scp of .dwarfs failed; falling back to plain pull")
            return bfg_instance.pull(remote_snapshot, local_subvol, parent, clonesrcs)

        # Step 3: import locally
        local_snapshot_name = Path(remote_snapshot).name
        local_snapshot_path = str(
            Path(local_subvol).parent / ".bfg_snapshots" / local_snapshot_name
        )
        result = import_from_dwarfs(local_tmp, local_snapshot_path)

        if result is None:
            log.warning("bdfs_pull: local import failed; falling back to plain pull")
            return bfg_instance.pull(remote_snapshot, local_subvol, parent, clonesrcs)

        # Clean up remote temp file
        bfg_instance._remote_cmd(["rm", "-f", remote_tmp])

        log.info("bdfs_pull: compressed pull complete -> %s", local_snapshot_path)
        from btrfsgit.btrfsgit import Res
        return Res(local_snapshot_path)

    finally:
        try:
            os.unlink(local_tmp)
        except (OSError, UnboundLocalError):
            pass
