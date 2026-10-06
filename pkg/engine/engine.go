// Package engine turns a validated topology into running infrastructure by
// driving pkg/network, pkg/hypervisor and pkg/bmc, and records the result in
// the workspace.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"bmbox/pkg/bmc"
	"bmbox/pkg/hypervisor"
	"bmbox/pkg/network"
	"bmbox/pkg/topology"
	"bmbox/pkg/workspace"
)

// SerialRoot holds the per-node serial console sockets. /run is shared with
// libvirtd even when it runs in a container.
const SerialRoot = "/run/bmbox"

type Options struct {
	LibvirtURI string
	Out        io.Writer
	// SushyEmulator / VBMC override where the BMC tools are found.
	SushyEmulator string
	VBMC          string
}

func SerialSocket(lab, node string) string {
	return filepath.Join(SerialRoot, lab, node+".serial.sock")
}

func DiskVolumeName(node string, i int) string { return fmt.Sprintf("%s-disk%d.qcow2", node, i) }
func NVRAMVolumeName(node string) string       { return node + "-VARS.fd" }

// NodeSpec maps a topology node onto hypervisor inputs.
func NodeSpec(t *topology.Topology, n *topology.Node, fw hypervisor.Firmware, disks []string, nvram string) hypervisor.NodeSpec {
	spec := hypervisor.NodeSpec{
		Lab:          t.Metadata.Name,
		Node:         n.Name,
		DomainName:   t.DomainName(n.Name),
		CPUs:         n.CPUs,
		MemoryBytes:  uint64(n.Memory),
		Disks:        disks,
		Boot:         n.Boot,
		Loader:       fw.Loader,
		NVRAM:        nvram,
		VarsTemplate: fw.VarsTemplate,
		SerialSocket: SerialSocket(t.Metadata.Name, n.Name),
	}
	for _, nic := range n.NICs {
		spec.NICs = append(spec.NICs, hypervisor.NICSpec{MAC: nic.MAC, Bridge: t.Network(nic.Network).Bridge})
	}
	return spec
}

// BMCSpec maps a node's topology BMC onto pkg/bmc inputs.
func BMCSpec(t *topology.Topology, n *topology.Node, uuid, loader, libvirtURI string, ws *workspace.Workspace) bmc.Spec {
	return bmc.Spec{
		Lab: t.Metadata.Name, Node: n.Name, Domain: t.DomainName(n.Name), UUID: uuid,
		Type: n.BMC.Type, Address: n.BMC.Address, Port: n.BMC.Port,
		Username: n.BMC.Username, Password: n.BMC.Password,
		LibvirtURI: libvirtURI, Loader: loader,
		Unit: t.BMCUnit(n.Name),
		Dir:  ws.BMCDir(t.Metadata.Name, n.Name),
	}
}

// Up brings the lab to the declared state: bridges exist, every node is
// defined in libvirt (powered off) and every BMC is serving. It is safe to
// run repeatedly.
func Up(ctx context.Context, t *topology.Topology, raw []byte, ws *workspace.Workspace, opt Options) (st *workspace.LabState, err error) {
	lab := t.Metadata.Name
	log := func(format string, args ...any) { fmt.Fprintf(opt.Out, format+"\n", args...) }

	if os.Geteuid() != 0 {
		return nil, errors.New("bmbox up must run as root (bridges need CAP_NET_ADMIN): sudo bmbox up ...")
	}
	if err := ws.SaveTopology(lab, raw); err != nil {
		return nil, fmt.Errorf("save topology: %w", err)
	}

	st = &workspace.LabState{Lab: lab, Libvirt: opt.LibvirtURI}
	prev, _ := ws.LoadState(lab)
	if prev != nil {
		// Keep the original values of sysctls we changed on a previous run.
		st.Sysctls = prev.Sysctls
	}
	// Persist whatever was created, even on failure, so destroy can clean up.
	// A failed run must not forget what earlier runs created.
	defer func() {
		if err != nil {
			mergePrevious(st, prev)
		}
		if serr := ws.SaveState(st); serr != nil && err == nil {
			err = fmt.Errorf("save state: %w", serr)
		}
	}()

	// 1. Networks
	log("==> Networks")
	needForwarding := false
	specs := make([]network.BridgeSpec, len(t.Spec.Networks))
	for i, n := range t.Spec.Networks {
		specs[i] = network.BridgeSpec{
			Name:     n.Bridge,
			Owner:    lab + "/" + n.Name,
			MTU:      n.MTU,
			External: n.External,
		}
		if n.CIDR != "" {
			specs[i].Gateway, _ = topology.ParseGateway(n.CIDR)
			needForwarding = true
		}
	}
	// Check every network before touching any, so a conflict on the last
	// one does not leave the first ones half-created.
	for i, n := range t.Spec.Networks {
		if _, err := network.CheckBridge(specs[i]); err != nil {
			return st, fmt.Errorf("network %s: %w", n.Name, err)
		}
	}
	bmcs, err := preflightBMCs(ctx, t, specs, opt)
	if err != nil {
		return st, err
	}
	if bmcs != nil {
		defer bmcs.Close()
	}
	for i, n := range t.Spec.Networks {
		res, err := network.EnsureBridge(specs[i])
		if err != nil {
			return st, fmt.Errorf("network %s: %w", n.Name, err)
		}
		ns := workspace.NetworkState{Name: n.Name, Bridge: res.Name, Created: res.Created, External: res.External, Address: res.Address}
		if prev := prevNetwork(ws, lab, n.Name); prev != nil {
			ns.Created = ns.Created || prev.Created
			ns.Sysctls = prev.Sysctls
		}
		ns.Sysctls = mergeSysctls(ns.Sysctls, res.Sysctls)
		st.Networks = append(st.Networks, ns)
		action := "ok"
		switch {
		case res.External:
			action = "external"
		case res.Created:
			action = "created"
		}
		log("    %-10s bridge=%-15s %-8s %s", n.Name, res.Name, action, res.Address)
	}

	changes, err := network.EnsureHostSysctls(needForwarding)
	st.Sysctls = mergeSysctls(st.Sysctls, changes)
	if err != nil {
		return st, err
	}
	for _, c := range changes {
		log("    sysctl %s: %s -> %s", c.Key, c.Old, c.New)
	}

	// 2. Hypervisor
	hv, err := hypervisor.Connect(opt.LibvirtURI)
	if err != nil {
		return st, err
	}
	defer hv.Close()
	ver, _ := hv.Version()
	log("==> Libvirt %s (%s)", ver, opt.LibvirtURI)

	fw, err := hv.ResolveFirmware(t.Spec.UEFI)
	if err != nil {
		return st, err
	}
	log("    firmware  loader=%s", fw.Loader)
	log("              vars=%s", fw.VarsTemplate)

	pool, err := hv.EnsurePool(t.PoolName(), t.Spec.Storage.Path)
	if err != nil {
		return st, err
	}
	st.Pool = workspace.PoolState{Name: pool.Name, Path: pool.Path}
	log("    pool      %s -> %s", pool.Name, pool.Path)

	if err := os.MkdirAll(filepath.Join(SerialRoot, lab), 0o755); err != nil {
		return st, fmt.Errorf("create serial socket dir: %w", err)
	}

	// 3. Nodes
	log("==> Nodes")
	for i := range t.Spec.Nodes {
		n := &t.Spec.Nodes[i]
		ns := workspace.NodeState{Name: n.Name, Domain: t.DomainName(n.Name), Loader: fw.Loader, SerialSocket: SerialSocket(lab, n.Name)}

		var disks []string
		for j, d := range n.Disks {
			v, err := pool.EnsureDisk(DiskVolumeName(n.Name, j), uint64(d.Size))
			if err != nil {
				return st, fmt.Errorf("node %s: %w", n.Name, err)
			}
			disks = append(disks, v.Path)
		}
		ns.Disks = disks

		nvram := pool.VolumePath(NVRAMVolumeName(n.Name))
		if fw.VarsData != nil {
			v, err := pool.EnsureNVRAM(NVRAMVolumeName(n.Name), fw.VarsData)
			if err != nil {
				return st, fmt.Errorf("node %s: %w", n.Name, err)
			}
			nvram = v.Path
		}
		ns.NVRAM = nvram
		for _, nic := range n.NICs {
			ns.MACs = append(ns.MACs, nic.MAC)
		}

		res, err := hv.DefineNode(NodeSpec(t, n, fw, disks, nvram))
		if err != nil {
			return st, fmt.Errorf("node %s: %w", n.Name, err)
		}
		ns.UUID, ns.State = res.UUID, res.State
		st.Nodes = append(st.Nodes, ns)

		action := "updated"
		switch {
		case res.Skipped:
			action = "unchanged (running)"
		case res.Created:
			action = "defined"
		}
		if res.XML != "" {
			if _, err := ws.SaveDomainXML(lab, n.Name, res.XML); err != nil {
				return st, err
			}
		}
		log("    %-10s %-24s %-8s %s", n.Name, ns.Domain, ns.State, action)
	}

	// 4. BMCs
	if bmcs == nil {
		return st, nil
	}
	log("==> BMCs")
	for i := range t.Spec.Nodes {
		n := &t.Spec.Nodes[i]
		ns := &st.Nodes[i]
		if n.BMC == nil {
			continue
		}
		spec := BMCSpec(t, n, ns.UUID, fw.Loader, opt.LibvirtURI, ws)
		res, err := bmcs.Ensure(ctx, spec, ws)
		if err != nil {
			return st, fmt.Errorf("node %s: %w", n.Name, err)
		}
		ns.BMC = &workspace.BMCState{
			Type: spec.Type, Address: spec.Address, Port: spec.Port,
			Username: spec.Username, Unit: spec.Unit, Endpoint: spec.Endpoint(),
		}
		action := "running"
		if res.Started {
			action = "started"
		}
		log("    %-10s %-8s %-8s %s", n.Name, spec.Type, action, spec.Endpoint())
	}
	return st, nil
}

// preflightBMCs locates the BMC tools, connects to systemd and checks every
// BMC port is free, before anything is created. It returns nil when the lab
// has no BMCs.
func preflightBMCs(ctx context.Context, t *topology.Topology, nets []network.BridgeSpec, opt Options) (*bmc.Manager, error) {
	need := map[string]bool{}
	for _, n := range t.Spec.Nodes {
		if n.BMC != nil {
			need[n.BMC.Type] = true
		}
	}
	if len(need) == 0 {
		return nil, nil
	}
	tools, err := bmc.FindTools(opt.SushyEmulator, opt.VBMC, need)
	if err != nil {
		return nil, err
	}
	m, err := bmc.NewManager(ctx, tools)
	if err != nil {
		return nil, err
	}

	// A BMC may listen on a lab gateway that only exists once its bridge
	// is created.
	gateways := map[string]bool{}
	for _, s := range nets {
		if s.Gateway.IsValid() {
			gateways[s.Gateway.Addr().String()] = true
		}
	}
	for _, n := range t.Spec.Nodes {
		if n.BMC == nil || m.State(ctx, t.BMCUnit(n.Name)) == "active" {
			continue // ours, from a previous up
		}
		err := bmc.CheckPort(n.BMC.Type, n.BMC.Address, n.BMC.Port)
		if errors.Is(err, bmc.ErrAddrNotLocal) {
			if gateways[n.BMC.Address] {
				continue
			}
			err = fmt.Errorf("address %s is not configured on this host", n.BMC.Address)
		}
		if err != nil {
			m.Close()
			return nil, fmt.Errorf("node %s BMC: %w", n.Name, err)
		}
	}
	return m, nil
}

// mergePrevious adds the networks, nodes and pool of a previous state that a
// failed run did not get to refresh.
func mergePrevious(st, prev *workspace.LabState) {
	if prev == nil {
		return
	}
	hasNet, hasNode := map[string]bool{}, map[string]bool{}
	for _, n := range st.Networks {
		hasNet[n.Name] = true
	}
	for _, n := range st.Nodes {
		hasNode[n.Name] = true
	}
	for _, n := range prev.Networks {
		if !hasNet[n.Name] {
			st.Networks = append(st.Networks, n)
		}
	}
	for _, n := range prev.Nodes {
		if !hasNode[n.Name] {
			st.Nodes = append(st.Nodes, n)
		}
	}
	if st.Pool.Name == "" {
		st.Pool = prev.Pool
	}
}

func prevNetwork(ws *workspace.Workspace, lab, name string) *workspace.NetworkState {
	prev, _ := ws.LoadState(lab)
	if prev == nil {
		return nil
	}
	for i := range prev.Networks {
		if prev.Networks[i].Name == name {
			return &prev.Networks[i]
		}
	}
	return nil
}

// mergeSysctls keeps the first recorded "old" value for each key so the
// original host setting survives repeated runs.
func mergeSysctls(have []workspace.SysctlState, add []network.SysctlChange) []workspace.SysctlState {
	for _, c := range add {
		found := false
		for i := range have {
			if have[i].Key == c.Key {
				have[i].New = c.New
				found = true
			}
		}
		if !found {
			have = append(have, workspace.SysctlState{Key: c.Key, Old: c.Old, New: c.New})
		}
	}
	return have
}
