#!/bin/bash
# SPDX-License-Identifier: GPL-2.0-or-later
#
# boot/install.sh - Install BDFS boot integration components
#
# Usage:
#   sudo bash boot/install.sh [--initramfs-tools | --dracut] [--dry-run]
#
# Detects the initramfs system automatically if not specified.

set -euo pipefail

DRY_RUN=0
INITRAMFS_SYSTEM=""

for arg in "$@"; do
    case "$arg" in
        --initramfs-tools) INITRAMFS_SYSTEM="initramfs-tools" ;;
        --dracut)          INITRAMFS_SYSTEM="dracut" ;;
        --dry-run)         DRY_RUN=1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

run() {
    if [[ "$DRY_RUN" -eq 1 ]]; then
        echo "[dry-run] $*"
    else
        "$@"
    fi
}

# ── Auto-detect initramfs system ──────────────────────────────────────────

if [[ -z "$INITRAMFS_SYSTEM" ]]; then
    if command -v update-initramfs &>/dev/null; then
        INITRAMFS_SYSTEM="initramfs-tools"
    elif command -v dracut &>/dev/null; then
        INITRAMFS_SYSTEM="dracut"
    else
        echo "error: cannot detect initramfs system (install initramfs-tools or dracut)"
        exit 1
    fi
fi

echo "Installing BDFS boot integration (system: $INITRAMFS_SYSTEM)"

# ── Install image-update script ───────────────────────────────────────────

run install -m755 "$SCRIPT_DIR/bdfs-image-update" /usr/local/sbin/bdfs-image-update

# ── Install systemd units ─────────────────────────────────────────────────

run install -m644 \
    "$SCRIPT_DIR/systemd-generator/bdfs-image-update.service" \
    /etc/systemd/system/bdfs-image-update.service

run install -m644 \
    "$SCRIPT_DIR/systemd-generator/bdfs-image-update.timer" \
    /etc/systemd/system/bdfs-image-update.timer

# ── Install boot configuration ────────────────────────────────────────────

run mkdir -p /etc/bdfs
if [[ ! -f /etc/bdfs/boot.conf ]]; then
    run install -m644 \
        "$SCRIPT_DIR/../configs/boot.conf" \
        /etc/bdfs/boot.conf
    echo "Installed /etc/bdfs/boot.conf — edit before rebooting"
else
    echo "Skipping /etc/bdfs/boot.conf (already exists)"
fi

# ── Install initramfs hook ────────────────────────────────────────────────

if [[ "$INITRAMFS_SYSTEM" == "initramfs-tools" ]]; then
    run install -m755 \
        "$SCRIPT_DIR/initramfs/hooks/bdfs" \
        /etc/initramfs-tools/hooks/bdfs
    run install -m755 \
        "$SCRIPT_DIR/initramfs/scripts/bdfs-root" \
        /etc/initramfs-tools/scripts/local-premount/bdfs-root
    echo "Installed initramfs-tools hook and script"

elif [[ "$INITRAMFS_SYSTEM" == "dracut" ]]; then
    run mkdir -p /usr/lib/dracut/modules.d/90bdfs
    run install -m755 \
        "$SCRIPT_DIR/dracut/module-setup.sh" \
        /usr/lib/dracut/modules.d/90bdfs/module-setup.sh
    run install -m755 \
        "$SCRIPT_DIR/dracut/bdfs-root.sh" \
        /usr/lib/dracut/modules.d/90bdfs/bdfs-root.sh
    echo "Installed dracut module 90bdfs"
fi

# ── Rebuild initramfs ─────────────────────────────────────────────────────

if [[ "$DRY_RUN" -eq 0 ]]; then
    echo ""
    echo "Rebuilding initramfs..."
    if [[ "$INITRAMFS_SYSTEM" == "initramfs-tools" ]]; then
        update-initramfs -u -k all
    else
        dracut --force --add bdfs
    fi
    echo "Initramfs rebuilt."
fi

# ── Enable systemd timer ──────────────────────────────────────────────────

if [[ "$DRY_RUN" -eq 0 ]]; then
    systemctl daemon-reload
    systemctl enable bdfs-image-update.timer
    echo "Enabled bdfs-image-update.timer"
fi

echo ""
echo "Installation complete."
echo ""
echo "Next steps:"
echo "  1. Edit /etc/bdfs/boot.conf"
echo "  2. Add to GRUB_CMDLINE_LINUX in /etc/default/grub:"
echo "       bdfs.root=/dev/sdXN bdfs.image=/images/system.dwarfs"
echo "       bdfs.upper=/dev/sdYN"
echo "  3. Run: sudo update-grub"
echo "  4. Reboot"
echo ""
echo "To roll back after a bad update, add 'bdfs.rollback' to the kernel"
echo "cmdline at boot (e.g. via GRUB edit mode)."
