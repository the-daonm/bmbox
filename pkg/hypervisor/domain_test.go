package hypervisor

import (
	"testing"

	"libvirt.org/go/libvirtxml"
)

func TestBuildDomainXML(t *testing.T) {
	spec := NodeSpec{
		Lab: "lab1", Node: "n1", DomainName: "bmbox-lab1-n1",
		CPUs: 2, MemoryBytes: 4 << 30,
		Disks:        []string{"/p/n1-disk0.qcow2", "/p/n1-disk1.qcow2"},
		NICs:         []NICSpec{{MAC: "52:54:00:00:00:01", Bridge: "br0"}, {MAC: "52:54:00:00:00:02", Bridge: "br1"}},
		Boot:         []string{"network", "disk"},
		Loader:       "/fw/CODE.fd",
		NVRAM:        "/p/n1-VARS.fd",
		VarsTemplate: "/fw/VARS.fd",
		SerialSocket: "/run/bmbox/lab1/n1.serial.sock",
	}
	raw, err := BuildDomainXML(spec)
	if err != nil {
		t.Fatal(err)
	}
	var d libvirtxml.Domain
	if err := d.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}

	if d.OS.Type.Machine != "q35" || d.OS.Loader.Type != "pflash" || d.OS.Loader.Readonly != "yes" {
		t.Errorf("unexpected firmware setup: %+v %+v", d.OS.Type, d.OS.Loader)
	}
	if d.OS.NVRam.NVRam != spec.NVRAM || d.OS.NVRam.Template != spec.VarsTemplate {
		t.Errorf("nvram = %+v", d.OS.NVRam)
	}

	// network devices boot first, then disks, in device order
	want := map[string]uint{"nic0": 1, "nic1": 2, "disk0": 3, "disk1": 4}
	got := map[string]uint{}
	for i, n := range d.Devices.Interfaces {
		got["nic"+string(rune('0'+i))] = n.Boot.Order
	}
	for i, dk := range d.Devices.Disks {
		got["disk"+string(rune('0'+i))] = dk.Boot.Order
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s boot order = %d, want %d", k, got[k], v)
		}
	}

	s := d.Devices.Serials[0]
	if s.Source.UNIX == nil || s.Source.UNIX.Path != spec.SerialSocket || s.Source.UNIX.Mode != "bind" {
		t.Errorf("serial = %+v", s.Source)
	}
	if d.Metadata == nil || d.Metadata.XML == "" {
		t.Error("ownership metadata missing")
	}
}

func TestPickLoaderSkipsSecureBoot(t *testing.T) {
	got := pickLoader([]string{
		"/usr/share/OVMF/OVMF_CODE_4M.secboot.fd",
		"/usr/share/OVMF/OVMF_CODE.fd",
		"/usr/share/OVMF/OVMF_CODE_4M.ms.fd",
		"/usr/share/OVMF/OVMF_CODE_4M.fd",
	})
	if got != "/usr/share/OVMF/OVMF_CODE_4M.fd" {
		t.Errorf("pickLoader = %q", got)
	}
	if v := varsCandidates(got); len(v) == 0 || v[0] != "/usr/share/OVMF/OVMF_VARS_4M.fd" {
		t.Errorf("varsCandidates = %v", v)
	}
}
