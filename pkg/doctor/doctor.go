// Package doctor checks that a host can run bmbox labs and says how to fix
// what is missing.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/the-daonm/bmbox/pkg/bmc"
	"github.com/the-daonm/bmbox/pkg/engine"
	"github.com/the-daonm/bmbox/pkg/hypervisor"
	"github.com/the-daonm/bmbox/pkg/netboot"
	"github.com/the-daonm/bmbox/pkg/network"
	"github.com/the-daonm/bmbox/pkg/setup"
	"github.com/the-daonm/bmbox/pkg/systemd"
	"github.com/the-daonm/bmbox/pkg/topology"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn" // works, but something is limited or risky
	Fail Status = "fail" // up will not work
)

// Check is one result. The JSON form is printed by `doctor --output json`.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type Options struct {
	LibvirtURI string
	Workspace  *workspace.Workspace
	// Tools are the remembered or given tool paths (as up resolves them).
	Tools workspace.Tools
}

// Run performs every check.
func Run(ctx context.Context, opt Options) []Check {
	var cs []Check
	add := func(c Check) { cs = append(cs, c) }

	add(checkRoot())
	add(checkKVM())
	add(checkLibvirt(opt.LibvirtURI))
	add(checkSystemd(ctx))
	add(checkFirewall())
	add(checkRedfish(opt.Tools.SushyEmulator))
	add(checkIPMI(opt.Tools.VBMC))
	add(checkDnsmasq(opt.Tools.Dnsmasq))
	add(checkIPXE(opt.Tools.Dnsmasq, opt.Tools.IPXE))
	add(checkBinary())
	cs = append(cs, checkLabs(opt.Workspace)...)
	return cs
}

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

func checkRoot() Check {
	if os.Geteuid() == 0 {
		return Check{Name: "privileges", Status: OK, Detail: "running as root"}
	}
	return Check{Name: "privileges", Status: Warn, Detail: "not root: some checks are partial",
		Fix: "sudo bmbox doctor"}
}

func checkKVM() Check {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err == nil {
		f.Close()
		return Check{Name: "kvm", Status: OK, Detail: "/dev/kvm is usable"}
	}
	if os.IsNotExist(err) {
		return Check{Name: "kvm", Status: Fail, Detail: "/dev/kvm does not exist",
			Fix: "enable virtualization (VT-x/AMD-V) in the firmware, or nested virtualization on a VM, and load kvm_intel or kvm_amd"}
	}
	if os.IsPermission(err) && os.Geteuid() != 0 {
		return Check{Name: "kvm", Status: OK, Detail: "/dev/kvm exists (only root or the kvm group can open it)"}
	}
	return Check{Name: "kvm", Status: Fail, Detail: err.Error()}
}

func checkLibvirt(uri string) Check {
	hv, err := hypervisor.Connect(uri)
	if err != nil {
		return Check{Name: "libvirt", Status: Fail, Detail: err.Error(),
			Fix: "sudo bmbox setup (installs and starts libvirt)"}
	}
	defer hv.Close()
	ver, _ := hv.Version()
	fw, err := hv.ResolveFirmware(topology.UEFI{})
	if err != nil {
		return Check{Name: "libvirt", Status: Fail, Detail: fmt.Sprintf("libvirt %s at %s, but no UEFI firmware: %v", ver, uri, err),
			Fix: "install OVMF (ovmf, edk2-ovmf): sudo bmbox setup"}
	}
	return Check{Name: "libvirt", Status: OK, Detail: fmt.Sprintf("libvirt %s at %s, UEFI loader %s", ver, uri, fw.Loader)}
}

func checkSystemd(ctx context.Context) Check {
	sd, err := systemd.Connect(ctx)
	if err != nil {
		return Check{Name: "systemd", Status: Fail, Detail: err.Error(),
			Fix: "bmbox runs BMCs and dnsmasq as systemd units: run it on a systemd host, as root"}
	}
	sd.Close()
	return Check{Name: "systemd", Status: OK, Detail: "reachable over D-Bus"}
}

func checkFirewall() Check {
	fw := network.NewFirewall()
	nf, _ := network.ReadSysctl("net/bridge/bridge-nf-call-iptables")
	switch {
	case fw.Available():
		detail := fw.Tools() + " found"
		if nf == "1" {
			detail += "; bridged traffic goes through iptables (bridge-nf-call-iptables=1), bmbox adds rules for its bridges"
		}
		return Check{Name: "firewall", Status: OK, Detail: detail}
	case nf == "1":
		return Check{Name: "firewall", Status: Fail, Detail: "bridge-nf-call-iptables=1 but no iptables: bmbox cannot let lab traffic past FORWARD",
			Fix: "install iptables: sudo bmbox setup"}
	}
	return Check{Name: "firewall", Status: OK, Detail: "no iptables and bridged traffic is not filtered"}
}

// pythonPackageVersion asks the interpreter for an installed package's
// version.
func pythonPackageVersion(python, pkg string) (string, error) {
	out, err := exec.Command(python, "-c", "import importlib.metadata as m, sys; print(m.version(sys.argv[1]))", pkg).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", pkg, strings.TrimSpace(lastLine(string(out))))
	}
	return strings.TrimSpace(string(out)), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func versionCheck(name, pkg, module, python, want string) Check {
	got, err := pythonPackageVersion(python, pkg)
	if err != nil {
		return Check{Name: name, Status: Fail, Detail: err.Error(), Fix: "sudo bmbox setup"}
	}
	// Load it as the BMC unit will: an installed package can still fail
	// to import, e.g. when host packages shadow its dependencies.
	if out, err := exec.Command(python, "-c", "import libvirt, "+module).CombinedOutput(); err != nil {
		return Check{Name: name, Status: Fail, Detail: fmt.Sprintf("%s %s does not load: %s", pkg, got, lastLine(string(out))),
			Fix: "sudo bmbox setup (installs it in an isolated virtualenv)"}
	}
	if got != want {
		return Check{Name: name, Status: Warn, Detail: fmt.Sprintf("%s %s (bmbox is tested with %s)", pkg, got, want),
			Fix: "sudo bmbox setup (installs the tested version in bmbox's own virtualenv)"}
	}
	return Check{Name: name, Status: OK, Detail: fmt.Sprintf("%s %s with %s", pkg, got, python)}
}

func checkRedfish(sushy string) Check {
	t, err := bmc.FindTools(sushy, "", map[string]bool{bmc.TypeRedfish: true})
	if err != nil {
		return Check{Name: "redfish BMC", Status: Warn, Detail: err.Error() + " (needed by redfish BMCs)", Fix: "sudo bmbox setup"}
	}
	python, err := bmc.Interpreter(t.SushyEmulator)
	if err != nil {
		return Check{Name: "redfish BMC", Status: Warn, Detail: err.Error()}
	}
	return versionCheck("redfish BMC", "sushy-tools", "sushy_tools.emulator.main", python, setup.SushyToolsVersion)
}

func checkIPMI(vbmc string) Check {
	t, err := bmc.FindTools("", vbmc, map[string]bool{bmc.TypeIPMI: true})
	if err != nil {
		return Check{Name: "ipmi BMC", Status: Warn, Detail: err.Error() + " (needed by ipmi BMCs)", Fix: "sudo bmbox setup"}
	}
	return versionCheck("ipmi BMC", "virtualbmc", "virtualbmc.vbmc", t.VBMCPython, setup.VirtualBMCVersion)
}

func checkDnsmasq(dnsmasq string) Check {
	t, err := netboot.FindTools(dnsmasq, "", false)
	if err != nil {
		return Check{Name: "dnsmasq", Status: Warn, Detail: err.Error() + " (needed by networks with dhcp)", Fix: "sudo bmbox setup"}
	}
	out, _ := exec.Command(t.Dnsmasq, "--version").Output()
	ver, _, _ := strings.Cut(strings.SplitN(string(out), "\n", 2)[0], "  Copyright")
	return Check{Name: "dnsmasq", Status: OK, Detail: strings.TrimSpace(ver) + " at " + t.Dnsmasq}
}

// checkIPXE looks for iPXE the way up does, with the remembered dnsmasq:
// FindTools resolves dnsmasq first, and dnsmasq is often not on the PATH.
func checkIPXE(dnsmasq, ipxe string) Check {
	t, err := netboot.FindTools(dnsmasq, ipxe, true)
	if t.IPXE == "" {
		msg := "iPXE UEFI binary not found"
		if err != nil {
			msg = err.Error()
		}
		return Check{Name: "ipxe", Status: Warn, Detail: msg + " (needed by spec.netboot)", Fix: "sudo bmbox setup"}
	}
	return Check{Name: "ipxe", Status: OK, Detail: t.IPXE}
}

// checkBinary warns when bmbox runs from a place a boot unit should not
// depend on, such as a home directory.
func checkBinary() Check {
	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		return Check{Name: "binary", Status: Warn, Detail: err.Error()}
	}
	for _, dir := range []string{"/usr/", "/opt/"} {
		if strings.HasPrefix(bin, dir) {
			return Check{Name: "binary", Status: OK, Detail: bin}
		}
	}
	return Check{Name: "binary", Status: Warn, Detail: bin + ": boot units run this path, which may move or be deleted",
		Fix: "install bmbox as /usr/local/bin/bmbox and run up again"}
}

// checkLabs reports labs that will not come back after a reboot.
func checkLabs(ws *workspace.Workspace) []Check {
	if ws == nil {
		return nil
	}
	labs, err := ws.Labs()
	if err != nil {
		return []Check{{Name: "labs", Status: Warn, Detail: err.Error()}}
	}
	var cs []Check
	for _, lab := range labs {
		name := "lab " + lab
		st, err := ws.LoadState(lab)
		if err != nil {
			cs = append(cs, Check{Name: name, Status: Warn, Detail: err.Error()})
			continue
		}
		if st == nil {
			continue
		}
		raw, err := ws.LoadTopology(lab)
		var t *topology.Topology
		if err == nil {
			t, err = topology.Parse(raw)
		}
		switch {
		case os.IsPermission(err):
			cs = append(cs, Check{Name: name, Status: Warn, Detail: "topology snapshot is readable by root only", Fix: "sudo bmbox doctor"})
		case err != nil:
			cs = append(cs, Check{Name: name, Status: Warn, Detail: "no usable topology snapshot: " + err.Error(), Fix: "sudo bmbox up -f <topology> again"})
		case !t.Spec.AutostartEnabled():
			cs = append(cs, Check{Name: name, Status: OK, Detail: "autostart off: bring it back with sudo bmbox up --lab " + lab + " after a reboot"})
		case !exists(filepath.Join(systemd.UnitDir, engine.BootUnit(lab))):
			cs = append(cs, Check{Name: name, Status: Warn, Detail: "will not come back after a reboot (no " + engine.BootUnit(lab) + ")",
				Fix: "sudo bmbox up --lab " + lab})
		default:
			cs = append(cs, Check{Name: name, Status: OK, Detail: "comes back at boot (" + engine.BootUnit(lab) + ")"})
		}
	}
	return cs
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
