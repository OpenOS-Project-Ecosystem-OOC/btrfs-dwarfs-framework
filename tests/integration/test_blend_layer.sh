#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# test_blend_layer.sh - Blend layer (unified namespace) tests
#
# Tests the promote/demote cycle and unified namespace behaviour:
#   - Kernel blend mount (bdfs_blend module)
#   - Userspace blend mount (fuse-overlayfs fallback)
#   - Auto-fallback from kernel to userspace on ENODEV
#   - Teardown order: overlay unmounted before DwarFS lower layer
#
# Run as root against a loopback BTRFS device.
# Requires: btrfs-progs, dwarfs, fuse-overlayfs, bdfs CLI

set -euo pipefail
source "$(dirname "$0")/lib.sh"

# ── Fixtures ─────────────────────────────────────────────────────────────────

setup_blend_fixtures() {
	# Create a BTRFS loopback device
	BTRFS_IMG="$TEST_TMP/btrfs.img"
	BTRFS_MNT="$TEST_TMP/btrfs"
	truncate -s 2G "$BTRFS_IMG"
	mkfs.btrfs -q "$BTRFS_IMG"
	mkdir -p "$BTRFS_MNT"
	mount -o loop "$BTRFS_IMG" "$BTRFS_MNT"

	# Create a source subvolume with known content
	btrfs subvolume create "$BTRFS_MNT/source"
	echo "hello from btrfs" > "$BTRFS_MNT/source/btrfs_file.txt"
	mkdir -p "$BTRFS_MNT/source/subdir"
	echo "nested" > "$BTRFS_MNT/source/subdir/nested.txt"

	# Create a read-only snapshot for export
	btrfs subvolume snapshot -r \
		"$BTRFS_MNT/source" "$BTRFS_MNT/source_snap"

	# Export the snapshot to a DwarFS image
	DWARFS_IMG="$TEST_TMP/test.dwarfs"
	mkdwarfs -i "$BTRFS_MNT/source_snap" -o "$DWARFS_IMG" \
		--compression zstd --num-workers 2 2>/dev/null

	# Create the BTRFS upper subvolume (writable layer)
	btrfs subvolume create "$BTRFS_MNT/upper"

	# Create the overlayfs workdir on the same BTRFS filesystem
	btrfs subvolume create "$BTRFS_MNT/workdir"

	# Mountpoints
	BLEND_MNT="$TEST_TMP/blend"
	mkdir -p "$BLEND_MNT"

	# Private DwarFS lower mountpoint (mirrors daemon state_dir behaviour)
	LOWER_MNT="$TEST_TMP/lower_fuse"
	mkdir -p "$LOWER_MNT"
}

teardown_blend_fixtures() {
	# Unmount in reverse order; ignore errors (already unmounted)
	umount "$BLEND_MNT"  2>/dev/null || true
	umount "$LOWER_MNT"  2>/dev/null || true
	umount "$BTRFS_MNT"  2>/dev/null || true
}

# ── Helpers ───────────────────────────────────────────────────────────────────

# Mount the DwarFS image at LOWER_MNT using the dwarfs FUSE driver
mount_dwarfs_lower() {
	dwarfs "$DWARFS_IMG" "$LOWER_MNT" -o cache_size=64m
	# Wait for FUSE mount to be ready
	local retries=10
	while ! mountpoint -q "$LOWER_MNT" && (( retries-- > 0 )); do
		sleep 0.1
	done
	mountpoint -q "$LOWER_MNT" || fail "dwarfs FUSE mount did not appear"
}

# Mount fuse-overlayfs over LOWER_MNT + BTRFS_MNT/upper → BLEND_MNT
mount_userspace_blend() {
	fuse-overlayfs \
		-o lowerdir="$LOWER_MNT",upperdir="$BTRFS_MNT/upper",workdir="$BTRFS_MNT/workdir" \
		"$BLEND_MNT"
	local retries=10
	while ! mountpoint -q "$BLEND_MNT" && (( retries-- > 0 )); do
		sleep 0.1
	done
	mountpoint -q "$BLEND_MNT" || fail "fuse-overlayfs mount did not appear"
}

umount_userspace_blend() {
	fusermount -u "$BLEND_MNT"  2>/dev/null || umount "$BLEND_MNT"  2>/dev/null || true
	fusermount -u "$LOWER_MNT"  2>/dev/null || umount "$LOWER_MNT"  2>/dev/null || true
}

# ── Tests ─────────────────────────────────────────────────────────────────────

test_userspace_blend_mount() {
	desc "userspace blend mount: fuse-overlayfs over dwarfs FUSE"

	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs

	setup_blend_fixtures

	mount_dwarfs_lower
	mount_userspace_blend

	# DwarFS lower content is visible through the blend
	assert_file_contains "$BLEND_MNT/btrfs_file.txt" "hello from btrfs"
	assert_file_contains "$BLEND_MNT/subdir/nested.txt" "nested"

	pass "DwarFS lower content visible through fuse-overlayfs blend"
}

test_userspace_blend_write_goes_to_upper() {
	desc "userspace blend mount: writes land on BTRFS upper layer"

	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs

	# Write a new file through the blend
	echo "written via blend" > "$BLEND_MNT/new_file.txt"

	# File must appear on the BTRFS upper layer
	assert_file_exists "$BTRFS_MNT/upper/new_file.txt"
	assert_file_contains "$BTRFS_MNT/upper/new_file.txt" "written via blend"

	# DwarFS lower must be unmodified (read-only)
	assert_file_not_exists "$LOWER_MNT/new_file.txt"

	pass "writes go to BTRFS upper, DwarFS lower unchanged"
}

test_userspace_blend_modify_lower_file() {
	desc "userspace blend mount: modifying a lower file copies it up to BTRFS"

	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs

	# Modify a file that exists only in the DwarFS lower
	echo "modified" >> "$BLEND_MNT/btrfs_file.txt"

	# The modified copy must be on the BTRFS upper layer
	assert_file_exists "$BTRFS_MNT/upper/btrfs_file.txt"
	assert_file_contains "$BTRFS_MNT/upper/btrfs_file.txt" "modified"

	# The DwarFS lower must still have the original content
	assert_file_contains "$LOWER_MNT/btrfs_file.txt" "hello from btrfs"

	pass "copy-up on modify: modified file on BTRFS upper, original on DwarFS lower"
}

test_userspace_blend_delete_lower_file() {
	desc "userspace blend mount: deleting a lower file creates a whiteout on BTRFS"

	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs

	rm "$BLEND_MNT/btrfs_file.txt"

	# File must not be visible through the blend
	assert_file_not_exists "$BLEND_MNT/btrfs_file.txt"

	# DwarFS lower must still have the original (whiteout is on upper)
	assert_file_exists "$LOWER_MNT/btrfs_file.txt"

	pass "delete creates whiteout on BTRFS upper, DwarFS lower unchanged"
}

test_userspace_blend_umount_order() {
	desc "userspace blend umount: overlay unmounted before DwarFS lower"

	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs

	# Unmount overlay first
	fusermount -u "$BLEND_MNT" || umount "$BLEND_MNT"
	assert_not_mountpoint "$BLEND_MNT"

	# DwarFS lower must still be mounted at this point
	assert_mountpoint "$LOWER_MNT"

	# Now unmount the lower
	fusermount -u "$LOWER_MNT" || umount "$LOWER_MNT"
	assert_not_mountpoint "$LOWER_MNT"

	pass "teardown order correct: overlay before DwarFS lower"
}

test_userspace_blend_umount_lower_first_fails() {
	desc "userspace blend umount: unmounting DwarFS lower while overlay is active fails"

	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs

	# Attempting to unmount the lower while the overlay is still mounted
	# must fail (EBUSY)
	if fusermount -u "$LOWER_MNT" 2>/dev/null || umount "$LOWER_MNT" 2>/dev/null; then
		fail "expected umount of lower to fail while overlay is active"
	fi

	assert_mountpoint "$LOWER_MNT"
	pass "umount of lower correctly refused while overlay is active"
}

test_userspace_blend_teardown() {
	# Final cleanup for the userspace blend test group
	umount_userspace_blend
	teardown_blend_fixtures
}

# ── Kernel blend mount tests (require bdfs_blend module) ─────────────────────

test_kernel_blend_mount() {
	desc "kernel blend mount: bdfs_blend module"

	skip_if_no_cmd bdfs
	skip_unless_module_loaded bdfs_blend

	setup_blend_fixtures

	bdfs blend mount \
		--btrfs-uuid "$(btrfs filesystem show "$BTRFS_MNT" | awk '/uuid:/{print $NF}')" \
		--dwarfs-uuid "00000000-0000-0000-0000-000000000001" \
		--mountpoint "$BLEND_MNT"

	assert_mountpoint "$BLEND_MNT"
	assert_file_contains "$BLEND_MNT/btrfs_file.txt" "hello from btrfs"

	bdfs blend umount --mountpoint "$BLEND_MNT"
	assert_not_mountpoint "$BLEND_MNT"

	teardown_blend_fixtures
	pass "kernel blend mount/umount cycle"
}

test_kernel_blend_auto_fallback() {
	desc "kernel blend mount: auto-fallback to fuse-overlayfs on ENODEV"

	skip_if_no_cmd bdfs
	skip_if_no_cmd fuse-overlayfs
	skip_if_no_cmd dwarfs
	skip_if_module_loaded bdfs_blend  # only meaningful when module is absent

	setup_blend_fixtures

	# bdfs blend mount without --userspace: should detect ENODEV and
	# automatically retry with fuse-overlayfs
	bdfs blend mount \
		--btrfs-uuid "$(btrfs filesystem show "$BTRFS_MNT" | awk '/uuid:/{print $NF}')" \
		--dwarfs-uuid "00000000-0000-0000-0000-000000000001" \
		--mountpoint "$BLEND_MNT" \
		--btrfs-mount "$BTRFS_MNT/upper" \
		--dwarfs-image "$DWARFS_IMG" \
		--work-dir "$BTRFS_MNT/workdir"

	assert_mountpoint "$BLEND_MNT"
	assert_file_contains "$BLEND_MNT/btrfs_file.txt" "hello from btrfs"

	bdfs blend umount --mountpoint "$BLEND_MNT"
	assert_not_mountpoint "$BLEND_MNT"

	teardown_blend_fixtures
	pass "auto-fallback to fuse-overlayfs when kernel module absent"
}

# ── Test runner ───────────────────────────────────────────────────────────────

run_tests \
	test_userspace_blend_mount \
	test_userspace_blend_write_goes_to_upper \
	test_userspace_blend_modify_lower_file \
	test_userspace_blend_delete_lower_file \
	test_userspace_blend_umount_order \
	test_userspace_blend_umount_lower_first_fails \
	test_userspace_blend_teardown \
	test_kernel_blend_mount \
	test_kernel_blend_auto_fallback
