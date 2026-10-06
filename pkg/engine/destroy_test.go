package engine

import (
	"strings"
	"testing"

	"bmbox/pkg/topology"
	"bmbox/pkg/workspace"
)

func TestMergePreviousKeepsHistoryOnFailure(t *testing.T) {
	prev := &workspace.LabState{
		Pool:     workspace.PoolState{Name: "bmbox-lab1"},
		Networks: []workspace.NetworkState{{Name: "pxe", Created: true}},
		Nodes:    []workspace.NodeState{{Name: "a"}, {Name: "b"}},
	}
	st := &workspace.LabState{Nodes: []workspace.NodeState{{Name: "a", UUID: "new"}}}
	mergePrevious(st, prev)
	if len(st.Networks) != 1 || !st.Networks[0].Created || len(st.Nodes) != 2 || st.Nodes[0].UUID != "new" || st.Pool.Name == "" {
		t.Errorf("merged state = %+v", st)
	}
}

func TestPlanDestroyMergesStateTopologyAndHost(t *testing.T) {
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
		Nodes:    []workspace.NodeState{{Name: "a"}},
		Networks: []workspace.NetworkState{{Name: "pxe", Bridge: "bmb-lab1-pxe", Created: true}},
	}
	host := discovered{
		nodes:   []string{"old"}, // removed from topology, still defined
		units:   map[string]string{"gone": "bmbox-lab1-gone-bmc.service"},
		bridges: map[string]string{"stale": "bmb-lab1-stale"},
	}
	p := planDestroy("lab1", topo, st, host)

	if got := strings.Join(p.nodes, ","); got != "a,b,old,gone" {
		t.Errorf("nodes = %s", got)
	}
	if p.pool.Path != "/p" {
		t.Errorf("pool from state not preferred: %+v", p.pool)
	}
	var nets []string
	for _, n := range p.networks {
		nets = append(nets, n.Name+"="+n.Bridge)
	}
	if got := strings.Join(nets, ","); got != "pxe=bmb-lab1-pxe,ext=br-ext,stale=bmb-lab1-stale" {
		t.Errorf("networks = %s", got)
	}
}

func TestOwnsVolume(t *testing.T) {
	owned := ownsVolume([]string{"node1", "n-2"})
	for v, want := range map[string]bool{
		"node1-disk0.qcow2":  true,
		"node1-disk12.qcow2": true,
		"node1-VARS.fd":      true,
		"n-2-disk0.qcow2":    true,
		"node1-diskX.qcow2":  false,
		"node1-disk.qcow2":   false,
		"node3-disk0.qcow2":  false,
		"backup.qcow2":       false,
	} {
		if owned(v) != want {
			t.Errorf("owned(%q) = %v, want %v", v, !want, want)
		}
	}
}
