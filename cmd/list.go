package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"bmbox/pkg/workspace"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the labs in the workspace",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, err := workspace.Open()
		if err != nil {
			return err
		}
		labs, err := ws.Labs()
		if err != nil {
			return err
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

func init() { rootCmd.AddCommand(listCmd) }
