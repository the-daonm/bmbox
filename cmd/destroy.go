package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"bmbox/pkg/engine"
	"bmbox/pkg/workspace"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Remove everything up created: BMCs, nodes, disks, bridges, sysctls",
	Long: `destroy tears the lab down in reverse order using the state recorded by up
(and the topology, if given, to catch anything a failed up left behind).
Only resources tagged as created by bmbox for this lab are removed. Requires
root.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		lab, t, err := resolveLab()
		if err != nil {
			return err
		}
		ws, err := workspace.Open()
		if err != nil {
			return fmt.Errorf("open workspace: %w", err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return engine.Destroy(ctx, lab, t, ws, engine.Options{LibvirtURI: libvirtURI, Out: os.Stdout})
	},
}

func init() {
	addLabFlag(destroyCmd)
	rootCmd.AddCommand(destroyCmd)
}
