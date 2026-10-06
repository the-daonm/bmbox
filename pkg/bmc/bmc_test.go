package bmc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type dirWriter struct{ secrets map[string]bool }

func (w *dirWriter) WriteFile(p string, b []byte) error { return os.WriteFile(p, b, 0o644) }
func (w *dirWriter) WriteSecret(p string, b []byte) error {
	w.secrets[filepath.Base(p)] = true
	return os.WriteFile(p, b, 0o600)
}

func testSpec(t *testing.T, typ string) Spec {
	return Spec{
		Lab: "lab1", Node: "n1", Domain: "bmbox-lab1-n1", UUID: "1234",
		Type: typ, Address: "127.0.0.1", Port: 8300, Username: "admin", Password: "s3cret",
		LibvirtURI: "qemu:///system", Loader: "/fw/CODE.fd",
		Unit: "bmbox-lab1-n1-bmc.service", Dir: t.TempDir(),
	}
}

func TestRedfishConfigIsScopedAndStable(t *testing.T) {
	s := testSpec(t, TypeRedfish)
	w := &dirWriter{secrets: map[string]bool{}}
	cmd, changed, err := writeRedfish(s, w, Tools{SushyEmulator: "/bin/sushy-emulator"})
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	if strings.Join(cmd, " ") != "/bin/sushy-emulator --config "+filepath.Join(s.Dir, "sushy.conf") {
		t.Errorf("cmd = %v", cmd)
	}
	cfg, _ := os.ReadFile(filepath.Join(s.Dir, "sushy.conf"))
	for _, want := range []string{
		`SUSHY_EMULATOR_ALLOWED_INSTANCES = ["1234","bmbox-lab1-n1"]`,
		`SUSHY_EMULATOR_LISTEN_PORT = 8300`,
		`"x86_64": "/fw/CODE.fd"`,
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config missing %s:\n%s", want, cfg)
		}
	}
	if !w.secrets["htpasswd"] || !htpasswdMatches(filepath.Join(s.Dir, "htpasswd"), "admin", "s3cret") {
		t.Error("htpasswd not written as a secret with a matching bcrypt hash")
	}

	if _, changed, _ := writeRedfish(s, w, Tools{}); changed {
		t.Error("second write with same spec reported a change")
	}
	s.Password = "other"
	if _, changed, _ := writeRedfish(s, w, Tools{}); !changed {
		t.Error("password change not detected")
	}
}

func TestIPMIConfigIsSecret(t *testing.T) {
	s := testSpec(t, TypeIPMI)
	w := &dirWriter{secrets: map[string]bool{}}
	cmd, changed, err := writeIPMI(s, w, Tools{VBMCPython: "/venv/bin/python3"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if cmd[0] != "/venv/bin/python3" || !w.secrets["ipmi.json"] {
		t.Errorf("cmd=%v secrets=%v", cmd, w.secrets)
	}
	if _, changed, _ := writeIPMI(s, w, Tools{VBMCPython: "/venv/bin/python3"}); changed {
		t.Error("second write reported a change")
	}
}

func TestInterpreterOf(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vbmc")
	os.WriteFile(p, []byte("#!/home/u/venv/bin/python3\nimport sys\n"), 0o755)
	if got, err := interpreterOf(p); err != nil || got != "/home/u/venv/bin/python3" {
		t.Errorf("interpreterOf = %q, %v", got, err)
	}
}
