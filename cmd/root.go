// Package cmd implements the bmbox command-line interface.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"bmbox/pkg/topology"
)

var version = "dev"

var topologyFile string

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

func init() {
	rootCmd.PersistentFlags().StringVarP(&topologyFile, "file", "f", "topology.yaml", "topology file")
}

// loadTopology returns the parsed topology together with the raw bytes that
// are snapshotted into the workspace.
func loadTopology() (*topology.Topology, []byte, error) {
	raw, err := os.ReadFile(topologyFile)
	if err != nil {
		return nil, nil, err
	}
	t, err := topology.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", topologyFile, err)
	}
	return t, raw, nil
}
