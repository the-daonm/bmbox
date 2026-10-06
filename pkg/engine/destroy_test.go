package engine

import (
	"strings"
	"testing"

	"bmbox/pkg/topology"
	"bmbox/pkg/workspace"
)

func TestPlanDestroyMergesStateAndTopology(t *testing.T) {
	topo, err := topology.Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks: [{name: pxe}, {name: ext, bridge: br-ext, external: true}]
  nodes:
    - {name: a, nics: [{network: pxe}]}
    - {name: b, nics: [{network: pxe}], disks: [{size: 1G}, {size: 2G}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	st := &workspace.LabState{
		Lab:      "lab1",
		Pool:     workspace.PoolState{Name: "bmbox-lab1", Path: "/p"},
		Nodes:    []workspace.NodeState{{Name: "a", Disks: []string{"/p/a-disk0.qcow2"}, NVRAM: "/p/a-VARS.fd"}},
		Networks: []workspace.NetworkState{{Name: "pxe", Bridge: "bmb-lab1-pxe", Created: true}},
	}
	p := planDestroy("lab1", topo, st)

	if got := strings.Join(p.nodes, ","); got != "a,b" {
		t.Errorf("nodes = %s", got)
	}
	if got := strings.Join(p.volumes, ","); got != "a-disk0.qcow2,a-VARS.fd,b-disk0.qcow2,b-disk1.qcow2,b-VARS.fd" {
		t.Errorf("volumes = %s", got)
	}
	if p.pool.Path != "/p" {
		t.Errorf("pool from state not preferred: %+v", p.pool)
	}
	if len(p.networks) != 2 || !p.networks[1].External {
		t.Errorf("networks = %+v", p.networks)
	}
}
