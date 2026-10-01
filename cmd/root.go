// Package cmd implements the bmbox command-line interface.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "bmbox",
	Short: "Declarative virtualized bare-metal testbed engine",
	Long: `bmbox builds a lab of virtual "bare-metal" servers (KVM + UEFI) from a
declarative topology.yaml: Linux bridges for the networks, libvirt domains
for the nodes, and emulated BMCs for out-of-band control.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       version,
}

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	return err
}
