package topology

import (
	"errors"
	"os"
	"strings"
	"testing"
)

const minimal = `
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata:
  name: lab1
spec:
  networks:
    - name: pxe
      cidr: 10.20.0.0/24
  nodes:
    - name: node1
      nics:
        - network: pxe
`

func TestParseAppliesDefaults(t *testing.T) {
	topo, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	n := topo.Spec.Nodes[0]
	if n.CPUs != DefaultCPUs || n.Memory != DefaultMemory {
		t.Errorf("cpus/memory defaults not applied: %d %s", n.CPUs, n.Memory)
	}
	if len(n.Disks) != 1 || n.Disks[0].Size != DefaultDiskSize {
		t.Errorf("disk default not applied: %+v", n.Disks)
	}
	if strings.Join(n.Boot, ",") != "network,disk" {
		t.Errorf("boot default = %v", n.Boot)
	}
	if got := topo.Spec.Networks[0].Bridge; got != "bmb-lab1-pxe" {
		t.Errorf("bridge = %q", got)
	}
	if got := topo.Spec.Storage.Path; got != "/var/lib/libvirt/bmbox/lab1" {
		t.Errorf("storage path = %q", got)
	}
	if got := topo.DomainName("node1"); got != "bmbox-lab1-node1" {
		t.Errorf("domain name = %q", got)
	}

	again, _ := Parse([]byte(minimal))
	if again.Spec.Nodes[0].NICs[0].MAC != n.NICs[0].MAC {
		t.Error("generated MAC is not deterministic")
	}
	if !strings.HasPrefix(n.NICs[0].MAC, "52:54:00:") {
		t.Errorf("MAC %q outside the QEMU OUI", n.NICs[0].MAC)
	}
}

func TestLongBridgeNameIsHashed(t *testing.T) {
	name := defaultBridgeName("a-very-long-lab", "provisioning")
	if len(name) > MaxIfNameLen || !strings.HasPrefix(name, "bmb-") {
		t.Fatalf("bad bridge name %q", name)
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte(minimal + "  bogus: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestValidationCollectsAllProblems(t *testing.T) {
	doc := `
apiVersion: bmbox.io/v1alpha1
kind: Topology
metadata:
  name: Lab_1
spec:
  networks:
    - name: pxe
      cidr: 10.0.0.0/16
    - name: pxe
      bridge: this-name-is-way-too-long
      cidr: 10.0.1.0/24
  nodes:
    - name: node1
      memory: 64MiB
      nics:
        - network: missing
        - network: pxe
          mac: 52:54:00:00:00:01
      boot: [network, cdrom]
      bmc: {type: ipmi}
    - name: node1
      disks: []
      nics:
        - network: pxe
          mac: 52:54:00:00:00:01
`
	_, err := Parse([]byte(doc))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	want := []string{
		"metadata.name",
		"duplicate network",
		"exceeds the Linux limit",
		"overlaps",
		"below the minimum",
		`unknown network "missing"`,
		`unknown boot device "cdrom"`,
		"is required for IPMI",
		"duplicate node",
		"already used by",
	}
	msg := err.Error()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("missing problem %q in:\n%s", w, msg)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]Size{
		"512MiB": 512 * MiB,
		"4GiB":   4 * GiB,
		"40G":    40 * GiB,
		"1GB":    1_000_000_000,
		"1024":   1024,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "4XB", "-1G"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) succeeded", bad)
		}
	}
}

func TestParseGateway(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.0/24":   "10.0.0.1/24",
		"10.0.0.254/24": "10.0.0.254/24",
		"fd00::/64":     "fd00::1/64",
	} {
		got, err := ParseGateway(in)
		if err != nil || got.String() != want {
			t.Errorf("ParseGateway(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	if _, err := ParseGateway("10.0.0.1/32"); err == nil {
		t.Error("expected /32 to be rejected")
	}
}

func TestExampleTopologyIsValid(t *testing.T) {
	if _, err := os.Stat("../../examples/topology.yaml"); err != nil {
		t.Skip("example not found")
	}
	if _, err := Load("../../examples/topology.yaml"); err != nil {
		t.Fatal(err)
	}
}
