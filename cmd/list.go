package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"bmbox/pkg/workspace"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the labs in the workspace",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, err := jsonOutput()
		if err != nil {
			return err
		}
		ws, err := workspace.Open()
		if err != nil {
			return err
		}
		labs, err := ws.Labs()
		if err != nil {
			return err
		}
		if asJSON {
			out := []labJSON{}
			for _, lab := range labs {
				l := labJSON{Name: lab, Dir: ws.LabDir(lab)}
				if st, err := ws.LoadState(lab); err == nil && st != nil {
					l.Nodes, l.Networks = len(st.Nodes), len(st.Networks)
					l.UpdatedAt = st.UpdatedAt.UTC().Format(time.RFC3339)
				}
				out = append(out, l)
			}
			return printJSON(out)
		}
		if len(labs) == 0 {
			fmt.Printf("No labs in %s\n", ws.Root)
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "LAB\tNODES\tNETWORKS\tUPDATED")
		for _, lab := range labs {
			st, err := ws.LoadState(lab)
			if err != nil || st == nil {
				fmt.Fprintf(w, "%s\t-\t-\t(no state)\n", lab)
				continue
			}
			fmt.Fprintf(w, "%s\t%d\t%d\t%s\n", lab, len(st.Nodes), len(st.Networks), st.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		return w.Flush()
	},
}

type labJSON struct {
	Name      string `json:"name"`
	Nodes     int    `json:"nodes"`
	Networks  int    `json:"networks"`
	UpdatedAt string `json:"updatedAt,omitempty"` // empty: no state
	Dir       string `json:"dir"`
}

func init() {
	addOutputFlag(listCmd)
	rootCmd.AddCommand(listCmd)
}
