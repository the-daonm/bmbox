package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeSystem answers queries from its maps and records every change.
type fakeSystem struct {
	bins      map[string]bool
	installed map[string]bool
	files     map[string]string
	versions  string
	ran       []string
}

func newFake() *fakeSystem {
	return &fakeSystem{bins: map[string]bool{}, installed: map[string]bool{}, files: map[string]string{}}
}

func (f *fakeSystem) Run(env []string, name string, args ...string) error {
	f.ran = append(f.ran, name+" "+strings.Join(args, " "))
	return nil
}

func (f *fakeSystem) Check(name string, args ...string) (string, bool) {
	switch {
	case name == "dpkg-query":
		if f.installed[args[len(args)-1]] {
			return "install ok installed", true
		}
		return "", false
	case name == "systemctl":
		return "", false
	case name == "python3": // the host's interpreter: where libvirt is
		return "/usr/lib/python3/dist-packages/libvirt.py\n/usr/lib/python3/dist-packages/libvirtmod.so\n", true
	case strings.HasSuffix(name, "/bin/python3"): // the virtualenv's
		switch {
		case strings.Contains(args[1], "importlib"):
			return f.versions, true
		case strings.Contains(args[1], "sysconfig"):
			return "/v/lib/python3.10/site-packages\n", true
		}
		return "", true
	}
	return "", false
}

func (f *fakeSystem) LookPath(name string) (string, error) {
	if f.bins[name] {
		return "/usr/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (f *fakeSystem) Exists(path string) bool { _, ok := f.files[path]; return ok }

func (f *fakeSystem) ReadFile(path string) (string, error) {
	if c, ok := f.files[path]; ok {
		return c, nil
	}
	return "", errors.New("no such file")
}

func (f *fakeSystem) WriteFile(path, content string) error {
	f.ran = append(f.ran, "write "+path)
	f.files[path] = content
	return nil
}

func (f *fakeSystem) Symlink(target, link string) error {
	f.ran = append(f.ran, "ln -s "+target+" "+link)
	return nil
}

func (f *fakeSystem) RemoveAll(path string) error {
	f.ran = append(f.ran, "rm -rf "+path)
	for p := range f.files {
		if strings.HasPrefix(p, path) {
			delete(f.files, p)
		}
	}
	return nil
}

var pipInstall = "/v/bin/pip install --disable-pip-version-check sushy-tools==" + SushyToolsVersion + " virtualbmc==" + VirtualBMCVersion

func TestSetupInstallsOnlyWhatIsMissing(t *testing.T) {
	sys := newFake()
	sys.bins["apt-get"], sys.bins["python3"] = true, true
	for _, p := range managers[0].Packages {
		sys.installed[p] = p != "dnsmasq-base" && p != "ipxe"
	}
	if _, err := Run(Options{Out: &bytes.Buffer{}, System: sys, VenvDir: "/v", OwnsVenv: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"apt-get update",
		"apt-get install -y --no-install-recommends dnsmasq-base ipxe",
		"systemctl enable --now libvirtd.service",
		"/usr/bin/python3 -m venv /v", // isolated: no --system-site-packages
		"write /v/.bmbox-venv",
		"ln -s /usr/lib/python3/dist-packages/libvirt.py /v/lib/python3.10/site-packages/libvirt.py",
		"ln -s /usr/lib/python3/dist-packages/libvirtmod.so /v/lib/python3.10/site-packages/libvirtmod.so",
		pipInstall,
	}
	if strings.Join(sys.ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran:\n%s\nwant:\n%s", strings.Join(sys.ran, "\n"), strings.Join(want, "\n"))
	}
}

func TestSetupIsIdempotent(t *testing.T) {
	sys := newFake()
	sys.bins["apt-get"], sys.bins["python3"] = true, true
	for _, p := range managers[0].Packages {
		sys.installed[p] = true
	}
	for _, f := range []string{"/v/bin/python3", "/v/.bmbox-venv", "/run/libvirt/libvirt-sock",
		"/v/lib/python3.10/site-packages/libvirt.py", "/v/lib/python3.10/site-packages/libvirtmod.so"} {
		sys.files[f] = ""
	}
	sys.files["/v/pyvenv.cfg"] = "include-system-site-packages = false\n"
	sys.versions = "sushy-tools==" + SushyToolsVersion + " virtualbmc==" + VirtualBMCVersion + " "
	tools, err := Run(Options{Out: &bytes.Buffer{}, System: sys, VenvDir: "/v", OwnsVenv: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sys.ran) != 0 {
		t.Errorf("a prepared host ran: %v", sys.ran)
	}
	if tools.SushyEmulator != "/v/bin/sushy-emulator" || tools.VBMC != "/v/bin/vbmc" {
		t.Errorf("tools = %+v", tools)
	}
}

func TestVenvSeeingHostPackagesIsRecreated(t *testing.T) {
	sys := newFake()
	sys.bins["python3"] = true
	sys.files["/v/bin/python3"] = ""
	sys.files["/v/pyvenv.cfg"] = "home = /usr/bin\ninclude-system-site-packages = true\n"
	opt := Options{Out: &bytes.Buffer{}, System: sys, VenvDir: "/v", SkipPackages: true}

	// A virtualenv bmbox did not create is never deleted.
	if _, err := Run(opt); err == nil || !strings.Contains(err.Error(), "host's Python packages") {
		t.Fatalf("foreign venv: %v", err)
	}
	opt.OwnsVenv = true
	sys.ran = nil
	if _, err := Run(opt); err != nil {
		t.Fatal(err)
	}
	if len(sys.ran) < 2 || sys.ran[0] != "systemctl enable --now libvirtd.service" || sys.ran[1] != "rm -rf /v" || sys.ran[2] != "/usr/bin/python3 -m venv /v" {
		t.Errorf("ran: %v", sys.ran)
	}
}

func TestSetupDryRunChangesNothing(t *testing.T) {
	sys := newFake()
	sys.bins["apt-get"] = true
	var out bytes.Buffer
	if _, err := Run(Options{Out: &out, System: sys, VenvDir: "/v", DryRun: true, OwnsVenv: true}); err != nil {
		t.Fatal(err)
	}
	if len(sys.ran) != 0 {
		t.Errorf("dry run ran: %v", sys.ran)
	}
	for _, want := range []string{"would run: apt-get install -y --no-install-recommends qemu-system-x86", "would run: python3 -m venv /v", "link the distribution's libvirt binding"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestNoPackageManager(t *testing.T) {
	sys := newFake()
	if _, err := Run(Options{Out: &bytes.Buffer{}, System: sys, VenvDir: "/v"}); err == nil || !strings.Contains(err.Error(), "--skip-packages") {
		t.Errorf("err = %v", err)
	}
}
