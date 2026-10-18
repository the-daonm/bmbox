package topology

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultDHCPRange(t *testing.T) {
	for _, c := range []struct{ cidr, want string }{
		{"10.0.0.0/24", "10.0.0.2-10.0.0.254"},
		{"10.0.0.254/24", "10.0.0.1-10.0.0.253"},
		{"10.0.0.100/24", "10.0.0.101-10.0.0.254"},
		{"10.0.0.0/30", "10.0.0.2-10.0.0.2"},
	} {
		first, last, ok := defaultDHCPRange(c.cidr)
		if got := first.String() + "-" + last.String(); !ok || got != c.want {
			t.Errorf("%s: got %s (ok=%v), want %s", c.cidr, got, ok, c.want)
		}
	}
}

func TestDHCPAndNetbootParse(t *testing.T) {
	topo, err := Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks:
    - {name: pxe, cidr: 10.20.0.0/24, dhcp: {}}
    - {name: data}
  netboot: {kernel: /srv/vmlinuz, initrd: "http://10.20.0.1/initrd", cmdline: console=ttyS0}
  nodes:
    - {name: n1, nics: [{network: pxe, ip: 10.20.0.10}, {network: data}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := topo.Spec.Networks[0].DHCP.Range; got != "10.20.0.2-10.20.0.254" {
		t.Errorf("default range = %q", got)
	}
	if n := topo.DHCPNetworks(); len(n) != 1 || n[0].Name != "pxe" {
		t.Errorf("DHCPNetworks = %+v", n)
	}
	if got := topo.DHCPUnit(); got != "bmbox-lab1-dnsmasq.service" {
		t.Errorf("unit = %q", got)
	}
}

func TestDHCPValidation(t *testing.T) {
	_, err := Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks:
    - {name: a, dhcp: {}}
    - {name: b, cidr: 10.1.0.0/24, dhcp: {range: 10.1.0.1-10.1.0.20}}
    - {name: c, cidr: 10.2.0.0/24, dhcp: {range: 10.2.0.50-10.2.1.9}}
    - {name: d, cidr: 10.3.0.0/24, dhcp: {range: 10.3.0.9-10.3.0.5}}
    - {name: e, cidr: "fd00::/64", dhcp: {}}
    - {name: f, cidr: 10.4.0.0/24}
  netboot: {kernel: vmlinuz, script: /x.ipxe, cmdline: "a\nb"}
  nodes:
    - name: n1
      nics:
        - {network: b, ip: 10.9.0.1}
        - {network: b, ip: 10.1.0.1}
        - {network: b, ip: 10.1.0.30}
        - {network: b, ip: 10.1.0.30}
        - {network: f, ip: 10.4.0.5}
        - {network: b, ip: nope}
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"spec.networks[0].dhcp: needs cidr",
		"spec.networks[1].dhcp.range: 10.1.0.1-10.1.0.20 contains the gateway 10.1.0.1",
		"spec.networks[2].dhcp.range: 10.2.0.50-10.2.1.9 is not within the host addresses",
		"spec.networks[3].dhcp.range: \"10.3.0.9-10.3.0.5\" ends before it starts",
		"spec.networks[4].dhcp: is IPv4 only",
		"spec.netboot: kernel and script are mutually exclusive",
		"spec.netboot.kernel: \"vmlinuz\" must be an absolute path",
		"spec.netboot.cmdline: must be a single line",
		"spec.nodes[0].nics[0].ip: 10.9.0.1 is not a host address of 10.1.0.0/24",
		"spec.nodes[0].nics[1].ip: 10.1.0.1 is the gateway of b",
		"spec.nodes[0].nics[3].ip: 10.1.0.30 already used by spec.nodes[0].nics[2]",
		"spec.nodes[0].nics[4].ip: needs dhcp on network \"f\"",
		"spec.nodes[0].nics[5].ip: \"nope\" is not a valid IP address",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	if n := len(ve.Problems); n != 13 {
		t.Errorf("got %d problems, want 13:\n%s", n, msg)
	}
}

func TestNetbootNeedsDHCPNetwork(t *testing.T) {
	_, err := Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks: [{name: pxe, cidr: 10.20.0.0/24}]
  netboot: {script: "https://boot.example/x.ipxe", initrd: /i}
  nodes: [{name: n1, nics: [{network: pxe}]}]
`))
	for _, want := range []string{"spec.netboot: needs a network with dhcp", "spec.netboot: initrd and cmdline only apply to kernel"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}
