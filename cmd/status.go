package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

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
		asJSON, err := jsonOutput()
		if err != nil {
			return err
		}
		ls, err := engine.Status(context.Background(), lab, ws, engine.Options{LibvirtURI: libvirtURI})
		if err != nil {
			return err
		}
		st := ls.State
		if asJSON {
			return printJSON(statusJSON(ls))
		}

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
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", n.Name, n.Power, dash(strings.Join(n.IPs, ",")), dash(n.BMCType), dash(n.BMCState), dash(n.Endpoint))
		}
		return w.Flush()
	},
}

type labStatusJSON struct {
	Lab       string              `json:"lab"`
	UpdatedAt string              `json:"updatedAt"`
	Libvirt   string              `json:"libvirt"`
	Networks  []networkStatusJSON `json:"networks"`
	DHCP      *dhcpStatusJSON     `json:"dhcp,omitempty"`
	Nodes     []engine.NodeStatus `json:"nodes"`
}

type networkStatusJSON struct {
	Name     string `json:"name"`
	Bridge   string `json:"bridge"`
	Address  string `json:"address,omitempty"`
	External bool   `json:"external"`
}

type dhcpStatusJSON struct {
	Unit  string `json:"unit"`
	State string `json:"state"`
}

func statusJSON(ls *engine.LabStatus) labStatusJSON {
	st := ls.State
	out := labStatusJSON{
		Lab: st.Lab, UpdatedAt: st.UpdatedAt.UTC().Format(time.RFC3339), Libvirt: st.Libvirt,
		Networks: []networkStatusJSON{}, Nodes: ls.Nodes,
	}
	if out.Nodes == nil {
		out.Nodes = []engine.NodeStatus{}
	}
	for _, n := range st.Networks {
		out.Networks = append(out.Networks, networkStatusJSON{Name: n.Name, Bridge: n.Bridge, Address: n.Address, External: n.External})
	}
	if st.DHCP != nil {
		out.DHCP = &dhcpStatusJSON{Unit: st.DHCP.Unit, State: ls.DHCPState}
	}
	return out
}

func init() {
	addOutputFlag(statusCmd)
	addLabFlag(statusCmd)
	rootCmd.AddCommand(statusCmd)
}
