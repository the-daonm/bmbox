package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"bmbox/pkg/engine"
	"bmbox/pkg/hypervisor"
)

var renderCmd = &cobra.Command{
	Use:   "render [node]",
	Short: "Print the libvirt domain XML bmbox would define (offline, no changes)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, _, err := loadTopology()
		if err != nil {
			return err
		}
		// Offline: resolve firmware from the topology or local well-known
		// paths; `up` asks libvirt instead.
		var nilClient *hypervisor.Client
		fw, err := nilClient.ResolveFirmware(t.Spec.UEFI)
		if err != nil {
			fw = hypervisor.Firmware{
				Loader:       "/usr/share/OVMF/OVMF_CODE_4M.fd",
				VarsTemplate: "/usr/share/OVMF/OVMF_VARS_4M.fd",
			}
		}
		found := false
		for i := range t.Spec.Nodes {
			n := &t.Spec.Nodes[i]
			if len(args) == 1 && n.Name != args[0] {
				continue
			}
			found = true
			var disks []string
			for j := range n.Disks {
				disks = append(disks, t.Spec.Storage.Path+"/"+engine.DiskVolumeName(n.Name, j))
			}
			nvram := t.Spec.Storage.Path + "/" + engine.NVRAMVolumeName(n.Name)
			xml, err := hypervisor.BuildDomainXML(engine.NodeSpec(t, n, fw, disks, nvram))
			if err != nil {
				return err
			}
			fmt.Printf("<!-- node %s -->\n%s\n", n.Name, xml)
		}
		if !found {
			return fmt.Errorf("node %q not found in %s", args[0], topologyFile)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(renderCmd) }
