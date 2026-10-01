package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"bmbox/pkg/engine"
	"bmbox/pkg/workspace"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Create the lab networks and define its nodes (powered off)",
	Long: `up creates the Linux bridges, the storage pool, one sparse qcow2 per disk and
a private UEFI NVRAM per node, then defines each node in libvirt in the
SHUTOFF state. Running it again converges to the topology without wiping
existing disks or NVRAM. Requires root.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t, raw, err := loadTopology()
		if err != nil {
			return err
		}
		ws, err := workspace.Open()
		if err != nil {
			return fmt.Errorf("open workspace: %w", err)
		}
		st, err := engine.Up(t, raw, ws, engine.Options{LibvirtURI: libvirtURI, Out: os.Stdout})
		if err != nil {
			return err
		}
		fmt.Printf("\nLab %q is up: %d network(s), %d node(s). State: %s\n",
			st.Lab, len(st.Networks), len(st.Nodes), ws.LabDir(st.Lab))
		return nil
	},
}

func init() { rootCmd.AddCommand(upCmd) }
