# BDFS Immutable Boot Integration

## Overview

The boot integration layer mounts a DwarFS image as the immutable system root
with a BTRFS writable overlay, enabling:

- **Instant rollback** — boot the previous image with `bdfs.rollback` on the
  kernel cmdline
- **Atomic updates** — new images are verified with `dwarfsck` before being
  rotated into place via `rename(2)`
- **Persistent or ephemeral writes** — use a BTRFS upper layer for persistent
  changes, or omit it for a live/kiosk mode where changes are lost on reboot
- **BTRFS snapshots of image collections** — every image rotation creates a
  BTRFS snapshot of the images subvolume

## Boot Flow

```
GRUB
  │  bdfs.root=/dev/sda2
  │  bdfs.image=/images/system.dwarfs
  │  bdfs.upper=/dev/sda3
  ▼
initramfs / dracut pre-mount hook (bdfs-root)
  │
  ├─ mount /dev/sda2 (BTRFS) → /run/bdfs/btrfs_part  [read-only]
  │
  ├─ dwarfs /run/bdfs/btrfs_part/images/system.dwarfs
  │         → /run/bdfs/lower                         [read-only FUSE]
  │
  ├─ mount /dev/sda3 (BTRFS) → /run/bdfs/upper        [read-write]
  │   └─ btrfs subvolume create upper  (if absent)
  │
  ├─ mount overlayfs → /root (or $NEWROOT for dracut)
  │     lower = /run/bdfs/lower   (DwarFS)
  │     upper = /run/bdfs/upper/upper  (BTRFS subvol)
  │     work  = /run/bdfs/upper/.bdfs_work
  │
  └─ bind-mount /run/bdfs/btrfs_part → /root/run/bdfs/btrfs_part
     (so the running system can manage images)

systemd / init
  └─ normal boot from /root
```

## Disk Layout

```
/dev/sda1   EFI / boot partition
/dev/sda2   BTRFS — image store (read-only at boot)
  └── /images/
        ├── system.dwarfs        ← active root image
        ├── system.prev.dwarfs   ← previous image (rollback target)
        └── system.new.dwarfs    ← staging area during update
/dev/sda3   BTRFS — writable upper layer
  └── /upper/                    ← overlayfs upper dir (persistent changes)
      /.bdfs_work/               ← overlayfs work dir
```

## Kernel Parameters

| Parameter | Description | Example |
|---|---|---|
| `bdfs.root=` | BTRFS partition holding images | `/dev/sda2` |
| `bdfs.image=` | Path to image within partition | `/images/system.dwarfs` |
| `bdfs.upper=` | BTRFS partition for writable layer | `/dev/sda3` |
| `bdfs.upper.subvol=` | Subvolume name (default: `upper`) | `upper` |
| `bdfs.rollback` | Boot `.prev` image instead | _(flag)_ |
| `bdfs.shell` | Drop to shell before mounting | _(flag, debug)_ |

## Installation

```bash
# Detect initramfs system automatically
sudo bash boot/install.sh

# Or specify explicitly
sudo bash boot/install.sh --initramfs-tools
sudo bash boot/install.sh --dracut

# Dry run (show what would be done)
sudo bash boot/install.sh --dry-run
```

Then edit `/etc/default/grub`:

```
GRUB_CMDLINE_LINUX="... bdfs.root=/dev/sda2 bdfs.image=/images/system.dwarfs bdfs.upper=/dev/sda3"
```

And rebuild GRUB:

```bash
sudo update-grub   # Debian/Ubuntu
sudo grub2-mkconfig -o /boot/grub2/grub.cfg  # Fedora/RHEL
```

## Creating the Initial Root Image

```bash
# 1. Install a base system into a directory
debootstrap stable /tmp/rootfs http://deb.debian.org/debian

# 2. Create a DwarFS image from it
mkdwarfs -i /tmp/rootfs -o /mnt/images/system.dwarfs \
    --compression zstd --block-size-bits 22 \
    --categorize --num-workers 4

# 3. Verify the image
dwarfsck /mnt/images/system.dwarfs

# 4. Register with the framework
bdfs partition add --type btrfs-backed \
    --device /dev/sda2 --label images --mount /mnt/images

bdfs verify --partition <uuid>
```

## Image Updates

The `bdfs-image-update` script manages the image lifecycle:

```bash
# Manual update from a local file
sudo bdfs-image-update --image /path/to/new_system.dwarfs

# Dry run (verify only, no rotation)
sudo bdfs-image-update --image /path/to/new_system.dwarfs --dry-run

# Force update even if hash matches
sudo bdfs-image-update --image /path/to/new_system.dwarfs --force
```

Automatic updates run via the systemd timer (daily by default):

```bash
systemctl enable --now bdfs-image-update.timer
systemctl status bdfs-image-update.timer
```

Configure the update URL in `/etc/bdfs/boot.conf`:

```ini
update_url = https://example.com/images/system.dwarfs
update_checksum = <sha256>
```

## Rollback

To roll back to the previous image, add `bdfs.rollback` to the kernel cmdline
at boot (via GRUB edit mode, `e` key):

```
linux /vmlinuz ... bdfs.root=/dev/sda2 bdfs.image=/images/system.dwarfs bdfs.rollback
```

Or permanently via `/etc/default/grub` until the issue is resolved.

The `.prev` image is retained until the next successful update rotation.

## Live / Kiosk Mode

Omit `bdfs.upper=` to use a tmpfs upper layer. All writes are lost on reboot:

```
GRUB_CMDLINE_LINUX="... bdfs.root=/dev/sda2 bdfs.image=/images/system.dwarfs"
```

This is useful for kiosk systems, CI runners, or read-only appliances.

## Comparison with Similar Systems

| Feature | BDFS | OSTree | NixOS | ChromeOS |
|---|---|---|---|---|
| Immutable base | DwarFS image | OSTree repo | Nix store | dm-verity |
| Writable layer | BTRFS overlay | bind mounts | tmpfs/persist | ext4 stateful |
| Rollback | `.prev` image | `ostree admin rollback` | `nixos-rebuild` | Powerwash |
| Compression | 10–16× (DwarFS) | none | store-level | squashfs |
| Snapshots | BTRFS CoW | OSTree commits | Nix generations | none |
| Update atomicity | `rename(2)` | hardlink tree | Nix profile | partition swap |
