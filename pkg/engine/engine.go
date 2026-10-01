// Package engine turns a validated topology into running infrastructure by
// driving pkg/network and pkg/hypervisor, and records the result in the
// workspace.
package engine

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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

// Up brings the lab to the declared state: bridges exist and every node is
// defined in libvirt (powered off). It is safe to run repeatedly.
func Up(t *topology.Topology, raw []byte, ws *workspace.Workspace, opt Options) (st *workspace.LabState, err error) {
	lab := t.Metadata.Name
	log := func(format string, args ...any) { fmt.Fprintf(opt.Out, format+"\n", args...) }

	if os.Geteuid() != 0 {
		return nil, errors.New("bmbox up must run as root (bridges need CAP_NET_ADMIN): sudo bmbox up ...")
	}
	if err := ws.SaveTopology(lab, raw); err != nil {
		return nil, fmt.Errorf("save topology: %w", err)
	}

	st = &workspace.LabState{Lab: lab, Libvirt: opt.LibvirtURI}
	if prev, _ := ws.LoadState(lab); prev != nil {
		// Keep the original values of sysctls we changed on a previous run.
		st.Sysctls = prev.Sysctls
	}
	// Persist whatever was created, even on failure, so destroy can clean up.
	defer func() {
		if serr := ws.SaveState(st); serr != nil && err == nil {
			err = fmt.Errorf("save state: %w", serr)
		}
	}()

	// 1. Networks
	log("==> Networks")
	needForwarding := false
	for _, n := range t.Spec.Networks {
		spec := network.BridgeSpec{
			Name:     n.Bridge,
			Owner:    lab + "/" + n.Name,
			MTU:      n.MTU,
			External: n.External,
		}
		if n.CIDR != "" {
			spec.Gateway, _ = topology.ParseGateway(n.CIDR)
			needForwarding = true
		}
		res, err := network.EnsureBridge(spec)
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
	return st, nil
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
