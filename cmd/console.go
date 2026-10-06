package cmd

import (
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"

	"bmbox/pkg/console"
	"bmbox/pkg/engine"
	"bmbox/pkg/workspace"
)

var consoleCmd = &cobra.Command{
	Use:               "console NODE",
	Short:             "Attach the terminal to a node's serial console (Ctrl-] to detach)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeNodes,
	RunE: func(cmd *cobra.Command, args []string) error {
		lab, t, err := resolveLab()
		if err != nil {
			return err
		}
		if t != nil {
			found := false
			for _, n := range t.Spec.Nodes {
				found = found || n.Name == args[0]
			}
			if !found {
				return fmt.Errorf("node %q not found in lab %q", args[0], lab)
			}
		}
		sock := engine.SerialSocket(lab, args[0])
		if _, err := os.Stat(sock); err != nil {
			return fmt.Errorf("node %s is powered off (no serial console at %s). Power it on through its BMC:\n  %s",
				args[0], sock, powerOnHint(lab, args[0]))
		}
		fmt.Fprintf(os.Stderr, "Connected to %s/%s serial console. Detach with Ctrl-].\r\n", lab, args[0])
		err = console.Attach(sock, engine.SerialLog(lab, args[0]), consoleTail, os.Stdin, os.Stdout)
		fmt.Fprint(os.Stderr, "\r\nDetached.\r\n")
		return err
	},
}

var consoleTail int

func init() {
	consoleCmd.Flags().IntVarP(&consoleTail, "tail", "n", 20, "lines of the current boot to show before attaching (0 = none)")
	addLabFlag(consoleCmd)
	rootCmd.AddCommand(consoleCmd)
}

// powerOnHint returns the out-of-band command that powers the node on, from
// the BMC recorded by the last `up`.
func powerOnHint(lab, node string) string {
	ws, err := workspace.Open()
	if err != nil {
		return "sudo virsh start bmbox-" + lab + "-" + node
	}
	st, _ := ws.LoadState(lab)
	if st == nil {
		return "sudo virsh start bmbox-" + lab + "-" + node
	}
	for _, n := range st.Nodes {
		if n.Name != node {
			continue
		}
		switch b := n.BMC; {
		case b == nil:
			return "sudo virsh start " + n.Domain
		case b.Type == "redfish":
			return fmt.Sprintf(`curl -u %s:PASSWORD -H 'Content-Type: application/json' -X POST -d '{"ResetType":"On"}' %s/Actions/ComputerSystem.Reset`,
				b.Username, b.Endpoint)
		default:
			host, port, _ := net.SplitHostPort(b.Endpoint)
			return fmt.Sprintf("ipmitool -I lanplus -H %s -p %s -U %s -P PASSWORD power on", host, port, b.Username)
		}
	}
	return "sudo virsh start bmbox-" + lab + "-" + node
}
