package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"bmbox/pkg/bmc"
	"bmbox/pkg/engine"
	"bmbox/pkg/workspace"
)

var (
	sushyPath string
	vbmcPath  string
	prune     bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Create the lab: networks, nodes (powered off) and their BMCs",
	Long: `up creates the Linux bridges, the storage pool, one sparse qcow2 per disk and
a private UEFI NVRAM per node, defines each node in libvirt in the SHUTOFF
state, then starts one emulated BMC (Redfish or IPMI) per node as a systemd
unit. Running it again converges to the topology without wiping existing
disks or NVRAM. Requires root.`,
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
		// Tool paths: flag, then environment, then what worked last time.
		tools := ws.LoadTools()
		sushy := firstNonEmpty(sushyPath, os.Getenv(bmc.EnvSushyEmulator), tools.SushyEmulator)
		vbmc := firstNonEmpty(vbmcPath, os.Getenv(bmc.EnvVBMC), tools.VBMC)

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		st, err := engine.Up(ctx, t, raw, ws, engine.Options{
			LibvirtURI: libvirtURI, Out: os.Stdout,
			SushyEmulator: sushy, VBMC: vbmc, Prune: prune,
		})
		if err != nil {
			return err
		}
		if err := ws.SaveTools(workspace.Tools{SushyEmulator: sushy, VBMC: vbmc}); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not remember BMC tool paths:", err)
		}
		fmt.Printf("\nLab %q is up: %d network(s), %d node(s). State: %s\n",
			st.Lab, len(st.Networks), len(st.Nodes), ws.LabDir(st.Lab))
		return nil
	},
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func init() {
	upCmd.Flags().StringVar(&sushyPath, "sushy-emulator", "", "path to sushy-emulator (default: $"+bmc.EnvSushyEmulator+", last used, or PATH)")
	upCmd.Flags().StringVar(&vbmcPath, "vbmc", "", "path to vbmc, used to find virtualbmc's python (default: $"+bmc.EnvVBMC+", last used, or PATH)")
	upCmd.Flags().BoolVar(&prune, "prune", false, "remove nodes, BMCs, disks and networks of this lab that are no longer in the topology")
	rootCmd.AddCommand(upCmd)
}
