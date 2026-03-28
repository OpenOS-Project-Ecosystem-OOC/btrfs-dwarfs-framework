# BTRFS+DwarFS Framework

A hybrid filesystem framework that blends [BTRFS](https://github.com/kdave/btrfs-devel) and [DwarFS](https://github.com/mhx/dwarfs) into a unified namespace, combining BTRFS's mutable Copy-on-Write semantics with DwarFS's extreme compression ratios (10–16×).

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
│   └── uapi/bdfs_ioctl.h         # Kernel↔userspace ioctl interface (18 ioctls)
│
├── kernel/
│   └── btrfs_dwarfs/
│       ├── bdfs_main.c           # Module init, /dev/bdfs_ctl, partition registry
│       ├── bdfs_blend.c          # bdfs_blend VFS type, unified namespace
│       ├── bdfs_btrfs_part.c     # BTRFS-backed partition backend
│       ├── bdfs_dwarfs_part.c    # DwarFS-backed partition backend
│       ├── bdfs_internal.h       # Internal kernel declarations
│       ├── Kbuild                # Kernel build rules
│       └── Kconfig               # Kernel config options
│
├── userspace/
│   ├── daemon/
│   │   ├── bdfs_daemon.c         # Lifecycle, worker pool, main loop
│   │   ├── bdfs_exec.c           # mkdwarfs/dwarfs/btrfs tool wrappers
│   │   ├── bdfs_jobs.c           # Job handlers (export/import/mount/snapshot)
│   │   ├── bdfs_netlink.c        # Netlink event listener
│   │   └── bdfs_socket.c         # Unix socket server for CLI
│   ├── cli/
│   │   ├── bdfs_main.c           # Entry point, global options, dispatch
│   │   ├── bdfs_partition.c      # partition add/remove/list/show
│   │   ├── bdfs_export_import.c  # export, import
│   │   ├── bdfs_mount.c          # mount, umount, blend mount/umount
│   │   ├── bdfs_snapshot_promote_demote.c  # snapshot, promote, demote
│   │   └── bdfs_status.c         # status
│   └── CMakeLists.txt
│
├── tests/
│   ├── integration/              # Loopback-device test suites (requires root)
│   │   ├── lib.sh                # Shared helpers, loopback setup, assertions
│   │   ├── test_dwarfs_partition.sh
│   │   ├── test_btrfs_partition.sh
│   │   ├── test_blend_layer.sh
│   │   ├── test_snapshot_lifecycle.sh
│   │   └── run_all.sh
│   └── unit/                     # Unit tests (no root required)
│       ├── test_uuid.c
│       ├── test_compression.c
│       └── test_job_alloc.c
│
├── configs/
│   ├── bdfs.conf                 # Framework configuration
│   └── bdfs_daemon.service       # systemd service unit
│
├── doc/
│   ├── architecture.md           # Detailed architecture and data flow diagrams
│   ├── bdfs.1                    # CLI man page
│   └── bdfs_daemon.8             # Daemon man page
│
└── Makefile                      # Top-level build (kernel + userspace)
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
bdfs blend mount \
    --btrfs-uuid <uuid> \
    --dwarfs-uuid <uuid> \
    --mountpoint /mnt/blend \
    --writeback
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

### Status

```bash
bdfs status
bdfs status --partition <uuid>
bdfs status --json
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
- `test_blend_layer.sh` — read routing, copy-up on write, promote/demote, round-trip integrity
- `test_snapshot_lifecycle.sh` — incremental snapshot chains, independent image mounts, size progression

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

## Known Limitations

- **Blend layer lookup is a skeleton.** The `bdfs_blend` VFS type is registered and the blend mount/umount ioctls are wired, but full inode routing across the BTRFS/DwarFS boundary in `bdfs_blend_lookup` requires kernel-version-specific FUSE internal API work and is not yet complete.
- **Incremental export not wired.** The `--incremental` flag is accepted but `btrfs send -p <parent>` is not yet passed through in the daemon job handler.
- **Read-only import is stubbed.** The `--readonly` flag on `bdfs import` constructs the `btrfs property set ro true` call but does not execute it yet.

---

## License

GPL-2.0-or-later. See individual file headers.

## References

- [kdave/btrfs-devel](https://github.com/kdave/btrfs-devel) — BTRFS kernel development tree
- [mhx/dwarfs](https://github.com/mhx/dwarfs) — DwarFS compressed filesystem
