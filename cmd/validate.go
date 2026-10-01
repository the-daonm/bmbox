package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Parse and validate a topology file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		t, _, err := loadTopology()
		if err != nil {
			return err
		}
		fmt.Printf("%s: topology %q is valid\n\n", topologyFile, t.Metadata.Name)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NETWORK\tBRIDGE\tCIDR\tMTU\tEXTERNAL")
		for _, n := range t.Spec.Networks {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%v\n", n.Name, n.Bridge, dash(n.CIDR), n.MTU, n.External)
		}
		w.Flush()
		fmt.Println()

		fmt.Fprintln(w, "NODE\tDOMAIN\tCPU\tMEMORY\tDISKS\tNICS\tBOOT\tBMC")
		for _, n := range t.Spec.Nodes {
			disks := ""
			for i, d := range n.Disks {
				if i > 0 {
					disks += ","
				}
				disks += d.Size.String()
			}
			nics := ""
			for i, nic := range n.NICs {
				if i > 0 {
					nics += ","
				}
				nics += nic.Network + "=" + nic.MAC
			}
			bmc := "-"
			if n.BMC != nil {
				bmc = n.BMC.Type
				if n.BMC.Port != 0 {
					bmc += fmt.Sprintf(":%d", n.BMC.Port)
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\t%v\t%s\n",
				n.Name, t.DomainName(n.Name), n.CPUs, n.Memory, dash(disks), dash(nics), n.Boot, bmc)
		}
		return w.Flush()
	},
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func init() { rootCmd.AddCommand(validateCmd) }
