package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/the-daonm/bmbox/pkg/bmc"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

var credentialsCmd = &cobra.Command{
	Use:               "credentials [NODE]",
	Aliases:           []string{"creds"},
	Short:             "Show the BMC endpoint, username and password of the lab's nodes",
	Long:              "credentials prints what a client needs to reach each node's BMC, including\npasswords bmbox generated. The passwords are readable by root only.",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeNodes,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, err := jsonOutput()
		if err != nil {
			return err
		}
		lab, _, err := resolveLab()
		if err != nil {
			return err
		}
		ws, err := workspace.Open()
		if err != nil {
			return err
		}
		st, err := ws.LoadState(lab)
		if err != nil {
			return err
		}
		if st == nil {
			return fmt.Errorf("lab %q is not up (no state in %s)", lab, ws.LabDir(lab))
		}
		creds := []credential{}
		found := false
		for _, n := range st.Nodes {
			if len(args) == 1 && n.Name != args[0] {
				continue
			}
			found = true
			if n.BMC == nil {
				continue
			}
			c := credential{Node: n.Name, Type: n.BMC.Type, Endpoint: n.BMC.Endpoint, Username: n.BMC.Username, CACert: n.BMC.CACert}
			file := n.BMC.PasswordFile
			if file == "" {
				file = bmc.PasswordPath(ws.BMCDir(lab, n.Name))
			}
			if c.Password, err = bmc.ReadPassword(filepath.Dir(file)); err != nil {
				if os.IsPermission(err) {
					return fmt.Errorf("BMC passwords are readable by root only: sudo bmbox credentials")
				}
				return fmt.Errorf("node %s: %w (run bmbox up to store it)", n.Name, err)
			}
			creds = append(creds, c)
		}
		if len(args) == 1 && !found {
			return fmt.Errorf("node %q not found in lab %q", args[0], lab)
		}
		if asJSON {
			return printJSON(creds)
		}
		if len(creds) == 0 {
			fmt.Printf("No BMCs in lab %q\n", lab)
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NODE\tTYPE\tUSERNAME\tPASSWORD\tENDPOINT")
		for _, c := range creds {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Node, c.Type, c.Username, c.Password, c.Endpoint)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		for _, c := range creds {
			if c.CACert != "" {
				fmt.Printf("\nHTTPS endpoints are signed by the bmbox CA: %s\n", c.CACert)
				break
			}
		}
		return nil
	},
}

type credential struct {
	Node     string `json:"node"`
	Type     string `json:"type"`
	Endpoint string `json:"endpoint"`
	Username string `json:"username"`
	Password string `json:"password"`
	CACert   string `json:"caCert,omitempty"` // CA to trust for an https endpoint
}

func init() {
	addOutputFlag(credentialsCmd)
	addLabFlag(credentialsCmd)
	rootCmd.AddCommand(credentialsCmd)
}
