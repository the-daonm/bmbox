// Package setup prepares a host for bmbox: the distribution packages it
// needs (QEMU, libvirt, OVMF, dnsmasq, iPXE, ipmitool), a Python
// virtualenv with the BMC emulators at tested versions, and the tool paths
// `up` uses.
package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/the-daonm/bmbox/pkg/netboot"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

// The BMC emulators bmbox is tested with (scripts/e2e.sh). The IPMI
// launcher uses virtualbmc's VirtualBMC class directly, so other versions
// may break it: change these only together with an e2e run.
const (
	SushyToolsVersion = "2.2.0"
	VirtualBMCVersion = "3.3.0"
)

// PackageManager installs distribution packages.
type PackageManager struct {
	Name string
	// Packages bmbox needs. libvirt's Python binding comes from the
	// distribution: the virtualenv sees it (--system-site-packages)
	// instead of compiling it against libvirt's headers.
	Packages []string
	// installed returns the command that succeeds when pkg is installed.
	installed func(pkg string) []string
	// install returns the commands that install pkgs.
	install func(pkgs []string) [][]string
}

var managers = []PackageManager{
	{
		Name: "apt-get",
		Packages: []string{"qemu-system-x86", "qemu-utils", "libvirt-daemon-system", "ovmf",
			"dnsmasq-base", "ipxe", "python3-venv", "python3-libvirt", "ipmitool", "iptables"},
		installed: func(p string) []string { return []string{"dpkg-query", "-W", "-f=${Status}", p} },
		install: func(pkgs []string) [][]string {
			return [][]string{
				{"apt-get", "update"},
				append([]string{"apt-get", "install", "-y", "--no-install-recommends"}, pkgs...),
			}
		},
	},
	{
		Name: "dnf",
		Packages: []string{"qemu-kvm", "libvirt", "edk2-ovmf", "dnsmasq", "ipxe-bootimgs-x86",
			"python3", "python3-libvirt", "ipmitool", "iptables"},
		installed: func(p string) []string { return []string{"rpm", "-q", p} },
		install: func(pkgs []string) [][]string {
			return [][]string{append([]string{"dnf", "install", "-y"}, pkgs...)}
		},
	},
	{
		Name: "pacman",
		Packages: []string{"qemu-base", "libvirt", "edk2-ovmf", "dnsmasq", "ipxe", "python",
			"libvirt-python", "ipmitool", "iptables"},
		installed: func(p string) []string { return []string{"pacman", "-Q", p} },
		install: func(pkgs []string) [][]string {
			return [][]string{append([]string{"pacman", "-S", "--needed", "--noconfirm"}, pkgs...)}
		},
	},
}

// System runs and queries the host. Tests replace it.
type System interface {
	// Run runs a command that changes the host, showing its output.
	Run(env []string, name string, args ...string) error
	// Check runs a read-only command and reports whether it succeeded,
	// with its output.
	Check(name string, args ...string) (string, bool)
	LookPath(name string) (string, error)
	Exists(path string) bool
}

type Options struct {
	Out    io.Writer
	System System
	// DryRun prints what would be done without changing anything.
	DryRun bool
	// SkipPackages leaves distribution packages alone.
	SkipPackages bool
	// VenvDir is the virtualenv for sushy-tools and virtualbmc.
	VenvDir string
	// Dnsmasq and IPXE are where those are, when not in a distribution
	// path (given before, or with --ipxe).
	Dnsmasq string
	IPXE    string
}

// DefaultVenvDir is the virtualenv in a workspace.
func DefaultVenvDir(ws *workspace.Workspace) string { return filepath.Join(ws.Root, "tools", "venv") }

// Run prepares the host and returns the tool paths to remember.
func Run(opt Options) (workspace.Tools, error) {
	log := func(format string, args ...any) { fmt.Fprintf(opt.Out, format+"\n", args...) }
	var tools workspace.Tools

	log("==> Packages")
	if opt.SkipPackages {
		log("    skipped (--skip-packages)")
	} else if err := installPackages(opt, log); err != nil {
		return tools, err
	}

	log("==> libvirt")
	if err := ensureLibvirt(opt, log); err != nil {
		return tools, err
	}

	log("==> BMC emulators (sushy-tools %s, virtualbmc %s)", SushyToolsVersion, VirtualBMCVersion)
	if err := ensureVenv(opt, log); err != nil {
		return tools, err
	}
	tools.SushyEmulator = filepath.Join(opt.VenvDir, "bin", "sushy-emulator")
	tools.VBMC = filepath.Join(opt.VenvDir, "bin", "vbmc")

	log("==> Netboot")
	if nt, err := netboot.FindTools(opt.Dnsmasq, opt.IPXE, true); err == nil {
		tools.Dnsmasq, tools.IPXE = nt.Dnsmasq, nt.IPXE
		log("    dnsmasq    %s", nt.Dnsmasq)
		log("    ipxe       %s", nt.IPXE)
	} else if nt.Dnsmasq != "" {
		tools.Dnsmasq = nt.Dnsmasq
		log("    dnsmasq    %s", nt.Dnsmasq)
		log("    warning: %v", err)
	} else if opt.DryRun {
		log("    dnsmasq and iPXE are looked up once the packages are installed")
	} else {
		log("    warning: %v", err)
	}
	return tools, nil
}

func detectManager(sys System) (*PackageManager, error) {
	for i := range managers {
		if _, err := sys.LookPath(managers[i].Name); err == nil {
			return &managers[i], nil
		}
	}
	return nil, fmt.Errorf("no supported package manager (apt-get, dnf, pacman): install QEMU, libvirt, OVMF, dnsmasq, iPXE, python3-venv, python3-libvirt and ipmitool, then run setup --skip-packages")
}

func installPackages(opt Options, log func(string, ...any)) error {
	pm, err := detectManager(opt.System)
	if err != nil {
		return err
	}
	var missing []string
	for _, p := range pm.Packages {
		args := pm.installed(p)
		out, ok := opt.System.Check(args[0], args[1:]...)
		if pm.Name == "apt-get" {
			ok = ok && strings.Contains(out, "install ok installed")
		}
		if !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		log("    %s: all %d packages installed", pm.Name, len(pm.Packages))
		return nil
	}
	log("    %s: installing %s", pm.Name, strings.Join(missing, " "))
	for _, c := range pm.install(missing) {
		if err := run(opt, []string{"DEBIAN_FRONTEND=noninteractive"}, c...); err != nil {
			return fmt.Errorf("install packages: %w", err)
		}
	}
	return nil
}

// ensureLibvirt starts libvirt when its socket is missing: the modular
// virtqemud where the distribution ships it, else libvirtd.
func ensureLibvirt(opt Options, log func(string, ...any)) error {
	for _, sock := range []string{"/run/libvirt/virtqemud-sock", "/run/libvirt/libvirt-sock"} {
		if opt.System.Exists(sock) {
			log("    running (%s)", sock)
			return nil
		}
	}
	unit := "libvirtd.service"
	if _, ok := opt.System.Check("systemctl", "cat", "virtqemud.socket"); ok {
		unit = "virtqemud.socket"
	}
	log("    starting %s", unit)
	return run(opt, nil, "systemctl", "enable", "--now", unit)
}

func ensureVenv(opt Options, log func(string, ...any)) error {
	sys := opt.System
	py := filepath.Join(opt.VenvDir, "bin", "python3")
	if !sys.Exists(py) {
		python, err := sys.LookPath("python3")
		if err != nil && !opt.DryRun {
			return fmt.Errorf("python3 not found (install python3 and its venv module)")
		}
		if python == "" {
			python = "python3"
		}
		log("    create virtualenv %s", opt.VenvDir)
		if err := run(opt, nil, python, "-m", "venv", "--system-site-packages", opt.VenvDir); err != nil {
			return fmt.Errorf("create virtualenv: %w", err)
		}
	}
	want := "sushy-tools==" + SushyToolsVersion + " virtualbmc==" + VirtualBMCVersion
	have, _ := sys.Check(py, "-c", `import importlib.metadata as m
for p in ("sushy-tools", "virtualbmc"):
    try:
        print(p + "==" + m.version(p), end=" ")
    except m.PackageNotFoundError:
        pass`)
	if strings.TrimSpace(have) == want {
		log("    up to date in %s", opt.VenvDir)
	} else {
		log("    pip install %s", want)
		if err := run(opt, nil, filepath.Join(opt.VenvDir, "bin", "pip"), "install", "--disable-pip-version-check",
			"sushy-tools=="+SushyToolsVersion, "virtualbmc=="+VirtualBMCVersion); err != nil {
			return fmt.Errorf("install BMC emulators: %w (if libvirt-python failed to build, install your distribution's python3-libvirt)", err)
		}
	}
	if opt.DryRun {
		return nil
	}
	if out, ok := sys.Check(py, "-c", "import libvirt, sushy_tools, virtualbmc"); !ok {
		return fmt.Errorf("the virtualenv cannot import libvirt, sushy_tools and virtualbmc: %s (is python3-libvirt installed?)", strings.TrimSpace(out))
	}
	return nil
}

func run(opt Options, env []string, args ...string) error {
	if opt.DryRun {
		fmt.Fprintf(opt.Out, "    would run: %s\n", strings.Join(args, " "))
		return nil
	}
	return opt.System.Run(env, args[0], args[1:]...)
}

// Host is the real System.
type Host struct {
	Out io.Writer
}

func (h Host) Run(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = h.Out, h.Out
	return cmd.Run()
}

func (Host) Check(name string, args ...string) (string, bool) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err == nil
}

func (Host) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (Host) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
