package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		`frzr-meta-root rollback` — revert to the previous system state.

		In "ab" mode: swaps the A/B partition roles (mirrors `abroot rollback`).
		In "btrfs" mode: points the boot entry at the previous subvolume.
*/

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/core"
	"github.com/openos-project/frzr-meta-root/settings"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Revert to the previous system state",
	Long: `In A/B mode, swaps the present and future partition roles so the
previous system boots on next restart.

In btrfs mode, updates the boot entry to point at the previous subvolume.

Takes effect after reboot.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := settings.Cnf

		switch cfg.DeployMode {
		case "ab":
			client, err := core.NewIncusClient(cfg.IncusSocket, cfg.IncusRemote, cfg.IncusStoragePool)
			if err != nil {
				return err
			}
			sys, err := core.NewABSystem(client)
			if err != nil {
				return err
			}
			res := sys.RollbackSystem(false)
			fmt.Println(res)
			return nil

		case "btrfs":
			return rollbackBtrfs(cfg)

		default:
			return fmt.Errorf("unknown deployMode %q", cfg.DeployMode)
		}
	},
}

// rollbackBtrfs points the boot entry at the deployment that is not currently
// running. If only one deployment exists, rollback is not possible.
func rollbackBtrfs(cfg *settings.Config) error {
	deployPath := "/frzr_root/deployments"
	current := core.CurrentBtrfsDeployment()

	deployments, err := core.ListDeployments(deployPath)
	if err != nil {
		return err
	}

	var previous string
	for _, d := range deployments {
		if d != current {
			previous = d
		}
	}

	if previous == "" {
		return fmt.Errorf("no previous deployment found; rollback not possible")
	}

	if err := core.WriteBootEntry(deployPath, previous, ""); err != nil {
		return err
	}

	fmt.Printf("Rollback staged: will boot %s on next restart.\n", previous)
	return nil
}
