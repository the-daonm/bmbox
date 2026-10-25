package engine

import (
	"os"
	"path/filepath"
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

func TestFirewallRuleOrphans(t *testing.T) {
	topo, err := topology.Parse([]byte(`
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata: {name: lab1}
spec:
  networks: [{name: pxe}, {name: ext, bridge: br-ext, external: true}]
  nodes: [{name: a, nics: [{network: pxe}]}]
`))
	if err != nil {
		t.Fatal(err)
	}
	d := discovered{units: map[string]string{}, bridges: map[string]string{}, rules: map[string]string{
		"pxe":  "bmb-lab1-pxe",  // declared
		"data": "bmb-lab1-data", // network removed
		"ext":  "br-ext",        // now external
	}}
	o := findOrphans(topo, d, nil)
	if len(o.rules) != 2 || o.rules["data"] == "" || o.rules["ext"] == "" {
		t.Errorf("rule orphans = %v", o.rules)
	}
}

func TestGivenPassword(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "pw")
	os.WriteFile(file, []byte("from-file\n"), 0o600)
	node := func(b topology.BMC) *topology.Node { return &topology.Node{Name: "n1", BMC: &b} }

	if pw, err := givenPassword(node(topology.BMC{Type: "redfish", Password: "inline"}), dir); err != nil || pw != "inline" {
		t.Errorf("inline: %q, %v", pw, err)
	}
	if pw, err := givenPassword(node(topology.BMC{Type: "redfish", PasswordFile: file}), dir); err != nil || pw != "from-file" {
		t.Errorf("file: %q, %v", pw, err)
	}
	t.Setenv("BMBOX_TEST_PW", "from-env")
	if pw, err := givenPassword(node(topology.BMC{Type: "redfish", PasswordEnv: "BMBOX_TEST_PW"}), dir); err != nil || pw != "from-env" {
		t.Errorf("env: %q, %v", pw, err)
	}
	if _, err := givenPassword(node(topology.BMC{Type: "redfish", PasswordEnv: "BMBOX_TEST_UNSET"}), dir); err == nil {
		t.Error("unset variable without a stored password accepted")
	}
	os.WriteFile(filepath.Join(dir, "password"), []byte("stored\n"), 0o600)
	if pw, err := givenPassword(node(topology.BMC{Type: "redfish", PasswordEnv: "BMBOX_TEST_UNSET"}), dir); err != nil || pw != "stored" {
		t.Errorf("unset variable must keep the stored password: %q, %v", pw, err)
	}
	if _, err := givenPassword(node(topology.BMC{Type: "ipmi", Password: "123456789012345678901"}), dir); err == nil {
		t.Error("21-character IPMI password accepted")
	}
}
