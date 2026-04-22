# frzr-meta-root

**frzr-meta-root** is a unified fork of [frzr](https://gitlab.com/openos-project/upstream-mirrors/frzr) and [ABRoot v2](https://gitlab.com/openos-project/upstream-mirrors/ab-root), rewritten in Go with [Incus](https://gitlab.com/openos-project/incus_deving/incus) as the sole OCI backend — no Docker, no Podman, no Buildah.

---

## What problem it solves

Linux immutable-root systems need two things:

1. **A way to deploy OS images atomically** — so a failed update never leaves the system in a broken state.
2. **A way to pull those images from a registry** — without dragging in a full container runtime.

frzr solved (1) with btrfs read-only subvolumes. ABRoot v2 solved (1) with A/B partition transactions and (2) with a Podman/Buildah stack (~15 packages). Neither project does both well, and both require Docker-ecosystem dependencies for what is fundamentally a simple image-pull-and-extract operation.

frzr-meta-root merges the two deployment models into a single binary and replaces the entire Podman/Buildah stack with the [Incus Go client](https://pkg.go.dev/github.com/lxc/incus/v6/client), which already supports OCI registries natively.

---

## Deploy modes

Select via `deployMode` in `/etc/frzr-meta-root/frzr-meta-root.json`:

| Mode | Mechanism | Best for |
|---|---|---|
| `ab` (default) | A/B partition transactions | New installs, strongest atomicity |
| `btrfs` | Read-only btrfs subvolumes | Existing frzr systems, btrfs-only setups |

Both modes pull images from any OCI registry via Incus. The running system is never modified — changes take effect after reboot.

---

## How it works

```
OCI registry (ghcr.io, docker.io, …)
        │
        │  incus remote add oci-registry https://ghcr.io --protocol=oci
        ▼
   Incus daemon  ──────────────────────────────────────────────────────┐
        │                                                              │
        │  PullImage()        ExportRootFs()       BuildImage()        │
        ▼                          ▼                    ▼             │
  local image cache         rootfs tarball      incus exec + publish  │
        │                          │                    │             │
        └──────────────────────────┴────────────────────┘             │
                                   │                                  │
                    ┌──────────────┴──────────────┐                   │
                    │                             │                   │
              A/B mode                      btrfs mode               │
                    │                             │                   │
         future partition               new subvolume                │
         (rsync rootfs in)           (btrfs subvol create)           │
                    │                             │                   │
              update GRUB               update boot entry             │
                    │                             │                   │
                    └──────────────┬──────────────┘                   │
                                   │                                  │
                              reboot → new root                       │
                                                                      │
  Local package changes (A/B mode only):                              │
    frzr-meta-root pkg add vim  →  stage in /etc/frzr-meta-root/  ───┘
    frzr-meta-root pkg apply    →  BuildImage() → ExportRootFs()
```

---

## Removed dependencies

ABRoot v2 required these packages from the Docker/Podman ecosystem. All are gone:

| Removed | Replaced by |
|---|---|
| `github.com/containers/buildah` | `incus exec` + `incus publish` via Go client |
| `go.podman.io/image/v5` | `incus.InstanceServer` image API |
| `go.podman.io/storage` | Incus storage pool API |
| `go.podman.io/common` | `github.com/lxc/incus/v6/shared/api` |
| `github.com/vanilla-os/prometheus` | `core/incus.go` |
| `github.com/docker/docker` | removed |
| `github.com/fsouza/go-dockerclient` | removed |
| `github.com/moby/buildkit` | removed |
| frzr GitHub Releases API | Incus OCI remote |

---

## Quick start

### Prerequisites

- Incus installed and running (`incus version`)
- A UEFI system
- An OCI registry containing your OS image

### 1. Add your registry as an Incus remote

```sh
# Example: GitHub Container Registry
incus remote add oci-registry https://ghcr.io --protocol=oci
```

### 2. Build

```sh
git clone https://gitlab.com/openos-project/linux-kernel_filesystem_deving/frzr-meta-root
cd frzr-meta-root/frzr-meta-root
go build -o /usr/bin/frzr-meta-root .
```

### 3. Configure

```sh
mkdir -p /etc/frzr-meta-root
cp config/frzr-meta-root.json /etc/frzr-meta-root/frzr-meta-root.json
```

Edit `/etc/frzr-meta-root/frzr-meta-root.json` — at minimum set:

```json
{
    "incusRemote": "oci-registry",
    "name": "myorg/myos",
    "tag": "stable",
    "deployMode": "ab"
}
```

### 4. Bootstrap a new disk

```sh
# A/B partition layout (default)
frzr-meta-root bootstrap --disk /dev/sda

# btrfs subvolume layout (frzr-compatible)
frzr-meta-root bootstrap --disk /dev/sda --mode btrfs
```

### 5. Day-to-day operations

```sh
# Check for an update
frzr-meta-root upgrade --check

# Pull and stage the latest image (takes effect after reboot)
frzr-meta-root upgrade

# Stage local package changes (A/B mode only)
frzr-meta-root pkg add vim htop
frzr-meta-root pkg apply

# Roll back to the previous state
frzr-meta-root rollback

# Show current image and partition state
frzr-meta-root status
```

---

## Migrating from frzr

1. Install Incus and add your image registry as an OCI remote.
2. Set `"deployMode": "btrfs"` — this reuses your existing `frzr_root` btrfs volume and `frzr_efi` partition unchanged.
3. Run `frzr-meta-root upgrade`. Images are now pulled via Incus instead of the GitHub Releases API; deployment is identical btrfs subvolume receive.

## Migrating from ABRoot v2

1. Install Incus and add your OCI registry as a remote.
2. Copy `/etc/abroot/abroot.json` → `/etc/frzr-meta-root/frzr-meta-root.json` and add:
   ```json
   "incusSocket": "/var/lib/incus/unix.socket",
   "incusRemote": "oci-registry"
   ```
3. Run `frzr-meta-root upgrade`. The A/B transaction engine is identical to ABRoot v2; only the image backend changes.

---

## bdfs integration

frzr-meta-root optionally integrates with [btrfs-dwarfs-framework](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btrfs-dwarfs-framework) to archive old deployments as compressed DwarFS images before they are deleted. This allows any previous OS deployment to be mounted read-only later via `bdfs blend mount`.

### How it works

When `PruneDeployments` removes an old deployment subvolume, it calls `BdfsPostDeployHook` first:

```
old deployment subvolume
        │
        ▼
BdfsPostDeployHook()
        │
        ├── bdfs absent or disabled? → skip (no-op)
        │
        └── bdfs present + enabled?
                │
                ▼
        bdfs snapshot demote --to-dwarfs <subvol> --output <archive>
                │
                ▼
        /var/lib/bdfs/archives/frzr/<name>_<timestamp>.dwarfs
                │
                ▼
        btrfs subvolume delete <subvol>
```

### Configuration

The hook reads `/etc/bdfs/bdfs.conf`. Relevant keys:

```ini
[archive]
dir = /var/lib/bdfs/archives/frzr   # where .dwarfs archives are written

[general]
enabled = true                       # set to false to disable the hook entirely
```

If the file is absent, defaults are used. The hook is always non-fatal — a failed archive is logged as a warning and pruning continues.

### Mounting an archived deployment

```bash
# List archived deployments
ls /var/lib/bdfs/archives/frzr/

# Mount one read-only via the blend layer
bdfs blend mount \
    --dwarfs-image /var/lib/bdfs/archives/frzr/myos_20260101T120000Z.dwarfs \
    --mountpoint /mnt/old-deploy \
    --userspace   # use fuse-overlayfs if bdfs_blend kernel module is unavailable
```

## Repository layout

```
frzr-meta-root/
├── cmd/              # cobra CLI (upgrade, pkg, rollback, status, kargs, bootstrap)
├── core/
│   ├── incus.go      # OCIBackend implementation via Incus Go client
│   ├── deploy.go     # btrfs subvolume deployment; calls BdfsPostDeployHook on prune
│   ├── bdfs.go       # BdfsConfig loader, BdfsArchiveDeployment, BdfsListArchives
│   ├── deploy_bdfs_hook.go  # BdfsPostDeployHook thin wrapper
│   ├── system.go     # A/B transaction engine (ABRoot model)
│   └── stubs.go      # placeholders for ABRoot core files pending port
├── settings/         # unified config schema
├── config/           # example config file
└── systemd/          # autoupdate service + timer
```

See [`frzr-meta-root/ARCHITECTURE.md`](frzr-meta-root/ARCHITECTURE.md) for the full component map, design decisions, and Incus API mapping.

---

## Status

The core architecture, Incus backend, and both deploy-mode paths are implemented. The following ABRoot v2 files are stubbed and need to be ported (copy + module path rename, no OCI changes required):

- `core/root.go`, `core/disk-manager.go` — partition discovery
- `core/chroot.go` — chroot helper
- `core/grub.go`, `core/kernel.go`, `core/kargs.go` — boot management
- `core/packages.go`, `core/package-diff.go` — package queue
- `core/image.go`, `core/image-recipe.go` — ABImage struct, recipe writer
- `core/checks.go`, `core/integrity.go`, `core/atomic-io.go`, `core/rsync.go`, `core/logging.go`

---

## License

GPLv3 — same as both upstream projects ([frzr](https://gitlab.com/openos-project/upstream-mirrors/frzr), [ABRoot v2](https://gitlab.com/openos-project/upstream-mirrors/ab-root)).
