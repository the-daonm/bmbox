package engine

import (
	"strings"
	"testing"

	"github.com/the-daonm/bmbox/pkg/workspace"
)

func TestBootUnitContent(t *testing.T) {
	ws := &workspace.Workspace{Root: workspace.SystemRoot}
	got := bootUnitContent("demo", "/usr/local/bin/bmbox", "qemu:///system", ws)
	for _, want := range []string{
		"Description=bmbox boot for demo\n",
		"Before=libvirt-guests.service\n",
		"After=network-online.target libvirtd.service",
		"Type=oneshot\n",
		"ExecStart=/usr/local/bin/bmbox up --lab demo --connect qemu:///system\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BMBOX_HOME") {
		t.Error("default workspace must not set BMBOX_HOME")
	}

	other := bootUnitContent("demo", "/opt/my tools/bmbox", "qemu:///system", &workspace.Workspace{Root: "/srv/bmbox"})
	for _, want := range []string{`Environment=BMBOX_HOME=/srv/bmbox`, `ExecStart="/opt/my tools/bmbox" up`} {
		if !strings.Contains(other, want) {
			t.Errorf("missing %q in:\n%s", want, other)
		}
	}
}
