// Package cmd implements the bmbox command-line interface.
package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/the-daonm/bmbox/pkg/hypervisor"
	"github.com/the-daonm/bmbox/pkg/logging"
	"github.com/the-daonm/bmbox/pkg/topology"
)

var version = "dev"

var (
	topologyFile string
	libvirtURI   string
	labFlag      string
	quiet        bool
	verbose      bool
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
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if quiet && verbose {
			return fmt.Errorf("--quiet and --verbose cannot be used together")
		}
		if verbose {
			logging.SetDebug(stamp(os.Stderr))
		}
		return nil
	},
}

// stamp adds timestamps to f's lines when it is not a terminal.
func stamp(f *os.File) io.Writer {
	if logging.WantTimestamps(f) {
		return &logging.Timestamped{W: f}
	}
	return f
}

// progress is where up and destroy report what they do: nothing with
// --quiet, timestamped lines when stdout is not a terminal.
func progress() io.Writer {
	if quiet {
		return io.Discard
	}
	return stamp(os.Stdout)
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
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "do not print progress (up, destroy); errors are still printed")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "also print the commands and system calls bmbox makes (on stderr)")
}

// resolveLab returns the lab to act on: --lab, else the topology file's name.
// The topology is returned too when it could be loaded.
func resolveLab() (string, *topology.Topology, error) {
	if labFlag != "" {
		if err := topology.ValidateLabName(labFlag); err != nil {
			return "", nil, fmt.Errorf("--lab: %w", err)
		}
	}
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

// completeNodes offers the node names of the topology file for shell
// completion of a single NODE argument.
func completeNodes(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	t, _, err := loadTopology()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, n := range t.Spec.Nodes {
		names = append(names, n.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
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
