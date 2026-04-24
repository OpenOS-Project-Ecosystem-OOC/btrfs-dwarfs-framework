# BTRFS+DwarFS Framework

A hybrid filesystem framework that blends [BTRFS](https://github.com/kdave/btrfs-devel) and [DwarFS](https://github.com/mhx/dwarfs) into a unified namespace, combining BTRFS's mutable Copy-on-Write semantics with DwarFS's extreme compression ratios (10–16×).

---

## What's New

### Userspace blend fallback (fuse-overlayfs)
When the `bdfs_blend` kernel module is unavailable (e.g. on a stock kernel), the blend layer now falls back automatically to [fuse-overlayfs](https://github.com/containers/fuse-overlayfs). Pass `--userspace` to `bdfs blend mount` to force this path, or set `userspace_fallback = true` in `bdfs.conf`. The daemon detects `ENODEV` on the ioctl and retries via `BDFS_JOB_MOUNT_BLEND_USERSPACE`.

### O(1) mount table lookup
The mount table previously used a linear TAILQ scan for every umount and lookup. It now maintains a parallel FNV-1a open-addressing hash index (`bdfs_mount_index`, 256 slots, linear probing with tombstones) in `userspace/daemon/bdfs_mount_table.c`, giving O(1) lookup by mount point path.

### Snapshot pruning (`bdfs snapshot prune`)
```bash
bdfs snapshot prune /mnt/btrfs/subvol \
    --keep 5 \
    --pattern "backup_*" \
    --demote-first \
    --dry-run
```
Sorts snapshots by mtime, keeps the N most recent, and deletes the rest. `--demote-first` archives each pruned snapshot as a DwarFS image before deletion. `--dry-run` reports what would be deleted without acting.

### Home directory snapshots (`bdfs home`)
Per-user snapshot management with [ecryptfs](https://www.ecryptfs.org/) support:
```bash
bdfs home init /home/alice        # set up snapshot tracking for a home dir
bdfs home snapshot /home/alice    # take a snapshot
bdfs home demote /home/alice      # compress oldest snapshots to DwarFS
```
User configuration is read from `~/.config/bdfs/bdfs.conf`.

### Maintenance operations
Weekly scrub and monthly balance are now managed by systemd units installed via `bdfs setup` or `boot/install.sh --maintenance`:

| Unit | Schedule | Notes |
|---|---|---|
| `bdfs-scrub.timer` | Weekly | Reads device list from `/etc/bdfs/scrub.conf` |
| `bdfs-balance.timer` | Monthly | Skips if a balance is already running |

### Distro-agnostic setup
All distro-specific assumptions have been removed. `bdfs setup fstab` generates `/etc/fstab` entries by introspecting `btrfs subvolume list` output at runtime — no hardcoded paths, package managers, usernames, or desktop environments. See [`doc/distro-agnostic.md`](doc/distro-agnostic.md) for the full policy and [`doc/bootloader-integration.md`](doc/bootloader-integration.md) for GRUB2/systemd-boot/Limine setup.

### Full socket command dispatch
`userspace/daemon/bdfs_socket.c` now implements all commands used by the CLI and GUI clients:

| Command | Action |
|---|---|
| `list_partitions` | Walks `/proc/mounts` for btrfs entries |
| `list_images` | Scans archive dir for `.dwarfs` files |
| `list_mounts` | Returns tracked blend mount points |
| `blend_mount` | Enqueues kernel or userspace blend mount job |
| `umount` | Enqueues umount job |
| `mount` | Enqueues bare DwarFS mount job |
| `import` | Enqueues `BDFS_JOB_IMPORT_FROM_DWARFS` |
| `demote` | Enqueues `BDFS_JOB_EXPORT_TO_DWARFS` |
| `prune` | Enqueues `BDFS_JOB_PRUNE` |
| `status` | Returns worker count, queue depth, active mounts |
| `ping` | Liveness check |

---

## Concept

Two technologies, two strengths:

| | BTRFS | DwarFS |
|---|---|---|
| **Mode** | Read/write | Read-only |
| **Storage** | Block device | Single image file |
| **Snapshots** | Native CoW subvolumes | N/A |
| **Compression** | Transparent, per-block | 10–16× via similarity hashing |
| **Use case** | Live, active data | Archival, read-mostly data |

This framework bridges them with two partition types and a blend layer:

### DwarFS-backed Partition
Stores BTRFS subvolumes and snapshots as compressed `.dwarfs` image files. A BTRFS subvolume is exported via `btrfs send | btrfs receive | mkdwarfs` into a single self-contained image. Ideal for archiving versioned data at maximum compression.

### BTRFS-backed Partition
Stores `.dwarfs` image files on a live BTRFS filesystem. The images gain BTRFS's CoW semantics (no partial-write corruption), per-file checksumming, and snapshot capability — so you can take point-in-time snapshots of entire image collections.

### Blend Layer
Merges a BTRFS upper layer and one or more DwarFS lower layers into a single coherent namespace. Reads fall through from BTRFS to DwarFS; writes always land on BTRFS with automatic copy-up.

```
┌──────────────────────────────────────────────┐
│              Blend Namespace                 │
│              /mnt/blend/                     │
│                                              │
│  READ:  BTRFS upper → DwarFS lower fallback  │
│  WRITE: always to BTRFS upper (copy-up)      │
└──────────┬───────────────────────┬───────────┘
           │                       │
  ┌────────▼────────┐   ┌──────────▼──────────┐
  │  BTRFS Upper    │   │   DwarFS Lower(s)   │
  │  (writable)     │   │   (compressed)      │
  └─────────────────┘   └─────────────────────┘
```

**Promote** — extract a DwarFS-backed path into a writable BTRFS subvolume.  
**Demote** — compress a BTRFS subvolume into a DwarFS image, optionally deleting the subvolume to reclaim space.

---

## Repository Layout

```
btrfs-dwarfs-framework/
├── include/
│   ├── btrfs_dwarfs/types.h      # Shared type definitions
│   └── uapi/bdfs_ioctl.h         # Kernel↔userspace ioctl interface
│
├── kernel/
│   └── btrfs_dwarfs/
│       ├── bdfs_main.c           # Module init, /dev/bdfs_ctl, partition registry
│       ├── bdfs_blend.c          # bdfs_blend VFS type, unified namespace
│       ├── bdfs_btrfs_part.c     # BTRFS-backed partition backend
│       ├── bdfs_dwarfs_part.c    # DwarFS-backed partition backend
│       ├── bdfs_internal.h       # Internal kernel declarations
│       ├── Kbuild
│       └── Kconfig
│
├── userspace/
│   ├── daemon/
│   │   ├── bdfs_daemon.c         # Lifecycle, worker pool, main loop
│   │   ├── bdfs_exec.c           # mkdwarfs/dwarfs/btrfs/fuse-overlayfs wrappers
│   │   ├── bdfs_jobs.c           # Job handlers (export/import/mount/prune/userspace-blend)
│   │   ├── bdfs_mount_table.c    # FNV-1a hash index for O(1) mount lookups
│   │   ├── bdfs_netlink.c        # Netlink event listener
│   │   └── bdfs_socket.c         # Unix socket server — full command dispatch
│   ├── cli/
│   │   ├── bdfs_main.c           # Entry point, dispatch table
│   │   ├── bdfs_partition.c      # partition add/remove/list/show
│   │   ├── bdfs_export_import.c  # export, import
│   │   ├── bdfs_mount.c          # mount, umount, blend mount/umount (--userspace)
│   │   ├── bdfs_snapshot_promote_demote.c  # snapshot, promote, demote, prune
│   │   ├── bdfs_home.c           # home init/snapshot/demote
│   │   ├── bdfs_setup.c          # setup fstab/check
│   │   ├── bdfs_userconf.c/.h    # ~/.config/bdfs/bdfs.conf parser
│   │   └── bdfs_status.c         # status
│   └── CMakeLists.txt
│
├── configs/
│   ├── bdfs.conf                 # Framework configuration
│   ├── bdfs_daemon.service       # systemd daemon unit
│   ├── bdfs-scrub.{sh,service,timer}    # Weekly scrub
│   └── bdfs-balance.{sh,service,timer}  # Monthly balance
│
├── tools/
│   ├── autosnap/                 # Package-manager snapshot hook (see below)
│   ├── homed/                    # systemd-homed identity repair (distro-agnostic)
│   └── setup/
│       └── bdfs-genfstab.sh      # Runtime fstab generator
│
├── boot/
│   └── install.sh                # Installer (--maintenance, --homed-check flags)
│
├── tests/
│   ├── integration/              # Loopback-device test suites (requires root)
│   │   ├── lib.sh
│   │   ├── test_dwarfs_partition.sh
│   │   ├── test_btrfs_partition.sh
│   │   ├── test_blend_layer.sh   # Includes userspace blend (fuse-overlayfs) tests
│   │   ├── test_snapshot_lifecycle.sh
│   │   └── run_all.sh
│   └── unit/
│       ├── test_uuid.c
│       ├── test_compression.c
│       └── test_job_alloc.c
│
├── doc/
│   ├── architecture.md
│   ├── bootloader-integration.md # GRUB2 / systemd-boot / Limine
│   ├── distro-agnostic.md        # Policy: no hardcoded distro assumptions
│   ├── bdfs.1
│   └── bdfs_daemon.8
│
└── integrations/                 # Git submodules — external projects that integrate with bdfs
    ├── frzr-meta-root/           # Immutable root manager (frzr + ABRoot v2, Incus backend)
    ├── btr-fs-git/               # Git-like workflow for btrfs subvolumes (BFG)
    ├── btrfs-assistant/          # Qt6 GUI with BDFS tab for daemon interaction
    └── ashos/                    # Immutable tree-shaped meta-distribution (AshOS)
```

---

## Building

### Dependencies

| Tool | Purpose |
|---|---|
| Linux kernel headers ≥ 5.15 | Kernel module build |
| `btrfs-progs` | `btrfs send/receive/snapshot/subvolume` |
| `mkdwarfs` | DwarFS image creation |
| `dwarfs` (FUSE3) | DwarFS image mounting |
| `dwarfsextract` | DwarFS image extraction |
| `dwarfsck` | DwarFS image verification |
| `fuse-overlayfs` | Userspace blend fallback (optional) |
| CMake ≥ 3.16 | Userspace build |
| pthreads | Daemon worker pool |

Install DwarFS tools from [mhx/dwarfs releases](https://github.com/mhx/dwarfs/releases) (pre-built static binaries available for most architectures).

### Build

```bash
# Kernel module + userspace (daemon + CLI)
make all

# Kernel module only
make kernel KDIR=/lib/modules/$(uname -r)/build

# Userspace only
make userspace

# Install (requires root for kernel module)
sudo make install

# Load the kernel module
sudo insmod kernel/btrfs_dwarfs/btrfs_dwarfs.ko
```

### CMake options

```bash
cmake -S userspace -B build \
    -DBUILD_DAEMON=ON \
    -DBUILD_CLI=ON \
    -DBUILD_TESTS=OFF \
    -DENABLE_ASAN=OFF \
    -DCMAKE_INSTALL_PREFIX=/usr/local
cmake --build build --parallel
```

---

## Usage

### 1. Start the daemon

```bash
sudo systemctl start bdfs_daemon
# or in the foreground for debugging:
sudo bdfs_daemon -f -v
```

### 2. Register partitions

```bash
# DwarFS-backed: stores BTRFS snapshots as compressed images
bdfs partition add \
    --type dwarfs-backed \
    --device /dev/sdb1 \
    --label archive \
    --mount /mnt/archive

# BTRFS-backed: stores DwarFS image files with CoW + checksums
bdfs partition add \
    --type btrfs-backed \
    --device /dev/sdc1 \
    --label images \
    --mount /mnt/images
```

### 3. Export a BTRFS subvolume to a DwarFS image

```bash
# Find the subvolume ID
btrfs subvolume list /mnt/data

# Export it (creates a read-only snapshot, runs mkdwarfs, cleans up)
bdfs export \
    --partition <dwarfs-backed-uuid> \
    --subvol-id 256 \
    --btrfs-mount /mnt/data \
    --name myapp_v1 \
    --compression zstd \
    --verify
```

### 4. Mount a DwarFS image

```bash
bdfs mount \
    --partition <dwarfs-backed-uuid> \
    --image-id 1 \
    --mountpoint /mnt/myapp_v1 \
    --cache-mb 512
```

### 5. Import a DwarFS image into a BTRFS subvolume

```bash
bdfs import \
    --partition <btrfs-backed-uuid> \
    --image-id 1 \
    --btrfs-mount /mnt/data \
    --subvol-name myapp_restored
```

### 6. Snapshot the BTRFS container of a DwarFS image

```bash
# Point-in-time CoW snapshot of the subvolume holding the image file
bdfs snapshot \
    --partition <btrfs-backed-uuid> \
    --image-id 1 \
    --name images_snap_20250101 \
    --readonly
```

### 7. Mount the blend namespace

```bash
# Kernel blend (requires bdfs_blend module)
bdfs blend mount \
    --btrfs-uuid <uuid> \
    --dwarfs-uuid <uuid> \
    --mountpoint /mnt/blend

# Userspace blend via fuse-overlayfs (no kernel module needed)
bdfs blend mount \
    --btrfs-uuid <uuid> \
    --dwarfs-uuid <uuid> \
    --mountpoint /mnt/blend \
    --userspace
```

### 8. Promote / demote

```bash
# Promote: make a DwarFS-backed path writable (extract to BTRFS subvolume)
bdfs promote \
    --blend-path /mnt/blend/myapp \
    --subvol-name myapp_live

# Demote: compress a BTRFS subvolume to DwarFS and reclaim space
bdfs demote \
    --blend-path /mnt/blend/myapp_live \
    --image-name myapp_archived \
    --compression zstd \
    --delete-subvol
```

### 9. Prune snapshots

```bash
# Keep 5 most recent, archive older ones as DwarFS before deleting
bdfs snapshot prune /mnt/data --keep 5 --demote-first

# Preview without making changes
bdfs snapshot prune /mnt/data --keep 5 --dry-run
```

### 10. Home directory snapshots

```bash
bdfs home init /home/alice
bdfs home snapshot /home/alice
bdfs home demote /home/alice
```

### 11. Distro-agnostic setup

```bash
# Generate /etc/fstab from live btrfs subvolume introspection
bdfs setup fstab

# Verify setup health
bdfs setup check

# Install weekly scrub + monthly balance timers
sudo bash boot/install.sh --maintenance
```

### Status

```bash
bdfs status
bdfs status --json
```

---

## autosnap — package-manager snapshot hook

`tools/autosnap/` integrates automatic pre/post snapshots into your package
manager so every install, upgrade, or removal is preceded by a recoverable
system snapshot.

**Primary backend:** `bdfs snapshot` (daemon-managed, supports `--demote-first`
archival and `bdfs snapshot prune` retention).  
**Fallback backend:** raw `btrfs subvolume snapshot` (used automatically when
the bdfs daemon is not running).

### Install

```bash
sudo make install-autosnap
```

This installs hooks for all detected package managers (APT, DNF, Pacman,
Zypper). Edit `/etc/autosnap.conf` to configure retention and demote behaviour.

### How it works

```
apt install / dnf upgrade / pacman -Syu / zypper up
        │
        ▼
  [pre hook]  autosnap pre <pm>
        │  → bdfs snapshot --name autosnap-pre-<date>-<pm>
        │  → bdfs snapshot prune --pattern "autosnap-*" --keep $MAX_SNAPSHOTS
        │
  [package manager runs]
        │
  [post hook]  autosnap post <pm>
        │  → bdfs snapshot --name autosnap-post-<date>-<pm>
```

### Management

```bash
# List autosnap snapshots
autosnap list

# Show what changed between pre and post
autosnap status autosnap-pre-2026-01-01T12:00:00-apt \
                autosnap-post-2026-01-01T12:00:01-apt

# Roll back to a pre-snapshot
sudo autosnap rollback autosnap-pre-2026-01-01T12:00:00-apt

# Delete a snapshot
autosnap delete autosnap-pre-2026-01-01T12:00:00-apt

# Skip snapshotting for one run
SKIP_AUTOSNAP= sudo apt upgrade
```

### Retention and DwarFS archival

Retention is enforced via `bdfs snapshot prune`. With `BDFS_DEMOTE_ON_PRUNE=true`
in `/etc/autosnap.conf`, snapshots beyond `MAX_SNAPSHOTS` are compressed into
DwarFS archives before deletion rather than discarded outright:

```bash
# Equivalent manual command
bdfs snapshot prune \
    --partition <uuid> \
    --btrfs-mount / \
    --keep 5 \
    --pattern "autosnap-*" \
    --demote-first \
    --compression zstd
```

### Uninstall

```bash
sudo make uninstall-autosnap
```

---

## Testing

### Integration tests (requires root, btrfs-progs, dwarfs)

Tests run against loopback devices — no real block devices needed. Prerequisites are checked per-suite; missing tools cause a graceful skip rather than a failure.

```bash
sudo make test
# or directly:
sudo bash tests/integration/run_all.sh
```

Suites:
- `test_dwarfs_partition.sh` — export pipeline, compression ratio, FUSE mount, integrity, read-only enforcement
- `test_btrfs_partition.sh` — image storage, CoW semantics, snapshot independence, import pipeline, scrub
- `test_blend_layer.sh` — kernel blend, userspace blend (fuse-overlayfs), copy-up on write, promote/demote, round-trip integrity
- `test_snapshot_lifecycle.sh` — incremental snapshot chains, prune with demote-first, size progression

### Unit tests (no root required)

```bash
make check
# runs: test_uuid, test_compression, test_job_alloc
```

---

## Architecture

See [`doc/architecture.md`](doc/architecture.md) for component diagrams and full data flow for the export and import pipelines.

Man pages:
```bash
man doc/bdfs.1
man doc/bdfs_daemon.8
```

---

## Ecosystem

The `integrations/` directory contains projects that extend or consume btrfs-dwarfs-framework, tracked as git submodules. Each stays in its own repository with its own CI and release cadence.

To initialise after cloning:

```bash
git submodule update --init --recursive integrations/
```

To update a specific integration to its latest upstream commit:

```bash
git submodule update --remote integrations/frzr-meta-root
git add integrations/frzr-meta-root
git commit -m "integrations/frzr-meta-root: update to latest"
```

### frzr-meta-root ([source](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/frzr-meta-root))

A unified fork of [frzr](https://gitlab.com/openos-project/upstream-mirrors/frzr) and ABRoot v2 that uses [Incus](https://gitlab.com/openos-project/incus_deving/incus) as the sole OCI backend for immutable root filesystem management. Integrates with bdfs via `core/bdfs.go`, which archives retiring frzr deployments as DwarFS images before deletion — so old OS states are compressed and recoverable rather than discarded.

Supports two deployment models: A/B partitions (strongest atomicity) and btrfs subvolumes (drop-in for existing frzr installs).

### btr-fs-git / BFG ([source](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btr-fs-git))

A Python tool that provides git-like workflow (`push`, `pull`, `commit`, `checkout`, `stash`) for btrfs subvolumes across multiple machines. Integrates with bdfs via `btrfsgit/btrfsgit_bdfs_patch.py`, which monkey-patches `push`/`pull` to accept `--compress-via-bdfs`: snapshots are compressed as DwarFS images in transit, reducing transfer size by 10–16×, then decompressed on the receiving end.

### btrfs-assistant ([source](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btrfs-assistant))

A Qt6 GUI for btrfs filesystem management. Includes a **BDFS tab** (`src/ui/BdfsPartitionsTab`) that communicates with the bdfs daemon over its Unix socket (`/run/bdfs/bdfs.sock`), providing a graphical interface for:

- Viewing BTRFS partitions, DwarFS images, and active blend mounts
- Mounting and unmounting blend layers (kernel or fuse-overlayfs mode)
- Demoting snapshots to compressed DwarFS images
- Importing DwarFS images back as BTRFS subvolumes
- Pruning snapshots with keep-N policy, name pattern filter, demote-before-delete, and dry-run mode

The BDFS tab disables itself gracefully when the daemon is not running.

### ashos ([source](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/ashos))

AshOS is a distro-agnostic immutable meta-distribution that wraps any bootstrappable Linux distribution (Arch, Debian, Fedora, Alpine, etc.) with a tree-shaped btrfs snapshot hierarchy. Each snapshot is a full read-only subvolume; package installation happens by chrooting into a snapshot, then deploying it as the next boot target.

Integrates with bdfs via `src/bdfs_hook.py`, which monkey-patches `delete_node()` to demote each snapshot's rootfs subvolume to a DwarFS archive before the btrfs subvolume is deleted. Old OS states become recoverable compressed archives rather than being discarded.

**Enabling the hook** (disabled by default):

```ini
# /etc/bdfs/bdfs.conf
[ashos]
demote_on_delete = true
archive_dir = /var/lib/bdfs/archives/ashos
compression = zstd
```

Or set `BDFS_DEMOTE_ON_DELETE=1` in the environment. If bdfs is absent or the demote fails, ash falls back to plain `btrfs subvolume delete` — no snapshot is ever lost due to a bdfs failure.

> **Note:** AshOS is licensed AGPLv3. Integration is at the subprocess boundary (`ash` calls `bdfs` as a CLI tool) — no shared linking, no license conflict.

---

## Known Limitations

- **Blend layer inode routing is a skeleton.** The `bdfs_blend` VFS type is registered and blend mount/umount ioctls are wired, but full inode routing across the BTRFS/DwarFS boundary in `bdfs_blend_lookup` requires kernel-version-specific FUSE internal API work and is not yet complete. Use `--userspace` (fuse-overlayfs) for a fully functional blend layer today.

---

## License

GPL-2.0-or-later. See individual file headers.

## References

- [kdave/btrfs-devel](https://github.com/kdave/btrfs-devel) — BTRFS kernel development tree
- [mhx/dwarfs](https://github.com/mhx/dwarfs) — DwarFS compressed filesystem
- [containers/fuse-overlayfs](https://github.com/containers/fuse-overlayfs) — Userspace overlay filesystem (blend fallback)
