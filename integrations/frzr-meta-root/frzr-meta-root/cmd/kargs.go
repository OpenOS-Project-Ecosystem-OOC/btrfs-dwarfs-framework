package cmd

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		`frzr-meta-root kargs` — kernel argument management.
		Directly ported from ABRoot v2's cmd/kargs.go. No changes needed
		since kernel args are independent of the OCI backend.
*/

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/openos-project/frzr-meta-root/core"
)

var kargsCmd = &cobra.Command{
	Use:   "kargs",
	Short: "Manage kernel parameters",
}

var kargsEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit kernel parameters interactively",
	RunE: func(cmd *cobra.Command, args []string) error {
		return core.KargsEdit()
	},
}

var kargsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current kernel parameters",
	RunE: func(cmd *cobra.Command, args []string) error {
		kargs, err := core.KargsShow()
		if err != nil {
			return err
		}
		fmt.Println(kargs)
		return nil
	},
}

func init() {
	kargsCmd.AddCommand(kargsEditCmd, kargsShowCmd)
}
