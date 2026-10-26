package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/the-daonm/bmbox/pkg/systemd"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

func TestVersionCheck(t *testing.T) {
	dir := t.TempDir()
	py := filepath.Join(dir, "python")
	os.WriteFile(py, []byte("#!/bin/sh\ncase \"$2\" in import\\ libvirt*) exit 0;; esac\necho 2.2.0\n"), 0o755)
	if c := versionCheck("redfish BMC", "sushy-tools", "m", py, "2.2.0"); c.Status != OK {
		t.Errorf("same version: %+v", c)
	}
	if c := versionCheck("redfish BMC", "sushy-tools", "m", py, "9.9.9"); c.Status != Warn {
		t.Errorf("other version: %+v", c)
	}
	os.WriteFile(py, []byte("#!/bin/sh\necho 'PackageNotFoundError: sushy-tools' >&2\nexit 1\n"), 0o755)
	if c := versionCheck("redfish BMC", "sushy-tools", "m", py, "2.2.0"); c.Status != Fail {
		t.Errorf("missing package: %+v", c)
	}
	// Installed, but the module does not import.
	os.WriteFile(py, []byte("#!/bin/sh\ncase \"$2\" in import\\ libvirt*) echo \"TypeError: 'ABCMeta' object is not subscriptable\" >&2; exit 1;; esac\necho 2.2.0\n"), 0o755)
	if c := versionCheck("redfish BMC", "sushy-tools", "m", py, "2.2.0"); c.Status != Fail || !strings.Contains(c.Detail, "does not load") {
		t.Errorf("broken import: %+v", c)
	}
}

func TestLabsWithoutBootUnitWarn(t *testing.T) {
	old := systemd.UnitDir
	systemd.UnitDir = t.TempDir()
	defer func() { systemd.UnitDir = old }()

	t.Setenv("BMBOX_HOME", t.TempDir())
	ws, _ := workspace.Open()
	for _, lab := range []string{"up", "down"} {
		ws.SaveTopology(lab, []byte(fmt.Sprintf("apiVersion: bmbox.io/v1alpha1\nkind: Topology\nmetadata: {name: %s}\nspec: {nodes: [{name: n1, boot: [disk]}]}\n", lab)))
		ws.SaveState(&workspace.LabState{Lab: lab})
	}
	os.WriteFile(filepath.Join(systemd.UnitDir, "bmbox-up-boot.service"), nil, 0o644)

	got := map[string]Check{}
	for _, c := range checkLabs(ws) {
		got[c.Name] = c
	}
	if got["lab up"].Status != OK || got["lab down"].Status != Warn {
		t.Errorf("checks = %+v", got)
	}
}

func TestIPXEUsesRememberedDnsmasq(t *testing.T) {
	dir := t.TempDir()
	dnsmasq := filepath.Join(dir, "dnsmasq")
	ipxe := filepath.Join(dir, "snponly.efi")
	for _, p := range []string{dnsmasq, ipxe} {
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := checkIPXE(dnsmasq, ipxe)
	if c.Status != OK || c.Detail != ipxe {
		t.Fatalf("checkIPXE = %+v, want OK with %s", c, ipxe)
	}
}
