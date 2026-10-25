package setup

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// fakeSystem has the commands in installed succeed and records the rest.
type fakeSystem struct {
	bins      map[string]bool
	installed map[string]bool
	files     map[string]bool
	versions  string
	ran       []string
}

func (f *fakeSystem) Run(env []string, name string, args ...string) error {
	f.ran = append(f.ran, name+" "+strings.Join(args, " "))
	return nil
}

func (f *fakeSystem) Check(name string, args ...string) (string, bool) {
	switch name {
	case "dpkg-query":
		if f.installed[args[len(args)-1]] {
			return "install ok installed", true
		}
		return "", false
	case "systemctl":
		return "", false
	}
	if strings.HasSuffix(name, "python3") {
		if strings.Contains(args[1], "importlib") {
			return f.versions, true
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

func (f *fakeSystem) Exists(path string) bool { return f.files[path] }

func TestSetupInstallsOnlyWhatIsMissing(t *testing.T) {
	sys := &fakeSystem{
		bins:      map[string]bool{"apt-get": true, "python3": true},
		installed: map[string]bool{},
		files:     map[string]bool{},
	}
	for _, p := range managers[0].Packages {
		sys.installed[p] = p != "dnsmasq-base" && p != "ipxe"
	}
	var out bytes.Buffer
	if _, err := Run(Options{Out: &out, System: sys, VenvDir: "/v"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"apt-get update",
		"apt-get install -y --no-install-recommends dnsmasq-base ipxe",
		"systemctl enable --now libvirtd.service",
		"/usr/bin/python3 -m venv --system-site-packages /v",
		"/v/bin/pip install --disable-pip-version-check sushy-tools==" + SushyToolsVersion + " virtualbmc==" + VirtualBMCVersion,
	}
	if strings.Join(sys.ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran:\n%s\nwant:\n%s", strings.Join(sys.ran, "\n"), strings.Join(want, "\n"))
	}
}

func TestSetupIsIdempotent(t *testing.T) {
	sys := &fakeSystem{
		bins:      map[string]bool{"apt-get": true, "python3": true},
		installed: map[string]bool{},
		files:     map[string]bool{"/v/bin/python3": true, "/run/libvirt/libvirt-sock": true},
		versions:  "sushy-tools==" + SushyToolsVersion + " virtualbmc==" + VirtualBMCVersion + " ",
	}
	for _, p := range managers[0].Packages {
		sys.installed[p] = true
	}
	var out bytes.Buffer
	tools, err := Run(Options{Out: &out, System: sys, VenvDir: "/v"})
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

func TestSetupDryRunChangesNothing(t *testing.T) {
	sys := &fakeSystem{bins: map[string]bool{"apt-get": true}, installed: map[string]bool{}, files: map[string]bool{}}
	var out bytes.Buffer
	if _, err := Run(Options{Out: &out, System: sys, VenvDir: "/v", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(sys.ran) != 0 {
		t.Errorf("dry run ran: %v", sys.ran)
	}
	if !strings.Contains(out.String(), "would run: apt-get install -y --no-install-recommends qemu-system-x86") {
		t.Errorf("dry run output:\n%s", out.String())
	}
}

func TestNoPackageManager(t *testing.T) {
	sys := &fakeSystem{bins: map[string]bool{}, files: map[string]bool{}}
	if _, err := Run(Options{Out: &bytes.Buffer{}, System: sys, VenvDir: "/v"}); err == nil || !strings.Contains(err.Error(), "--skip-packages") {
		t.Errorf("err = %v", err)
	}
}
