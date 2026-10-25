package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/the-daonm/bmbox/pkg/engine"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

func TestStatusJSONShape(t *testing.T) {
	ls := &engine.LabStatus{
		State: &workspace.LabState{
			Lab: "demo", UpdatedAt: time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC), Libvirt: "qemu:///system",
			Networks: []workspace.NetworkState{{Name: "pxe", Bridge: "bmb-demo-pxe", Address: "172.30.10.1/24"}},
			DHCP:     &workspace.DHCPState{Unit: "bmbox-demo-dnsmasq.service"},
		},
		DHCPState: "active",
		Nodes: []engine.NodeStatus{{
			Name: "node1", Domain: "bmbox-demo-node1", Power: "running", IPs: []string{"172.30.10.11"},
			BMCType: "redfish", BMCState: "active", Endpoint: "https://127.0.0.1:8300/redfish/v1/Systems/u",
		}},
	}
	b, err := json.Marshal(statusJSON(ls))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"lab":"demo"`, `"updatedAt":"2026-10-25T01:02:03Z"`,
		`"networks":[{"name":"pxe","bridge":"bmb-demo-pxe","address":"172.30.10.1/24","external":false}]`,
		`"dhcp":{"unit":"bmbox-demo-dnsmasq.service","state":"active"}`,
		`"ips":["172.30.10.11"]`, `"bmcEndpoint":"https://127.0.0.1:8300/redfish/v1/Systems/u"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}

	// Empty lists are [] rather than null, so consumers can iterate.
	b, _ = json.Marshal(statusJSON(&engine.LabStatus{State: &workspace.LabState{Lab: "empty"}}))
	if !strings.Contains(string(b), `"networks":[]`) || !strings.Contains(string(b), `"nodes":[]`) || strings.Contains(string(b), `"dhcp"`) {
		t.Errorf("empty lab: %s", b)
	}
}
