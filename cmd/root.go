// Package cmd implements the bmbox command-line interface.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"bmbox/pkg/hypervisor"
	"bmbox/pkg/topology"
)

var version = "dev"

var (
	topologyFile string
	libvirtURI   string
	labFlag      string
)

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
	defURI := os.Getenv("LIBVIRT_DEFAULT_URI")
	if defURI == "" {
		defURI = hypervisor.DefaultURI
	}
	rootCmd.PersistentFlags().StringVarP(&topologyFile, "file", "f", "topology.yaml", "topology file")
	rootCmd.PersistentFlags().StringVarP(&libvirtURI, "connect", "c", defURI, "libvirt connection URI")
}

// resolveLab returns the lab to act on: --lab, else the topology file's name.
// The topology is returned too when it could be loaded.
func resolveLab() (string, *topology.Topology, error) {
	t, _, err := loadTopology()
	switch {
	case labFlag != "" && (t == nil || t.Metadata.Name != labFlag):
		return labFlag, nil, nil
	case labFlag != "":
		return labFlag, t, nil
	case err != nil:
		return "", nil, fmt.Errorf("%w (or pass --lab NAME)", err)
	}
	return t.Metadata.Name, t, nil
}

func addLabFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&labFlag, "lab", "", "lab name (default: metadata.name of the topology file)")
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
