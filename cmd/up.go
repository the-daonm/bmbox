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
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		st, err := engine.Up(ctx, t, raw, ws, engine.Options{
			LibvirtURI: libvirtURI, Out: os.Stdout,
			SushyEmulator: sushyPath, VBMC: vbmcPath,
		})
		if err != nil {
			return err
		}
		fmt.Printf("\nLab %q is up: %d network(s), %d node(s). State: %s\n",
			st.Lab, len(st.Networks), len(st.Nodes), ws.LabDir(st.Lab))
		return nil
	},
}

func init() {
	upCmd.Flags().StringVar(&sushyPath, "sushy-emulator", "", "path to sushy-emulator (default: $"+bmc.EnvSushyEmulator+" or PATH)")
	upCmd.Flags().StringVar(&vbmcPath, "vbmc", "", "path to vbmc, used to find virtualbmc's python (default: $"+bmc.EnvVBMC+" or PATH)")
	rootCmd.AddCommand(upCmd)
}
