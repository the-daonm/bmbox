package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"bmbox/pkg/bmc"
	"bmbox/pkg/doctor"
	"bmbox/pkg/netboot"
	"bmbox/pkg/workspace"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that this host can run bmbox labs",
	Long: `doctor checks KVM, libvirt and its UEFI firmware, systemd, the firewall, the
BMC emulators and their versions, dnsmasq, iPXE, and whether each lab comes
back after a reboot, and says how to fix what is missing. It exits with an
error when a check fails.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, err := jsonOutput()
		if err != nil {
			return err
		}
		ws, err := workspace.Open()
		if err != nil {
			return err
		}
		known := ws.LoadTools()
		checks := doctor.Run(context.Background(), doctor.Options{
			LibvirtURI: libvirtURI,
			Workspace:  ws,
			Tools: workspace.Tools{
				SushyEmulator: firstNonEmpty(os.Getenv(bmc.EnvSushyEmulator), known.SushyEmulator),
				VBMC:          firstNonEmpty(os.Getenv(bmc.EnvVBMC), known.VBMC),
				Dnsmasq:       firstNonEmpty(os.Getenv(netboot.EnvDnsmasq), known.Dnsmasq),
				IPXE:          firstNonEmpty(os.Getenv(netboot.EnvIPXE), known.IPXE),
			},
		})
		if asJSON {
			if err := printJSON(checks); err != nil {
				return err
			}
		} else {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, c := range checks {
				fmt.Fprintf(w, "%s\t%s\t%s\n", strings.ToUpper(string(c.Status)), c.Name, c.Detail)
				if c.Fix != "" && c.Status != doctor.OK {
					fmt.Fprintf(w, "\t\t-> %s\n", c.Fix)
				}
			}
			w.Flush()
		}
		if doctor.Failed(checks) {
			return errors.New("some checks failed")
		}
		return nil
	},
}

func init() {
	addOutputFlag(doctorCmd)
	rootCmd.AddCommand(doctorCmd)
}
