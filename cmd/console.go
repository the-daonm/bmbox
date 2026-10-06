package cmd

import (
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"

	"bmbox/pkg/console"
	"bmbox/pkg/engine"
	"bmbox/pkg/topology"
	"bmbox/pkg/workspace"
)

var consoleCmd = &cobra.Command{
	Use:               "console NODE",
	Short:             "Attach the terminal to a node's serial console (" + console.EscapeName() + " to detach)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeNodes,
	RunE: func(cmd *cobra.Command, args []string) error {
		node := args[0]
		lab, t, err := resolveLab()
		if err != nil {
			return err
		}
		if t != nil {
			found := false
			for _, n := range t.Spec.Nodes {
				found = found || n.Name == node
			}
			if !found {
				return fmt.Errorf("node %q not found in lab %q", node, lab)
			}
		}
		var st *workspace.LabState
		if ws, err := workspace.Open(); err == nil {
			st, _ = ws.LoadState(lab)
		}
		domain := domainName(lab, node, t, st)

		sock := engine.SerialSocket(lab, node)
		if _, err := os.Stat(sock); err != nil {
			return fmt.Errorf("node %s is powered off (no serial console at %s). Power it on through its BMC:\n  %s",
				node, sock, powerOnHint(domain, node, st))
		}
		end, err := console.Attach(console.Options{
			Socket: sock,
			Log:    engine.SerialLog(lab, node),
			Tail:   consoleTail,
			In:     os.Stdin,
			Out:    os.Stdout,
			OnConnect: func() {
				// Same banner as `virsh console`.
				fmt.Fprintf(os.Stderr, "Connected to domain '%s' (lab %s, node %s)\r\nEscape character is %s\r\n",
					domain, lab, node, console.EscapeName())
			},
		})
		if err != nil {
			return err
		}
		if end == console.Closed {
			fmt.Fprintf(os.Stderr, "\r\nConsole closed by %s: the node powered off.\r\n", node)
		} else {
			fmt.Fprint(os.Stderr, "\r\n")
		}
		return nil
	},
}

var consoleTail int

func init() {
	consoleCmd.Flags().IntVarP(&consoleTail, "tail", "n", 20, "lines of the current boot to show before attaching (0 = none)")
	addLabFlag(consoleCmd)
	rootCmd.AddCommand(consoleCmd)
}

// domainName is the node's libvirt domain: from the topology, else the
// recorded state, else the naming scheme for the lab.
func domainName(lab, node string, t *topology.Topology, st *workspace.LabState) string {
	if t != nil {
		return t.DomainName(node)
	}
	if st != nil {
		for _, n := range st.Nodes {
			if n.Name == node && n.Domain != "" {
				return n.Domain
			}
		}
	}
	return (&topology.Topology{Metadata: topology.Metadata{Name: lab}}).DomainName(node)
}

// powerOnHint returns the out-of-band command that powers the node on, from
// the BMC recorded by the last `up`.
func powerOnHint(domain, node string, st *workspace.LabState) string {
	if st != nil {
		for _, n := range st.Nodes {
			if n.Name != node || n.BMC == nil {
				continue
			}
			if b := n.BMC; b.Type == "redfish" {
				return fmt.Sprintf(`curl -u %s:PASSWORD -H 'Content-Type: application/json' -X POST -d '{"ResetType":"On"}' %s/Actions/ComputerSystem.Reset`,
					b.Username, b.Endpoint)
			} else if host, port, err := net.SplitHostPort(b.Endpoint); err == nil {
				return fmt.Sprintf("ipmitool -I lanplus -H %s -p %s -U %s -P PASSWORD power on", host, port, b.Username)
			}
		}
	}
	return "sudo virsh start " + domain
}
