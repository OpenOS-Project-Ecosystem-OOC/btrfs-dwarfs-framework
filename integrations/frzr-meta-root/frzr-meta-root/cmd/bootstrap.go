package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		`frzr-meta-root bootstrap` — disk setup and initial deployment.

		Merges frzr-bootstrap (interactive disk partitioning + btrfs setup)
		with ABRoot's partition scheme. Supports both deploy modes.

		In "ab" mode: creates the A/B + var + boot + EFI partition layout.
		In "btrfs" mode: creates the frzr-compatible btrfs + EFI layout.

		The initial image is pulled via Incus in both cases.
*/

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/core"
	"github.com/openos-project/frzr-meta-root/settings"
)

var (
	bootstrapDisk     string
	bootstrapMode     string
	bootstrapUsername string
	bootstrapRepair   bool
)

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Partition a disk and perform the initial system deployment",
	Long: `Partitions the target disk and deploys the initial system image.

Modes:
  ab     — A/B + var + boot + EFI layout (default)
  btrfs  — btrfs root + EFI layout (frzr-compatible, for existing frzr installs)

The image is pulled from the configured OCI registry via Incus.
Requires root privileges and a UEFI system.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Geteuid() != 0 {
			return fmt.Errorf("bootstrap must be run as root")
		}

		if bootstrapDisk == "" {
			return fmt.Errorf("--disk is required")
		}

		cfg := settings.Cnf
		mode := bootstrapMode
		if mode == "" {
			mode = cfg.DeployMode
		}

		client, err := core.NewIncusClient(cfg.IncusSocket, cfg.IncusRemote, cfg.IncusStoragePool)
		if err != nil {
			return fmt.Errorf("connecting to Incus: %w", err)
		}

		switch mode {
		case "ab":
			return bootstrapAB(client, cfg, bootstrapDisk)
		case "btrfs":
			return bootstrapBtrfs(client, cfg, bootstrapDisk, bootstrapUsername, bootstrapRepair)
		default:
			return fmt.Errorf("unknown mode %q; use \"ab\" or \"btrfs\"", mode)
		}
	},
}

func init() {
	bootstrapCmd.Flags().StringVar(&bootstrapDisk, "disk", "", "Target block device, e.g. /dev/sda (required)")
	bootstrapCmd.Flags().StringVar(&bootstrapMode, "mode", "", "Deploy mode: ab or btrfs (default: from config)")
	bootstrapCmd.Flags().StringVar(&bootstrapUsername, "user", "user", "Initial user home directory name (btrfs mode)")
	bootstrapCmd.Flags().BoolVar(&bootstrapRepair, "repair", false, "Repair existing installation instead of clean install (btrfs mode)")
	_ = bootstrapCmd.MarkFlagRequired("disk")
}

// bootstrapAB creates the A/B partition layout and deploys the initial image.
// Partition scheme:
//
//	EFI  (512 MiB, FAT32, label fmr-efi)
//	boot (512 MiB, ext4,  label fmr-boot)
//	A    (LVM or raw, label fmr-a)
//	B    (LVM or raw, label fmr-b)
//	var  (LVM or raw, label fmr-var)
func bootstrapAB(client *core.IncusClient, cfg *settings.Config, disk string) error {
	fmt.Printf("Bootstrapping A/B layout on %s...\n", disk)

	// Partition the disk.
	if err := runCmd("parted", "--script", disk,
		"mklabel", "gpt",
		"mkpart", "primary", "fat32", "1MiB", "512MiB",
		"set", "1", "esp", "on",
		"mkpart", "primary", "512MiB", "1024MiB",
		"mkpart", "primary", "1024MiB", "40%",
		"mkpart", "primary", "40%", "70%",
		"mkpart", "primary", "70%", "100%",
	); err != nil {
		return fmt.Errorf("partitioning disk: %w", err)
	}

	// Format partitions.
	parts := diskParts(disk)
	if err := runCmd("mkfs.vfat", "-n", cfg.PartLabelEfi, parts[0]); err != nil {
		return err
	}
	if err := runCmd("mkfs.ext4", "-L", cfg.PartLabelBoot, parts[1]); err != nil {
		return err
	}
	if err := runCmd("mkfs.ext4", "-L", cfg.PartLabelA, parts[2]); err != nil {
		return err
	}
	if err := runCmd("mkfs.ext4", "-L", cfg.PartLabelB, parts[3]); err != nil {
		return err
	}
	if err := runCmd("mkfs.ext4", "-L", cfg.PartLabelVar, parts[4]); err != nil {
		return err
	}

	// Install bootloader.
	efiMount := "/tmp/fmr-efi"
	_ = os.MkdirAll(efiMount, 0o755)
	if err := runCmd("mount", parts[0], efiMount); err != nil {
		return err
	}
	defer runCmd("umount", efiMount) //nolint:errcheck

	if err := runCmd("bootctl", "--esp-path="+efiMount, "install"); err != nil {
		return err
	}

	// Deploy initial image to partition A.
	aMount := "/tmp/fmr-a"
	_ = os.MkdirAll(aMount, 0o755)
	if err := runCmd("mount", parts[2], aMount); err != nil {
		return err
	}
	defer runCmd("umount", aMount) //nolint:errcheck

	if err := client.ExportRootFs(cfg.Name, cfg.Tag, nil, "/tmp/fmr-trans", aMount); err != nil {
		return fmt.Errorf("deploying initial image to A partition: %w", err)
	}

	fmt.Println("Bootstrap complete. Reboot to start the system.")
	return nil
}

// bootstrapBtrfs creates the frzr-compatible btrfs layout and deploys the
// initial image. Mirrors frzr-bootstrap's partition and subvolume setup,
// replacing the GitHub Releases download with an Incus image pull.
func bootstrapBtrfs(client *core.IncusClient, cfg *settings.Config, disk, username string, repair bool) error {
	mountPath := "/tmp/frzr_root"

	if repair {
		fmt.Println("Repair install: preserving user data, reinstalling bootloader and clearing deployments...")
		return repairBtrfs(disk, mountPath)
	}

	fmt.Printf("Bootstrapping btrfs layout on %s...\n", disk)

	if err := runCmd("parted", "--script", disk,
		"mklabel", "gpt",
		"mkpart", "primary", "fat32", "1MiB", "512MiB",
		"set", "1", "esp", "on",
		"mkpart", "primary", "512MiB", "100%",
	); err != nil {
		return fmt.Errorf("partitioning disk: %w", err)
	}

	parts := diskParts(disk)

	if err := runCmd("mkfs.btrfs", "-L", cfg.BtrfsMountLabel, "-f", parts[1]); err != nil {
		return err
	}
	_ = os.MkdirAll(mountPath, 0o755)
	if err := runCmd("mount", "-t", "btrfs", "-o", "nodatacow", parts[1], mountPath); err != nil {
		return err
	}
	defer runCmd("umount", mountPath) //nolint:errcheck

	// Create persistence subvolumes (mirrors frzr-bootstrap).
	for _, sub := range []string{"var", "home"} {
		if err := runCmd("btrfs", "subvolume", "create", mountPath+"/"+sub); err != nil {
			return err
		}
	}

	_ = os.MkdirAll(mountPath+"/home/"+username, 0o755)
	_ = os.Chown(mountPath+"/home/"+username, 1000, 1000)
	_ = os.MkdirAll(mountPath+"/boot", 0o755)
	_ = os.MkdirAll(mountPath+"/etc", 0o755)
	_ = os.MkdirAll(mountPath+"/.etc", 0o755)
	_ = os.MkdirAll(mountPath+"/deployments", 0o755)

	// EFI partition.
	if err := runCmd("mkfs.vfat", parts[0]); err != nil {
		return err
	}
	if err := runCmd("dosfslabel", parts[0], cfg.BtrfsEfiLabel); err != nil {
		return err
	}
	if err := runCmd("mount", "-t", "vfat", parts[0], mountPath+"/boot"); err != nil {
		return err
	}
	defer runCmd("umount", mountPath+"/boot") //nolint:errcheck

	if err := runCmd("bootctl", "--esp-path="+mountPath+"/boot", "install"); err != nil {
		return err
	}

	// Pull and deploy the initial image via Incus.
	transDir := "/tmp/fmr-btrfs-trans"
	rootfsDir := transDir + "/rootfs"
	if err := client.ExportRootFs(cfg.Name, cfg.Tag, nil, transDir, rootfsDir); err != nil {
		return fmt.Errorf("pulling initial image: %w", err)
	}

	fingerprint, err := client.PullImage(cfg.Name, cfg.Tag)
	if err != nil {
		return err
	}
	name := cfg.Name + "-" + cfg.Tag + "-" + fingerprint[:12]

	if err := core.DeployFromRootfs(rootfsDir, mountPath+"/deployments", name); err != nil {
		return err
	}
	if err := core.CopyKernelFiles(mountPath+"/deployments", mountPath+"/boot", name); err != nil {
		return err
	}
	if err := core.WriteBootEntry(mountPath+"/deployments", name, ""); err != nil {
		return err
	}

	// Write source file (mirrors frzr's /frzr_root/source).
	_ = os.WriteFile(mountPath+"/source", []byte(cfg.Name+":"+cfg.Tag+"\n"), 0o644)

	fmt.Println("Bootstrap complete. Reboot to start the system.")
	return nil
}

func repairBtrfs(disk, mountPath string) error {
	parts := diskParts(disk)
	_ = os.MkdirAll(mountPath, 0o755)
	if err := runCmd("mount", parts[1], mountPath); err != nil {
		return err
	}
	defer runCmd("umount", mountPath) //nolint:errcheck

	if err := runCmd("mount", "-t", "vfat", parts[0], mountPath+"/boot"); err != nil {
		return err
	}
	defer runCmd("umount", mountPath+"/boot") //nolint:errcheck

	// Clear boot files and reinstall bootloader.
	_ = os.RemoveAll(mountPath + "/boot")
	_ = os.MkdirAll(mountPath+"/boot", 0o755)
	if err := runCmd("bootctl", "--esp-path="+mountPath+"/boot", "install"); err != nil {
		return err
	}

	// Remove all deployments so the next upgrade starts fresh.
	entries, _ := os.ReadDir(mountPath + "/deployments")
	for _, e := range entries {
		_ = runCmd("btrfs", "subvolume", "delete", mountPath+"/deployments/"+e.Name())
	}

	_ = os.RemoveAll(mountPath + "/etc")
	return nil
}

// diskParts returns the partition device paths for a given disk.
// Handles both /dev/sdX (→ /dev/sdX1, /dev/sdX2, ...) and
// /dev/nvmeXnY (→ /dev/nvmeXnYp1, /dev/nvmeXnYp2, ...).
func diskParts(disk string) []string {
	sep := ""
	if len(disk) > 0 && (disk[len(disk)-1] >= '0' && disk[len(disk)-1] <= '9') {
		sep = "p"
	}
	return []string{
		disk + sep + "1",
		disk + sep + "2",
		disk + sep + "3",
		disk + sep + "4",
		disk + sep + "5",
	}
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
