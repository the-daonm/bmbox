package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/the-daonm/bmbox/pkg/bmc"
	"github.com/the-daonm/bmbox/pkg/hypervisor"
	"github.com/the-daonm/bmbox/pkg/netboot"
	"github.com/the-daonm/bmbox/pkg/systemd"
	"github.com/the-daonm/bmbox/pkg/workspace"
)

// NodeStatus is the live state of a node. The JSON form is part of the
// CLI's interface (status --output json): add fields, do not rename them.
type NodeStatus struct {
	Name     string   `json:"name"`
	Domain   string   `json:"domain"`
	Power    string   `json:"power"`
	IPs      []string `json:"ips"` // current DHCP leases of the node's NICs
	MACs     []string `json:"macs"`
	BMCType  string   `json:"bmcType,omitempty"`
	BMCState string   `json:"bmcState,omitempty"`
	Endpoint string   `json:"bmcEndpoint,omitempty"`
	Console  string   `json:"console"`
}

type LabStatus struct {
	State *workspace.LabState
	Nodes []NodeStatus
	// DHCPState is the ActiveState of the lab's dnsmasq, "" without DHCP.
	DHCPState string
}

// Status reports the live power state, DHCP leases and BMC health of a
// lab's nodes, based on what the last `up` recorded.
func Status(ctx context.Context, lab string, ws *workspace.Workspace, opt Options) (*LabStatus, error) {
	st, err := ws.LoadState(lab)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("lab %q is not up (no state in %s)", lab, ws.LabDir(lab))
	}
	out := &LabStatus{State: st}

	hv, err := hypervisor.Connect(opt.LibvirtURI)
	if err != nil {
		return out, err
	}
	defer hv.Close()
	var m *bmc.Manager
	sd, err := systemd.Connect(ctx)
	if err == nil {
		defer sd.Close()
		m = bmc.NewManager(sd, bmc.Tools{})
	}

	leases := map[string]string{} // MAC -> IP
	if st.DHCP != nil {
		out.DHCPState = "unknown"
		if sd != nil {
			out.DHCPState = sd.State(ctx, st.DHCP.Unit)
		}
		ls, _ := netboot.ReadLeases(st.DHCP.LeaseFile)
		for _, l := range ls {
			if l.Expiry.Unix() == 0 || l.Expiry.After(time.Now()) {
				leases[l.MAC] = l.IP
			}
		}
	}

	for _, n := range st.Nodes {
		ns := NodeStatus{Name: n.Name, Domain: n.Domain, MACs: n.MACs, IPs: []string{}, Console: n.SerialSocket}
		if ns.Power, err = hv.NodeState(n.Domain); err != nil {
			ns.Power = "error: " + err.Error()
		}
		for _, mac := range n.MACs {
			if ip, ok := leases[strings.ToLower(mac)]; ok {
				ns.IPs = append(ns.IPs, ip)
			}
		}
		if n.BMC != nil {
			ns.BMCType, ns.Endpoint = n.BMC.Type, n.BMC.Endpoint
			if m != nil {
				ns.BMCState = m.State(ctx, n.BMC.Unit)
			}
		}
		out.Nodes = append(out.Nodes, ns)
	}
	return out, nil
}
