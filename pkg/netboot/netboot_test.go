package netboot

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSpec(dir string, boot *Boot) Spec {
	return Spec{
		Lab: "lab1", Unit: "bmbox-lab1-dnsmasq.service", Dir: dir,
		Networks: []Network{{
			Name: "pxe", Bridge: "bmb-lab1-pxe",
			Gateway: netip.MustParsePrefix("10.20.0.1/24"),
			First:   netip.MustParseAddr("10.20.0.2"), Last: netip.MustParseAddr("10.20.0.254"),
		}},
		Hosts: []Host{
			{MAC: "52:54:00:aa:00:01", Name: "n1", IP: netip.MustParseAddr("10.20.0.10")},
			{MAC: "52:54:00:aa:00:02", Name: "n2"},
		},
		Boot: boot,
	}
}

func TestConfig(t *testing.T) {
	cfg := Config(testSpec("/var/lib/bmbox/lab1", &Boot{Kernel: "/k"}))
	for _, want := range []string{
		"port=0\n", "bind-interfaces\n", "interface=bmb-lab1-pxe\n",
		"dhcp-leasefile=/var/lib/bmbox/lab1/dnsmasq.leases\n",
		"dhcp-range=set:pxe,10.20.0.2,10.20.0.254,255.255.255.0,1h\n",
		"dhcp-host=52:54:00:aa:00:01,10.20.0.10,n1\n",
		"dhcp-host=52:54:00:aa:00:02,n2\n",
		"tftp-root=/var/lib/bmbox/lab1/tftp\n",
		"dhcp-boot=tag:ipxe,boot.ipxe\n",
		"dhcp-boot=tag:!ipxe,tag:efi64,ipxe.efi\n",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if cfg := Config(testSpec("/d", nil)); strings.Contains(cfg, "dhcp-boot") || !strings.Contains(cfg, "enable-tftp") {
		t.Errorf("addresses-only config:\n%s", cfg)
	}
	if m := prefixMask(20); m != "255.255.240.0" {
		t.Errorf("prefixMask(20) = %s", m)
	}
}

func TestScript(t *testing.T) {
	s := string(Script("lab1", Boot{Kernel: "/boot/vmlinuz", Initrd: "/boot/initrd.img", Cmdline: "console=ttyS0"}))
	if !strings.Contains(s, "kernel vmlinuz initrd=initrd console=ttyS0\ninitrd initrd\nboot\n") || !strings.HasPrefix(s, "#!ipxe\n") {
		t.Errorf("local kernel script:\n%s", s)
	}
	s = string(Script("lab1", Boot{Kernel: "http://h/k", Initrd: "http://h/x/initramfs.gz"}))
	if !strings.Contains(s, "kernel http://h/k initrd=initramfs.gz\ninitrd http://h/x/initramfs.gz\n") {
		t.Errorf("URL kernel script:\n%s", s)
	}
	s = string(Script("lab1", Boot{Kernel: "/k"}))
	if !strings.Contains(s, "kernel vmlinuz\nboot\n") {
		t.Errorf("kernel-only script:\n%s", s)
	}
	if s := string(Script("lab1", Boot{Script: "https://h/menu.ipxe"})); !strings.Contains(s, "chain https://h/menu.ipxe\n") {
		t.Errorf("chain script:\n%s", s)
	}
}

func TestSyncTFTP(t *testing.T) {
	src := t.TempDir()
	root := filepath.Join(t.TempDir(), "tftp")
	write := func(name, data string) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ipxe, kernel := write("snponly.efi", "IPXE"), write("vmlinuz", "KERNEL")
	spec := testSpec(filepath.Dir(root), &Boot{Kernel: kernel, Initrd: "http://h/initrd"})
	if err := syncTFTP(root, tftpFiles(spec, ipxe)); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(root, e.Name()))
		got[e.Name()] = string(b)
		if fi, _ := e.Info(); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode %v, want world-readable", e.Name(), fi.Mode())
		}
	}
	if len(got) != 3 || got[IPXEName] != "IPXE" || got[KernelName] != "KERNEL" || !strings.Contains(got[ScriptName], "initrd http://h/initrd") {
		t.Errorf("tftp root = %v", got)
	}

	// Unchanged source is not copied again; stale files are removed.
	stamp := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(root, KernelName), stamp, stamp)
	_ = os.WriteFile(filepath.Join(root, "stale"), nil, 0o644)
	spec.Boot = &Boot{Script: "http://h/x.ipxe"}
	if err := syncTFTP(root, tftpFiles(spec, ipxe)); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(root)
	if len(entries) != 2 {
		t.Errorf("after switching to a chain script, root has %d entries, want ipxe.efi and boot.ipxe", len(entries))
	}
}

func TestReadLeases(t *testing.T) {
	p := filepath.Join(t.TempDir(), "leases")
	_ = os.WriteFile(p, []byte("1760000000 52:54:00:AA:00:01 10.20.0.10 n1 01:52:54:00:aa:00:01\n"+
		"1760000100 52:54:00:aa:00:02 10.20.0.77 * *\nduid 00:01\n"), 0o644)
	l, err := ReadLeases(p)
	if err != nil || len(l) != 2 {
		t.Fatalf("leases = %+v, %v", l, err)
	}
	if l[0].MAC != "52:54:00:aa:00:01" || l[0].IP != "10.20.0.10" || l[0].Hostname != "n1" || l[1].Hostname != "" {
		t.Errorf("leases = %+v", l)
	}
	if l, err := ReadLeases(filepath.Join(t.TempDir(), "none")); err != nil || l != nil {
		t.Errorf("missing file: %v %v", l, err)
	}
}

func TestProbeTFTP(t *testing.T) {
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	addr := srv.LocalAddr().(*net.UDPAddr).AddrPort()

	go func() {
		buf := make([]byte, 512)
		n, from, err := srv.ReadFromUDPAddrPort(buf)
		if err != nil || !strings.HasPrefix(string(buf[2:n]), "boot.ipxe\x00octet\x00") {
			return
		}
		_, _ = srv.WriteToUDPAddrPort([]byte{0, 5, 0, 1, 'n', 'o', 0}, from) // file not found
	}()
	if err := ProbeTFTP(addr, "boot.ipxe", 2*time.Second); err != nil {
		t.Errorf("probe of a live server: %v", err)
	}
	if err := ProbeTFTP(addr, "boot.ipxe", 200*time.Millisecond); err == nil {
		t.Error("probe without an answer succeeded")
	}
}

func TestFindTools(t *testing.T) {
	dir := t.TempDir()
	dnsmasq, ipxe := filepath.Join(dir, "dnsmasq"), filepath.Join(dir, "snponly.efi")
	_ = os.WriteFile(dnsmasq, nil, 0o755)
	_ = os.WriteFile(ipxe, nil, 0o644)
	t.Setenv(EnvIPXE, ipxe)
	tools, err := FindTools(dnsmasq, "", true)
	if err != nil || tools.Dnsmasq != dnsmasq || tools.IPXE != ipxe {
		t.Errorf("FindTools = %+v, %v", tools, err)
	}
}
