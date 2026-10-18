package engine

import (
	"testing"

	"bmbox/pkg/topology"
)

func TestNetbootSpec(t *testing.T) {
	topo, err := topology.Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks:
    - {name: pxe, cidr: 10.20.0.0/24, dhcp: {range: 10.20.0.100-10.20.0.199}}
    - {name: data}
  netboot: {kernel: /k, cmdline: console=ttyS0}
  nodes:
    - {name: a, nics: [{network: pxe, ip: 10.20.0.10, mac: "52:54:00:00:00:0a"}, {network: data}]}
    - {name: b, nics: [{network: data}, {network: pxe, mac: "52:54:00:00:00:0b"}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	s := NetbootSpec(topo)
	if s.Unit != "bmbox-lab1-dnsmasq.service" || s.Dir != "/var/lib/bmbox/lab1" {
		t.Errorf("unit/dir = %s %s", s.Unit, s.Dir)
	}
	if len(s.Networks) != 1 || s.Networks[0].Bridge != "bmb-lab1-pxe" || s.Networks[0].Gateway.String() != "10.20.0.1/24" ||
		s.Networks[0].First.String() != "10.20.0.100" || s.Networks[0].Last.String() != "10.20.0.199" {
		t.Errorf("networks = %+v", s.Networks)
	}
	if len(s.Hosts) != 2 || s.Hosts[0].Name != "a" || s.Hosts[0].IP.String() != "10.20.0.10" ||
		s.Hosts[1].MAC != "52:54:00:00:00:0b" || s.Hosts[1].IP.IsValid() {
		t.Errorf("hosts = %+v", s.Hosts)
	}
	if s.Boot == nil || s.Boot.Kernel != "/k" || s.Boot.Cmdline != "console=ttyS0" {
		t.Errorf("boot = %+v", s.Boot)
	}
}

func TestDHCPUnitOrphanedWhenNoDHCPNetworkLeft(t *testing.T) {
	topo, err := topology.Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks: [{name: pxe, cidr: 10.20.0.0/24}]
  nodes: [{name: a, nics: [{network: pxe}]}]
`))
	if err != nil {
		t.Fatal(err)
	}
	d := discovered{units: map[string]string{}, bridges: map[string]string{}, dhcp: "bmbox-lab1-dnsmasq.service"}
	if o := findOrphans(topo, d, nil); o.dhcp != d.dhcp || o.empty() {
		t.Errorf("orphans = %+v", o)
	}
	topo.Spec.Networks[0].DHCP = &topology.DHCP{Range: "10.20.0.2-10.20.0.254"}
	if o := findOrphans(topo, d, nil); !o.empty() {
		t.Errorf("dnsmasq still declared, orphans = %+v", o)
	}
}
