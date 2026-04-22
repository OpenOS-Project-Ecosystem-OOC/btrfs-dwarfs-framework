package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		`frzr-meta-root status` — display current system state.
		Mirrors `abroot status` and `frzr-release`.
*/

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/core"
	"github.com/openos-project/frzr-meta-root/settings"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display current system state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := settings.Cnf

		img, err := core.NewABImageFromRoot()
		if err != nil {
			return fmt.Errorf("reading image state: %w", err)
		}

		fmt.Printf("Deploy mode:  %s\n", cfg.DeployMode)
		fmt.Printf("Image:        %s:%s\n", cfg.Name, cfg.Tag)
		fmt.Printf("Digest:       %s\n", img.Digest)
		fmt.Printf("Deployed at:  %s\n", img.Timestamp.Format("2006-01-02 15:04:05"))

		switch cfg.DeployMode {
		case "ab":
			rm := core.NewABRootManager()
			for _, p := range rm.Partitions {
				role := "future"
				if p.Current {
					role = "present"
				}
				fmt.Printf("Partition %s: %s (%s)\n", p.Label, role, p.FsType)
			}

		case "btrfs":
			current := core.CurrentBtrfsDeployment()
			next := core.NextBootDeployment(core.BootCfgPath)
			fmt.Printf("Current:      %s\n", current)
			fmt.Printf("Next boot:    %s\n", next)
		}

		return nil
	},
}
