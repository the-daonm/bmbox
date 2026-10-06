package engine

import (
	"strings"
	"testing"

	"bmbox/pkg/topology"
)

func TestFindOrphans(t *testing.T) {
	topo, err := topology.Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks: [{name: pxe}]
  nodes:
    - {name: a, nics: [{network: pxe}], bmc: {type: redfish}}
    - {name: b, nics: [{network: pxe}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	host := discovered{
		nodes: []string{"a", "b", "c"}, // c was removed from the topology
		units: map[string]string{
			"a": "bmbox-lab1-a-bmc.service",
			"b": "bmbox-lab1-b-bmc.service", // b's BMC was removed
		},
		bridges: map[string]string{"pxe": "bmb-lab1-pxe", "data": "bmb-lab1-data"},
	}
	vols := []string{
		"a-disk0.qcow2", "a-VARS.fd",
		"b-disk0.qcow2", "b-disk1.qcow2", "b-VARS.fd", // b went from 2 disks to 1
		"c-disk0.qcow2", "c-VARS.fd",
		"iso-from-someone-else.iso",
	}
	o := findOrphans(topo, host, vols)

	if got := strings.Join(o.nodes, ","); got != "c" {
		t.Errorf("nodes = %s", got)
	}
	if len(o.units) != 1 || o.units["b"] == "" {
		t.Errorf("units = %v", o.units)
	}
	if got := strings.Join(o.volumes, ","); got != "b-disk1.qcow2,c-VARS.fd,c-disk0.qcow2" {
		t.Errorf("volumes = %s", got)
	}
	if len(o.bridges) != 1 || o.bridges["data"] != "bmb-lab1-data" {
		t.Errorf("bridges = %v", o.bridges)
	}

	clean := findOrphans(topo, discovered{nodes: []string{"a", "b"}}, []string{"a-disk0.qcow2"})
	if !clean.empty() {
		t.Errorf("expected no orphans, got %+v", clean)
	}
}
