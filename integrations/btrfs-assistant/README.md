# Btrfs Assistant

## Overview
Btrfs Assistant is a GUI management tool to make managing a Btrfs filesystem easier.  The primary features it offers are:
* An easy to read overview of Btrfs metadata
* A simple view of subvolumes with or without Snapper/Timeshift snapshots
* Run and monitor scrub and balance operations
* A pushbutton method for removing subvolumes
* A management front-end for Snapper with enhanced restore functionality
	* View, create and delete snapshots
	* Restore snapshots in a variety of situations
	  * When the filesystem is mounted in a different distro
	  * When booted off a snapshot
	  * From a live ISO
	* View, create, edit, remove Snapper configurations
	* Browse snapshots and restore individual files
	* Browse diffs of a single file across snapshot versions
	* Manage Snapper systemd units
* A front-end for Btrfs Maintenance
	* Manage systemd units
	* Easily manage configuration for defrag, balance and scrub settings
* A **BDFS tab** for [btrfs-dwarfs-framework](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btrfs-dwarfs-framework) integration
	* View BTRFS partitions, DwarFS images, and active blend mounts
	* Mount and unmount blend layers (kernel or fuse-overlayfs userspace mode)
	* Demote snapshots to compressed DwarFS images
	* Import DwarFS images back as BTRFS subvolumes
	* Prune snapshots with keep-N policy, optional demote-before-delete, and dry-run mode

## BDFS Tab

The BDFS tab communicates with the [bdfs daemon](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btrfs-dwarfs-framework) over its Unix socket (`/run/bdfs/bdfs.sock`). It disables itself gracefully when the daemon is not running, so the rest of the application is unaffected on systems without bdfs.

### Subtabs

| Subtab | Function |
|---|---|
| **Overview** | Live lists of BTRFS partitions, DwarFS images, and active blend mounts |
| **Blend Mount** | Mount a DwarFS image over a BTRFS subvolume; toggle kernel vs. fuse-overlayfs mode |
| **Demote** | Compress a read-only snapshot to a `.dwarfs` archive |
| **Import** | Restore a `.dwarfs` archive as a writable BTRFS subvolume |
| **Prune** | Delete old snapshots with keep-N, name pattern filter, demote-first, and dry-run options |

### Requirements

- `bdfs` daemon running (`systemctl start bdfs_daemon`)
- Socket accessible at `/run/bdfs/bdfs.sock` (configurable in `bdfs.conf`)

### Screenshots
![image](/uploads/21da59577c3e8a101347cf0d59569c09/image.png)

![image](/uploads/41aa431b6a0de85bc70b84d90da392ea/image.png)

![image](/uploads/65b6004c3257d66154828259a0fed47d/image.png)

![image](/uploads/d255a9d9839ba8633b8e911858f4b48f/image.png)

![image](/uploads/429be74e9fb92088697944d23a1def1d/image.png)

![image](/uploads/ea3940775576a3a0ef7f205b8f2fd77a/image.png)

## Installing

It is packaged as `btrfs-assistant` in Arch, Debian, Fedora, Ubuntu and many other distros.  You can see the packaging status below.

## Packaging Status
[![Packaging status](https://repology.org/badge/vertical-allrepos/btrfs-assistant.svg)](https://repology.org/project/btrfs-assistant/versions)

## Contributing
Contributions are welcome!

Please see [CONTRIBUTING.md](docs/CONTRIBUTING.md) for more details.

### Development Requirements
* Qt6 / Qt Design UI (including Qt6Network for the bdfs socket client)
* C++17
* Cmake >= 3.5
* Root user privileges
* Btrfs filesystem
* [btrfs-dwarfs-framework](https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btrfs-dwarfs-framework) daemon (optional — BDFS tab is disabled when absent)
