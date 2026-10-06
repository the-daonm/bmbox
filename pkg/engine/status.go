package engine

import (
	"context"
	"fmt"

	"bmbox/pkg/bmc"
	"bmbox/pkg/hypervisor"
	"bmbox/pkg/workspace"
)

type NodeStatus struct {
	Name     string
	Domain   string
	Power    string
	BMCType  string
	BMCState string
	Endpoint string
	Console  string
}

// Status reports the live power state and BMC health of a lab's nodes,
// based on what the last `up` recorded.
func Status(ctx context.Context, lab string, ws *workspace.Workspace, opt Options) (*workspace.LabState, []NodeStatus, error) {
	st, err := ws.LoadState(lab)
	if err != nil {
		return nil, nil, err
	}
	if st == nil {
		return nil, nil, fmt.Errorf("lab %q is not up (no state in %s)", lab, ws.LabDir(lab))
	}

	hv, err := hypervisor.Connect(opt.LibvirtURI)
	if err != nil {
		return st, nil, err
	}
	defer hv.Close()
	var m *bmc.Manager
	if mm, err := bmc.NewManager(ctx, bmc.Tools{}); err == nil {
		m = mm
		defer m.Close()
	}

	var out []NodeStatus
	for _, n := range st.Nodes {
		ns := NodeStatus{Name: n.Name, Domain: n.Domain, BMCType: "-", BMCState: "-", Endpoint: "-", Console: n.SerialSocket}
		if ns.Power, err = hv.NodeState(n.Domain); err != nil {
			ns.Power = "error: " + err.Error()
		}
		if n.BMC != nil {
			ns.BMCType, ns.Endpoint = n.BMC.Type, n.BMC.Endpoint
			if m != nil {
				ns.BMCState = m.State(ctx, n.BMC.Unit)
			}
		}
		out = append(out, ns)
	}
	return st, out, nil
}
