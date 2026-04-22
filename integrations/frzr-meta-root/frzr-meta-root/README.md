# frzr-meta-root

A unified fork of [frzr](https://gitlab.com/openos-project/upstream-mirrors/frzr) and
[ABRoot v2](https://gitlab.com/openos-project/upstream-mirrors/ab-root) that uses
[Incus](https://gitlab.com/openos-project/incus_deving/incus) as the sole OCI backend,
replacing the Docker/Podman/Buildah stack entirely.

## What it does

- Provides full immutability and atomicity for Linux root filesystems
- Supports two deployment models selectable per-system:
  - **A/B partitions** — strongest atomicity, mirrors ABRoot v2
  - **btrfs subvolumes** — frzr-compatible, drop-in for existing frzr installs
- Pulls OS images from any OCI registry via Incus (no Docker daemon, no Podman)
- Applies local package changes by building modified images via `incus exec` + `incus publish`
- Manages kernel args, rollback, and boot entries

## Why Incus instead of Docker/Podman

ABRoot v2 depends on ~15 packages from the Podman/Buildah/Moby ecosystem
(`containers/buildah`, `go.podman.io/image`, `go.podman.io/storage`,
`vanilla-os/prometheus`, `docker/docker`, `fsouza/go-dockerclient`, etc.).

Incus already supports OCI registries natively:

```sh
incus remote add ghcr https://ghcr.io --protocol=oci
incus launch ghcr:myorg/myos:stable my-container
incus publish my-container --alias myos-custom
```

The Incus Go client (`github.com/lxc/incus/v6/client`) exposes all of this
as a clean API, replacing the entire Podman stack with a single dependency.

## Quick start

### Prerequisites

- Incus installed and running
- An OCI remote configured: `incus remote add oci-registry https://ghcr.io --protocol=oci`
- A UEFI system

### Install

```sh
go build -o /usr/bin/frzr-meta-root ./main.go
```

### Configure

Copy `config/frzr-meta-root.json` to `/etc/frzr-meta-root/frzr-meta-root.json`
and set at minimum:

```json
{
    "name": "myorg/myos",
    "tag": "stable",
    "deployMode": "ab"
}
```

### Bootstrap a new system

```sh
# A/B mode (default)
frzr-meta-root bootstrap --disk /dev/sda

# btrfs mode (frzr-compatible)
frzr-meta-root bootstrap --disk /dev/sda --mode btrfs
```

### Upgrade

```sh
frzr-meta-root upgrade          # pull latest image, stage for next boot
frzr-meta-root upgrade --check  # check only, do not apply
```

### Package management (A/B mode only)

```sh
frzr-meta-root pkg add vim htop
frzr-meta-root pkg remove nano
frzr-meta-root pkg apply        # build modified image and stage it
```

### Rollback

```sh
frzr-meta-root rollback         # revert to previous state on next boot
```

## Migration from frzr

1. Install `frzr-meta-root` and Incus.
2. Configure the OCI remote pointing at your image registry.
3. Set `deployMode: "btrfs"` in the config — this reuses your existing
   `frzr_root` btrfs volume and `frzr_efi` partition.
4. Run `frzr-meta-root upgrade`. The next update is pulled via Incus
   instead of the GitHub Releases API, and deployed as a btrfs subvolume
   exactly as frzr did.

## Migration from ABRoot v2

1. Install `frzr-meta-root` and Incus.
2. Add your OCI registry as an Incus remote.
3. Copy `/etc/abroot/abroot.json` to `/etc/frzr-meta-root/frzr-meta-root.json`
   and add `incusSocket`, `incusRemote`, `incusStoragePool`.
4. Run `frzr-meta-root upgrade`. The transaction engine is identical to
   ABRoot v2; only the image pull/build backend changes.

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for a detailed breakdown of the
component map, design decisions, and the Incus API mapping.

## License

GPLv3 — same as both upstream projects.
