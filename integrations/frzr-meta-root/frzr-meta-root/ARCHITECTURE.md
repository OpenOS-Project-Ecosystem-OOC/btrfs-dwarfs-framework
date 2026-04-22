# frzr-meta-root — Architecture

## What this is

`frzr-meta-root` is a unified fork of [frzr](https://gitlab.com/openos-project/upstream-mirrors/frzr)
and [ABRoot v2](https://gitlab.com/openos-project/upstream-mirrors/ab-root) that replaces all
Docker/Podman/Buildah OCI machinery with [Incus](https://gitlab.com/openos-project/incus_deving/incus)
as the sole container/image backend.

The two upstream projects solve overlapping problems from different angles:

| Concern | frzr | ABRoot v2 |
|---|---|---|
| Immutable root | btrfs read-only subvolumes | A/B partition transactions |
| Image format | `.img` / `.img.tar.xz` (btrfs send/receive) | OCI images (Podman/Buildah stack) |
| Image source | GitHub Releases API | OCI registry (ghcr.io, etc.) |
| Local mutations | none (unlock script) | `abroot pkg` → local Containerfile build |
| Boot management | systemd-boot (`frzr.conf`) | GRUB (root-specific per partition) |
| Persistence | `/home`, `/var` btrfs subvols + overlayfs `/etc` | `/var` partition + EtcBuilder overlayfs |
| Rollback | keep previous subvolume | A/B swap |
| Language | Bash | Go |

## Unified model

`frzr-meta-root` adopts the **A/B partition model from ABRoot** (stronger atomicity guarantee)
and the **btrfs-on-top-of-LVM thin-provisioning layout** that ABRoot v2 already supports,
while replacing the entire Podman/Buildah/go-podman.io/prometheus stack with the
**Incus Go client** (`github.com/lxc/incus/v6/client`).

### Why Incus instead of Docker/Podman

ABRoot v2 currently depends on:
- `github.com/containers/buildah` — Containerfile builds
- `go.podman.io/image/v5` — image pull/inspect
- `go.podman.io/storage` — layer storage
- `go.podman.io/common` — shared types
- `github.com/vanilla-os/prometheus` — thin wrapper around the above
- `github.com/docker/docker` (indirect) — registry protocol types
- `github.com/fsouza/go-dockerclient` (indirect)

Incus already supports:
- Pulling OCI images from any registry (`incus remote add oci-docker https://docker.io --protocol=oci`)
- Exporting a container's rootfs to a tarball (`incus export`)
- Publishing a modified container back as an image (`incus publish`)
- Building images via `distrobuilder` (Containerfile-compatible)
- REST API + Go client for all of the above — no daemon socket required on the host

This means every operation ABRoot performs through Buildah/Podman can be expressed
through the Incus Go client API, and frzr's `.img` distribution can be replaced by
Incus image tarballs or OCI registry references.

## Component map

```
frzr-meta-root/
├── cmd/                    # cobra CLI (from ABRoot)
│   ├── root.go
│   ├── upgrade.go          # replaces abroot upgrade + frzr-deploy
│   ├── pkg.go              # replaces abroot pkg
│   ├── rollback.go
│   ├── kargs.go
│   ├── status.go
│   └── bootstrap.go        # replaces frzr-bootstrap (disk setup)
├── core/
│   ├── incus.go            # NEW: Incus client wrapper (replaces oci.go + prometheus)
│   ├── image.go            # ABImage struct (unchanged)
│   ├── image-recipe.go     # Containerfile-like recipe → incus build (replaces buildah)
│   ├── system.go           # ABSystem transaction engine (unchanged logic)
│   ├── root.go             # A/B partition manager (unchanged)
│   ├── disk-manager.go     # partition discovery (unchanged)
│   ├── chroot.go           # chroot helper (unchanged)
│   ├── grub.go             # GRUB config (unchanged) — or systemd-boot variant
│   ├── kargs.go            # kernel args (unchanged)
│   ├── kernel.go           # kernel copy (unchanged)
│   ├── packages.go         # package queue (unchanged)
│   ├── diff.go             # image diff via Differ API (unchanged)
│   ├── integrity.go        # integrity checks (unchanged)
│   ├── atomic-io.go        # atomic file writes (unchanged)
│   ├── rsync.go            # rsync helper (unchanged)
│   ├── checks.go           # pre-flight checks (unchanged)
│   ├── conf.go             # config loader (unchanged)
│   ├── logging.go          # verbose logging (unchanged)
│   └── deploy.go           # NEW: frzr deploy logic (btrfs subvol receive/send)
├── settings/
│   └── settings.go         # config struct (extended with Incus socket path)
├── config/
│   └── frzr-meta-root.json # example config
├── systemd/
│   ├── frzr-meta-root-autoupdate.service
│   └── frzr-meta-root-autoupdate.timer
├── go.mod
├── go.sum
├── LICENSE                 # GPLv3 (both upstreams)
└── README.md
```

## Key design decisions

### 1. Incus as the OCI backend (`core/incus.go`)

Replaces `core/oci.go` and the entire `prometheus` dependency tree.

```go
// IncusClient wraps the Incus Go client for image operations needed by
// the transaction engine. It does not require a running Incus daemon on
// the target system — it connects to a local or remote Incus socket.
type IncusClient struct {
    server incus.InstanceServer
    storagePool string
}

// PullImage pulls an OCI image from a registry via Incus and returns
// the local image fingerprint.
func (c *IncusClient) PullImage(imageRef string) (string, error)

// ExportRootFs exports the rootfs of a named image to dest directory,
// equivalent to OciExportRootFs in the original ABRoot.
func (c *IncusClient) ExportRootFs(imageRef string, recipe *ImageRecipe, dest string) error

// BuildImage applies a Containerfile-like recipe on top of a base image
// using `incus launch` + exec + `incus publish`, replacing buildah.BuildContainerFile.
func (c *IncusClient) BuildImage(base string, recipe *ImageRecipe, tag string) (string, error)

// HasUpdate checks the registry manifest digest without pulling the full image.
func (c *IncusClient) HasUpdate(currentDigest string) (string, bool, error)
```

### 2. frzr deploy path (`core/deploy.go`)

Preserves frzr's btrfs subvolume deployment model as an alternative to the
full A/B rsync path. Useful for systems that use btrfs instead of LVM.

```go
// DeploySubvolume receives a btrfs send-stream (from an Incus image export
// or a remote URL) and creates a new deployment subvolume.
func DeploySubvolume(stream io.Reader, deployPath string, name string) error

// PruneDeployments removes old subvolumes, keeping current and next-boot.
func PruneDeployments(deployPath string, current string, nextBoot string) error
```

### 3. Unified config (`settings/settings.go`)

Merges ABRoot's `abroot.json` with frzr's `/frzr_root/source` file:

```json
{
    "incusSocket": "/var/lib/incus/unix.socket",
    "incusRemote": "oci-registry",
    "incusRegistryURL": "https://ghcr.io",

    "name": "myorg/myos",
    "tag": "stable",

    "deployMode": "ab",

    "partLabelA": "fmr-a",
    "partLabelB": "fmr-b",
    "partLabelVar": "fmr-var",
    "partLabelBoot": "fmr-boot",
    "partLabelEfi": "fmr-efi",

    "thinProvisioning": true,

    "iPkgMngAdd": "apt install -y",
    "iPkgMngRm": "apt remove -y",
    "iPkgMngStatus": 1,

    "updateInitramfsCmd": "/usr/sbin/update-initramfs -u",
    "updateGrubCmd": "/usr/sbin/grub-mkconfig -o '%s'"
}
```

`deployMode` can be `"ab"` (ABRoot A/B partition model) or `"btrfs"` (frzr subvolume model).

### 4. Removed dependencies

| Removed | Replaced by |
|---|---|
| `github.com/containers/buildah` | `incus publish` + `incus exec` via Go client |
| `go.podman.io/image/v5` | `incus.InstanceServer.GetImage()` |
| `go.podman.io/storage` | Incus storage pool API |
| `go.podman.io/common` | `github.com/lxc/incus/v6/shared/api` |
| `github.com/vanilla-os/prometheus` | `core/incus.go` (direct Incus client) |
| `github.com/docker/docker` (indirect) | removed |
| `github.com/fsouza/go-dockerclient` (indirect) | removed |
| frzr GitHub Releases API | Incus OCI remote / registry manifest API |

### 5. What stays identical

- The A/B partition transaction engine (`core/system.go`, `core/root.go`, `core/disk-manager.go`)
- The chroot helper (`core/chroot.go`)
- The GRUB/systemd-boot config writers
- The kernel argument manager
- The package queue (`core/packages.go`)
- The EtcBuilder overlayfs for `/etc`
- The Differ API integration for image diffs
- The cobra CLI structure

## Migration path for existing frzr systems

1. Install `frzr-meta-root` alongside frzr.
2. Run `frzr-meta-root bootstrap --mode=btrfs` — this re-uses the existing
   `frzr_root` btrfs volume and `frzr_efi` partition, only replacing the
   update mechanism.
3. The next `frzr-meta-root upgrade` pulls the image via Incus instead of
   the GitHub Releases API and deploys it as a btrfs subvolume exactly as
   frzr did.

## Migration path for existing ABRoot systems

1. Install `frzr-meta-root` alongside abroot.
2. Ensure Incus is installed and the OCI remote is configured:
   `incus remote add oci-registry https://ghcr.io --protocol=oci`
3. Copy `/etc/abroot/abroot.json` to `/etc/frzr-meta-root/frzr-meta-root.json`
   and add the `incusSocket` / `incusRemote` fields.
4. Run `frzr-meta-root upgrade` — the transaction engine is identical to ABRoot,
   only the image pull/build backend changes.

## Incus OCI support notes

Incus supports OCI registries as first-class remotes since v0.4:

```sh
# Add any OCI registry as a remote
incus remote add ghcr https://ghcr.io --protocol=oci

# Pull and inspect
incus image copy ghcr:myorg/myos:stable local: --alias myos-stable
incus image info myos-stable

# Export rootfs (equivalent to OciExportRootFs)
incus export myos-stable /tmp/myos-rootfs.tar.gz --instance-only

# Build a modified image (equivalent to buildah.BuildContainerFile)
incus launch local:myos-stable build-container
incus exec build-container -- apt install -y vim
incus stop build-container
incus publish build-container --alias myos-custom
incus delete build-container
```

All of the above are available through the Incus Go client API without
shelling out, making `core/incus.go` a clean, testable replacement for
the entire Podman/Buildah stack.
