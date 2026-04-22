package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		`frzr-meta-root upgrade` — unified upgrade command.

		In "ab" mode: mirrors `abroot upgrade`, pulling the new OCI image
		via Incus and transacting to the future A/B partition.

		In "btrfs" mode: mirrors `frzr-deploy`, pulling the new OCI image
		via Incus and deploying it as a new btrfs subvolume.

		Both paths use IncusClient (core/incus.go) instead of the
		Podman/Buildah stack or frzr's GitHub Releases + curl download.
*/

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/core"
	"github.com/openos-project/frzr-meta-root/settings"
)

var (
	upgradeCheckOnly bool
	upgradeDryRun    bool
	upgradeForce     bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Pull the latest image and apply it on next boot",
	Long: `Checks for an updated OCI image on the configured registry (via Incus).
If an update is available, pulls it and stages it for the next boot.

In "ab" mode the update is applied to the future partition.
In "btrfs" mode a new subvolume is created and the boot entry is updated.

The running system is never modified — changes take effect after reboot.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := settings.Cnf

		client, err := core.NewIncusClient(cfg.IncusSocket, cfg.IncusRemote, cfg.IncusStoragePool)
		if err != nil {
			return fmt.Errorf("connecting to Incus: %w", err)
		}

		switch cfg.DeployMode {
		case "ab":
			return runABUpgrade(client, cfg, upgradeCheckOnly, upgradeDryRun, upgradeForce)
		case "btrfs":
			return runBtrfsUpgrade(client, cfg, upgradeCheckOnly)
		default:
			return fmt.Errorf("unknown deployMode %q", cfg.DeployMode)
		}
	},
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheckOnly, "check", false, "Only check for updates, do not apply")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "Simulate the upgrade without writing to disk")
	upgradeCmd.Flags().BoolVar(&upgradeForce, "force", false, "Force upgrade even if no update is detected")
}

// runABUpgrade performs an A/B partition upgrade using the Incus backend.
// This replaces `abroot upgrade` / ABSystem.RunOperation(UPGRADE).
func runABUpgrade(client *core.IncusClient, cfg *settings.Config, checkOnly, dryRun, force bool) error {
	sys, err := core.NewABSystem(client)
	if err != nil {
		return err
	}

	if checkOnly {
		_, hasUpdate, err := sys.CheckUpdate()
		if err != nil {
			return err
		}
		if hasUpdate {
			fmt.Println("Update available.")
		} else {
			fmt.Println("System is up to date.")
		}
		return nil
	}

	op := core.ABSystemOperation(core.UPGRADE)
	if force {
		op = core.FORCE_UPGRADE
	}

	return sys.RunOperation(op, dryRun)
}

// runBtrfsUpgrade performs a btrfs subvolume upgrade using the Incus backend.
// This replaces frzr's __frzr-deploy download + btrfs receive flow.
func runBtrfsUpgrade(client *core.IncusClient, cfg *settings.Config, checkOnly bool) error {
	curImage, err := core.NewABImageFromRoot()
	if err != nil {
		return fmt.Errorf("reading current image state: %w", err)
	}

	newDigest, hasUpdate, err := client.HasUpdate(cfg.Name, cfg.Tag, curImage.Digest)
	if err != nil {
		return err
	}

	if !hasUpdate {
		fmt.Println("System is up to date.")
		return nil
	}

	if checkOnly {
		fmt.Println("Update available:", newDigest)
		return nil
	}

	// Pull the new image via Incus (replaces curl download from GitHub Releases).
	fingerprint, err := client.PullImage(cfg.Name, cfg.Tag)
	if err != nil {
		return err
	}

	// Export rootfs and deploy as a new btrfs subvolume.
	name := cfg.Name + "-" + cfg.Tag + "-" + fingerprint[:12]
	transDir := "/tmp/frzr-meta-root-trans"
	rootfsDir := transDir + "/rootfs"

	if err := client.ExportRootFs(cfg.Name, cfg.Tag, nil, transDir, rootfsDir); err != nil {
		return err
	}

	deployPath := "/frzr_root/deployments"
	if err := core.DeployFromRootfs(rootfsDir, deployPath, name); err != nil {
		return err
	}

	efiPath := "/frzr_root/boot"
	if err := core.CopyKernelFiles(deployPath, efiPath, name); err != nil {
		return err
	}

	if err := core.WriteBootEntry(deployPath, name, ""); err != nil {
		return err
	}

	// Prune old subvolumes.
	current := core.CurrentBtrfsDeployment()
	if err := core.PruneDeployments(deployPath, current, name); err != nil {
		return err
	}

	// Record the new image state.
	newImage, err := core.NewABImage(newDigest, cfg.Name+":"+cfg.Tag)
	if err != nil {
		return err
	}
	if err := newImage.WriteTo("/"); err != nil {
		return err
	}

	fmt.Printf("Upgrade staged: %s\nReboot to apply.\n", name)
	return nil
}
