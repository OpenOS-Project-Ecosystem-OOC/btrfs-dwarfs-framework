"""
btrfsgit_bdfs_patch.py — monkey-patch to add --compress-via-bdfs to push/pull

This module is imported for its side effects.  When imported it wraps the
``push`` and ``pull`` methods on the BtrFsGit class so that they accept an
optional ``--compress-via-bdfs`` / ``COMPRESS_VIA_BDFS`` flag.

Usage (standalone):
    python -m btrfsgit.btrfsgit_bdfs_patch

Usage (as part of the normal CLI entry point):
    Import this module before ``fire.Fire(BtrFsGit)`` is called, e.g. in
    ``btrfsgit.py`` or a wrapper script:

        import btrfsgit.btrfsgit_bdfs_patch  # noqa: F401  (side-effects only)

The patch is safe to apply multiple times (idempotent).
"""

import logging

log = logging.getLogger("bfg.bdfs_patch")

_PATCHED = False


def _apply_patch():
    global _PATCHED
    if _PATCHED:
        return

    try:
        from btrfsgit.btrfsgit import BtrFsGit
        from btrfsgit.bdfs_transfer import bdfs_push, bdfs_pull
    except ImportError as exc:
        log.warning("bdfs patch: import failed (%s); skipping", exc)
        return

    _orig_push = BtrFsGit.push
    _orig_pull = BtrFsGit.pull

    def patched_push(self, SUBVOL, SNAPSHOT, REMOTE_SUBVOL,
                     PARENT=None, CLONESRCS=None, COMPRESS_VIA_BDFS=False):
        """Push SNAPSHOT to REMOTE_SUBVOL.

        Parameters
        ----------
        COMPRESS_VIA_BDFS : bool
            When True, compress the snapshot as a DwarFS image before
            transfer and decompress on the remote side.  Requires bdfs to
            be installed on both ends.  Falls back to plain btrfs-send if
            bdfs is unavailable.
        """
        if CLONESRCS is None:
            CLONESRCS = []
        if COMPRESS_VIA_BDFS:
            return bdfs_push(self, SUBVOL, SNAPSHOT, REMOTE_SUBVOL,
                             PARENT, CLONESRCS, compress=True)
        return _orig_push(self, SUBVOL, SNAPSHOT, REMOTE_SUBVOL, PARENT, CLONESRCS)

    def patched_pull(self, REMOTE_SNAPSHOT, LOCAL_SUBVOL,
                     PARENT=None, CLONESRCS=None, COMPRESS_VIA_BDFS=False):
        """Pull REMOTE_SNAPSHOT to LOCAL_SUBVOL.

        Parameters
        ----------
        COMPRESS_VIA_BDFS : bool
            When True, compress the snapshot on the remote as a DwarFS
            image, transfer it, and decompress locally.  Falls back to
            plain btrfs-send if bdfs is unavailable.
        """
        if CLONESRCS is None:
            CLONESRCS = []
        if COMPRESS_VIA_BDFS:
            return bdfs_pull(self, REMOTE_SNAPSHOT, LOCAL_SUBVOL,
                             PARENT, CLONESRCS, compress=True)
        return _orig_pull(self, REMOTE_SNAPSHOT, LOCAL_SUBVOL, PARENT, CLONESRCS)

    BtrFsGit.push = patched_push
    BtrFsGit.pull = patched_pull

    _PATCHED = True
    log.debug("bdfs patch applied to BtrFsGit.push and BtrFsGit.pull")


_apply_patch()


if __name__ == "__main__":
    # Allow running as: python -m btrfsgit.btrfsgit_bdfs_patch
    # to verify the patch applies cleanly.
    import fire
    from btrfsgit.btrfsgit import BtrFsGit
    fire.Fire(BtrFsGit)
