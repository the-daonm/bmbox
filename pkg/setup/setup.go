// Package setup prepares a host for bmbox: the distribution packages it
// needs (QEMU, libvirt, OVMF, dnsmasq, iPXE, ipmitool), a Python
// virtualenv with the BMC emulators at tested versions, and the tool paths
// `up` uses.
package setup

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/the-daonm/bmbox/pkg/netboot"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

// requirements pins the BMC emulators bmbox is tested with (see the file).
//
//go:embed requirements.txt
var requirements string

// The pinned versions, from requirements.txt.
var (
	SushyToolsVersion = pinned("sushy-tools")
	VirtualBMCVersion = pinned("virtualbmc")
)

// pinned returns the version requirements.txt pins pkg to.
func pinned(pkg string) string {
	for _, line := range strings.Split(requirements, "\n") {
		name, ver, ok := strings.Cut(strings.TrimSpace(line), "==")
		if ok && strings.EqualFold(strings.TrimSpace(name), pkg) {
			return strings.TrimSpace(ver)
		}
	}
	panic("requirements.txt does not pin " + pkg)
}

// PackageManager installs distribution packages.
type PackageManager struct {
	Name string
	// Packages bmbox needs. libvirt's Python binding comes from the
	// distribution and is linked into the virtualenv (see linkLibvirt)
	// instead of being compiled against libvirt's headers.
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
		// --whatprovides: "iptables" is installed as iptables-nft.
		installed: func(p string) []string { return []string{"rpm", "-q", "--whatprovides", p} },
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
	ReadFile(path string) (string, error)
	WriteFile(path, content string) error
	Symlink(target, link string) error
	RemoveAll(path string) error
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
	// OwnsVenv says VenvDir is bmbox's own (the default), which setup may
	// recreate even without the marker of older versions.
	OwnsVenv bool
	// Dnsmasq and IPXE are where those are, when not in a distribution
	// path (given before, or with --ipxe).
	Dnsmasq string
	IPXE    string
}

// venvMarker marks a virtualenv created by bmbox, which setup may delete
// and create again.
const venvMarker = ".bmbox-venv"

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
	// Without systemd (a container, say), libvirt is not ours to start.
	if !opt.System.Exists("/run/systemd/system") {
		log("    warning: libvirt is not running and systemd is not either: start libvirt yourself")
		return nil
	}
	unit := "libvirtd.service"
	if _, ok := opt.System.Check("systemctl", "cat", "virtqemud.socket"); ok {
		unit = "virtqemud.socket"
	}
	log("    starting %s", unit)
	return run(opt, nil, "systemctl", "enable", "--now", unit)
}

// ensureVenv makes an isolated virtualenv with the BMC emulators. Host
// packages must stay out of it: a host-wide pip install (an openstacksdk
// in /usr/local, say) would shadow the versions sushy-tools needs.
func ensureVenv(opt Options, log func(string, ...any)) error {
	sys := opt.System
	py := filepath.Join(opt.VenvDir, "bin", "python3")
	if cfg, err := sys.ReadFile(filepath.Join(opt.VenvDir, "pyvenv.cfg")); err == nil &&
		strings.Contains(strings.ReplaceAll(cfg, " ", ""), "include-system-site-packages=true") {
		if !sys.Exists(filepath.Join(opt.VenvDir, venvMarker)) && !opt.OwnsVenv {
			return fmt.Errorf("%s sees the host's Python packages (include-system-site-packages): remove it or choose another --venv", opt.VenvDir)
		}
		log("    recreate virtualenv %s without the host's packages", opt.VenvDir)
		if err := runf(opt, func() error { return sys.RemoveAll(opt.VenvDir) }, "rm -rf "+opt.VenvDir); err != nil {
			return err
		}
	}
	if !sys.Exists(py) || opt.DryRun && !sys.Exists(opt.VenvDir) {
		python, err := sys.LookPath("python3")
		if err != nil && !opt.DryRun {
			return fmt.Errorf("python3 not found (install python3 and its venv module)")
		}
		if python == "" {
			python = "python3"
		}
		log("    create virtualenv %s", opt.VenvDir)
		if err := run(opt, nil, python, "-m", "venv", opt.VenvDir); err != nil {
			return fmt.Errorf("create virtualenv: %w", err)
		}
		if err := runf(opt, func() error { return sys.WriteFile(filepath.Join(opt.VenvDir, venvMarker), "created by bmbox setup\n") }, "mark "+opt.VenvDir); err != nil {
			return err
		}
	}
	if err := linkLibvirt(opt, py, log); err != nil {
		return err
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
			return fmt.Errorf("install BMC emulators: %w", err)
		}
	}
	if opt.DryRun {
		return nil
	}
	if out, ok := sys.Check(py, "-c", ImportCheck); !ok {
		return fmt.Errorf("the BMC emulators do not load in %s: %s", opt.VenvDir, lastLine(out))
	}
	return nil
}

// ImportCheck loads the modules the BMC units run, as they would.
const ImportCheck = "import libvirt, sushy_tools.emulator.main, virtualbmc.vbmc"

// linkLibvirt links the distribution's libvirt binding (libvirt*.py, its
// compiled modules and metadata) into the virtualenv, so it is used, and
// pip sees libvirt-python installed, without exposing other host packages.
func linkLibvirt(opt Options, py string, log func(string, ...any)) error {
	sys := opt.System
	if opt.DryRun && !sys.Exists(py) {
		log("    link the distribution's libvirt binding into the virtualenv")
		return nil
	}
	out, ok := sys.Check("python3", "-c", `import glob, os, libvirt
d = os.path.dirname(libvirt.__file__)
print("\n".join(sorted(glob.glob(os.path.join(d, "libvirt*")))))`)
	if !ok {
		return fmt.Errorf("the host's python3 cannot import libvirt: install python3-libvirt (libvirt-python on Arch): %s", lastLine(out))
	}
	site, ok := sys.Check(py, "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])")
	if !ok {
		return fmt.Errorf("find the virtualenv's site-packages: %s", lastLine(site))
	}
	site = strings.TrimSpace(site)
	linked := 0
	for _, src := range strings.Fields(out) {
		dst := filepath.Join(site, filepath.Base(src))
		if sys.Exists(dst) {
			continue
		}
		if err := runf(opt, func() error { return sys.Symlink(src, dst) }, "ln -s "+src+" "+dst); err != nil {
			return fmt.Errorf("link libvirt binding: %w", err)
		}
		linked++
	}
	switch {
	case linked > 0 && opt.DryRun:
		log("    would link the distribution's libvirt binding (%d files)", linked)
	case linked > 0:
		log("    linked the distribution's libvirt binding (%d files)", linked)
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// runf performs a change through the System, or prints it on a dry run.
func runf(opt Options, f func() error, what string) error {
	if opt.DryRun {
		fmt.Fprintf(opt.Out, "    would run: %s\n", what)
		return nil
	}
	return f()
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
	_, err := os.Lstat(path)
	return err == nil
}

func (Host) ReadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func (Host) WriteFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }
func (Host) Symlink(target, link string) error    { return os.Symlink(target, link) }
func (Host) RemoveAll(path string) error          { return os.RemoveAll(path) }
