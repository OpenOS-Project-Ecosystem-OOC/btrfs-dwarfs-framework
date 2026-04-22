package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		`frzr-meta-root pkg` — local package management (A/B mode only).
		Mirrors `abroot pkg`. Not applicable in btrfs mode (use the
		image build pipeline instead).

		Package changes are staged in /etc/frzr-meta-root/packages.{add,remove}
		and applied on the next `upgrade` by building a local OCI image via
		Incus (incus exec + incus publish) rather than buildah.
*/

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/core"
	"github.com/openos-project/frzr-meta-root/settings"
)

var pkgDryRun bool

var pkgCmd = &cobra.Command{
	Use:   "pkg",
	Short: "Manage packages (A/B mode only)",
	Long: `Stage package additions and removals to be applied on the next upgrade.

Changes are accumulated in /etc/frzr-meta-root/packages.{add,remove} and
applied atomically during the next upgrade transaction via Incus.

Not available in btrfs deploy mode.`,
}

var pkgAddCmd = &cobra.Command{
	Use:   "add <package> [package...]",
	Short: "Stage packages for installation",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return pkgOp(core.ADD, args)
	},
}

var pkgRemoveCmd = &cobra.Command{
	Use:   "remove <package> [package...]",
	Short: "Stage packages for removal",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return pkgOp(core.REMOVE, args)
	},
}

var pkgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List staged package changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := settings.Cnf
		if cfg.DeployMode != "ab" {
			return fmt.Errorf("pkg is only available in A/B deploy mode")
		}

		pm, err := core.NewPackageManager(false)
		if err != nil {
			return err
		}

		adds, err := pm.GetPackagesToAdd()
		if err != nil {
			return err
		}
		removes, err := pm.GetPackagesToRemove()
		if err != nil {
			return err
		}

		fmt.Println("Staged additions:")
		for _, p := range adds {
			fmt.Println(" +", p)
		}
		fmt.Println("Staged removals:")
		for _, p := range removes {
			fmt.Println(" -", p)
		}
		return nil
	},
}

var pkgApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply staged package changes now",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := settings.Cnf
		if cfg.DeployMode != "ab" {
			return fmt.Errorf("pkg is only available in A/B deploy mode")
		}

		client, err := core.NewIncusClient(cfg.IncusSocket, cfg.IncusRemote, cfg.IncusStoragePool)
		if err != nil {
			return err
		}

		sys, err := core.NewABSystem(client)
		if err != nil {
			return err
		}

		return sys.RunOperation(core.APPLY, pkgDryRun)
	},
}

func init() {
	pkgApplyCmd.Flags().BoolVar(&pkgDryRun, "dry-run", false, "Simulate without writing to disk")
	pkgCmd.AddCommand(pkgAddCmd, pkgRemoveCmd, pkgListCmd, pkgApplyCmd)
}

func pkgOp(op string, packages []string) error {
	cfg := settings.Cnf
	if cfg.DeployMode != "ab" {
		return fmt.Errorf("pkg is only available in A/B deploy mode")
	}

	pm, err := core.NewPackageManager(false)
	if err != nil {
		return err
	}

	for _, pkg := range packages {
		if err := pm.Add(pkg, op); err != nil {
			return fmt.Errorf("staging %s %s: %w", op, pkg, err)
		}
		fmt.Printf("Staged: %s %s\n", op, pkg)
	}

	fmt.Println("Run `frzr-meta-root pkg apply` or `frzr-meta-root upgrade` to apply.")
	return nil
}
