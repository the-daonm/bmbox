package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"bmbox/pkg/console"
	"bmbox/pkg/engine"
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
			return fmt.Errorf("no serial console at %s: is the node powered on? (%w)", sock, err)
		}
		fmt.Fprintf(os.Stderr, "Connected to %s/%s serial console. Detach with Ctrl-].\r\n", lab, args[0])
		err = console.Attach(sock, os.Stdin, os.Stdout)
		fmt.Fprint(os.Stderr, "\r\nDetached.\r\n")
		return err
	},
}

func init() {
	addLabFlag(consoleCmd)
	rootCmd.AddCommand(consoleCmd)
}
