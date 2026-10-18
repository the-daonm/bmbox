package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"bmbox/pkg/engine"
	"bmbox/pkg/workspace"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the power state and BMC endpoint of every node",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		lab, _, err := resolveLab()
		if err != nil {
			return err
		}
		ws, err := workspace.Open()
		if err != nil {
			return err
		}
		ls, err := engine.Status(context.Background(), lab, ws, engine.Options{LibvirtURI: libvirtURI})
		if err != nil {
			return err
		}
		st := ls.State

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "Lab %q (updated %s)\n\n", st.Lab, st.UpdatedAt.Local().Format("2006-01-02 15:04"))
		fmt.Fprintln(w, "NETWORK\tBRIDGE\tADDRESS")
		for _, n := range st.Networks {
			fmt.Fprintf(w, "%s\t%s\t%s\n", n.Name, n.Bridge, dash(n.Address))
		}
		if st.DHCP != nil {
			fmt.Fprintf(w, "\nDHCP: %s (%s)\n", ls.DHCPState, st.DHCP.Unit)
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "NODE\tPOWER\tIP\tBMC\tBMC STATE\tENDPOINT")
		for _, n := range ls.Nodes {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", n.Name, n.Power, dash(strings.Join(n.IPs, ",")), n.BMCType, n.BMCState, n.Endpoint)
		}
		return w.Flush()
	},
}

func init() {
	addLabFlag(statusCmd)
	rootCmd.AddCommand(statusCmd)
}
