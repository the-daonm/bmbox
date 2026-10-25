package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/the-daonm/bmbox/pkg/setup"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

var (
	setupDryRun       bool
	setupSkipPackages bool
	setupVenv         string
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install and configure what bmbox needs on this host",
	Long: `setup prepares the host for bmbox in one step:

  - installs the distribution packages bmbox uses (QEMU, libvirt, OVMF,
    dnsmasq, iPXE, python3-venv, python3-libvirt, ipmitool) with apt-get,
    dnf or pacman; on Debian and Ubuntu only dnsmasq-base, so no dnsmasq
    service starts
  - starts libvirt if it is not running
  - creates a Python virtualenv with sushy-tools ` + setup.SushyToolsVersion + ` and virtualbmc
    ` + setup.VirtualBMCVersion + `, the versions bmbox is tested with
  - remembers every tool path, so up needs no flags or variables

It is safe to run again. Requires root, except with --dry-run.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Geteuid() != 0 && !setupDryRun {
			return errors.New("bmbox setup must run as root: sudo bmbox setup (or --dry-run to see what it would do)")
		}
		ws, err := workspace.Open()
		if err != nil {
			return err
		}
		venv := setupVenv
		if venv == "" {
			venv = setup.DefaultVenvDir(ws)
		}
		out := progress()
		known := ws.LoadTools()
		tools, err := setup.Run(setup.Options{
			Out: out, System: setup.Host{Out: out},
			DryRun: setupDryRun, SkipPackages: setupSkipPackages,
			VenvDir: venv, Dnsmasq: known.Dnsmasq, IPXE: firstNonEmpty(ipxePath, known.IPXE),
		})
		if err != nil {
			return err
		}
		if setupDryRun {
			fmt.Fprintln(out, "\nDry run: nothing was changed.")
			return nil
		}
		// Keep what setup could not find but an earlier run knew.
		tools.Dnsmasq = firstNonEmpty(tools.Dnsmasq, known.Dnsmasq)
		tools.IPXE = firstNonEmpty(tools.IPXE, known.IPXE)
		if err := ws.SaveTools(tools); err != nil {
			return fmt.Errorf("remember tool paths: %w", err)
		}
		fmt.Fprintf(out, "\nHost ready. Tool paths saved in %s/tools.json.\nNext: bmbox doctor, then sudo bmbox up -f topology.yaml\n", ws.Root)
		return nil
	},
}

func init() {
	setupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "print what would be installed and run, change nothing")
	setupCmd.Flags().BoolVar(&setupSkipPackages, "skip-packages", false, "do not install distribution packages")
	setupCmd.Flags().StringVar(&setupVenv, "venv", "", "virtualenv for sushy-tools and virtualbmc (default: <workspace>/tools/venv)")
	setupCmd.Flags().StringVar(&ipxePath, "ipxe", "", "path to the iPXE UEFI binary, when not in a distribution path")
	rootCmd.AddCommand(setupCmd)
}
