package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		Root cobra command for frzr-meta-root CLI.
		Structure mirrors ABRoot v2's cmd layout.
*/

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/settings"
)

var verbose bool

var rootCmd = &cobra.Command{
	Use:   "frzr-meta-root",
	Short: "Immutable, atomic Linux root manager",
	Long: `frzr-meta-root provides full immutability and atomicity for Linux systems.

It supports two deployment models:
  ab     — A/B partition transactions (default, strongest atomicity)
  btrfs  — btrfs read-only subvolume deployments (frzr-compatible)

Images are pulled from OCI registries via Incus. No Docker or Podman required.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return settings.Load()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Show more detailed output")

	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(pkgCmd)
	rootCmd.AddCommand(rollbackCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(kargsCmd)
	rootCmd.AddCommand(bootstrapCmd)
}
