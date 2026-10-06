package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"bmbox/pkg/bmc"
	"bmbox/pkg/hypervisor"
	"bmbox/pkg/network"
	"bmbox/pkg/topology"
	"bmbox/pkg/workspace"
)

// labPlan is what destroy removes: the union of the recorded state and the
// topology, so a lab is still cleaned after a crash mid-`up`. Every step
// re-checks ownership, so names derived from the topology are safe to try.
type labPlan struct {
	lab      string
	nodes    []string
	volumes  []string
	pool     workspace.PoolState
	networks []workspace.NetworkState
	sysctls  []workspace.SysctlState
}

func planDestroy(lab string, t *topology.Topology, st *workspace.LabState) labPlan {
	p := labPlan{lab: lab}
	names := &topology.Topology{Metadata: topology.Metadata{Name: lab}}
	seenNode, seenVol, seenNet := map[string]bool{}, map[string]bool{}, map[string]bool{}
	addNode := func(n string) {
		if !seenNode[n] {
			seenNode[n] = true
			p.nodes = append(p.nodes, n)
		}
	}
	addVol := func(v string) {
		if v != "" && !seenVol[v] {
			seenVol[v] = true
			p.volumes = append(p.volumes, v)
		}
	}

	if st != nil {
		for _, n := range st.Nodes {
			addNode(n.Name)
			for _, d := range n.Disks {
				addVol(filepath.Base(d))
			}
			addVol(filepath.Base(n.NVRAM))
		}
		p.pool = st.Pool
		for _, n := range st.Networks {
			seenNet[n.Name] = true
			p.networks = append(p.networks, n)
		}
		p.sysctls = st.Sysctls
	}
	if t != nil {
		for _, n := range t.Spec.Nodes {
			addNode(n.Name)
			for i := range n.Disks {
				addVol(DiskVolumeName(n.Name, i))
			}
			addVol(NVRAMVolumeName(n.Name))
		}
		if p.pool.Name == "" {
			p.pool = workspace.PoolState{Name: t.PoolName(), Path: t.Spec.Storage.Path}
		}
		for _, n := range t.Spec.Networks {
			if !seenNet[n.Name] {
				p.networks = append(p.networks, workspace.NetworkState{Name: n.Name, Bridge: n.Bridge, External: n.External})
			}
		}
	}
	if p.pool.Name == "" {
		p.pool = workspace.PoolState{Name: names.PoolName(), Path: topology.DefaultStorageRoot + "/" + lab}
	}
	return p
}

// Destroy removes everything `up` created for a lab, in reverse order:
// BMCs, domains, storage, bridges, host sysctls, runtime and workspace
// files. It keeps going after an error and reports all of them.
func Destroy(ctx context.Context, lab string, t *topology.Topology, ws *workspace.Workspace, opt Options) error {
	log := func(format string, args ...any) { fmt.Fprintf(opt.Out, format+"\n", args...) }
	if os.Geteuid() != 0 {
		return errors.New("bmbox destroy must run as root: sudo bmbox destroy ...")
	}
	st, err := ws.LoadState(lab)
	if err != nil {
		return err
	}
	if st == nil && t == nil {
		return fmt.Errorf("no state for lab %q in %s and no topology given", lab, ws.LabDir(lab))
	}
	p := planDestroy(lab, t, st)
	names := &topology.Topology{Metadata: topology.Metadata{Name: lab}}
	var errs []error
	fail := func(err error) {
		errs = append(errs, err)
		log("    error: %v", err)
	}

	log("==> BMCs")
	if m, err := bmc.NewManager(ctx, bmc.Tools{}); err != nil {
		fail(err)
	} else {
		for _, n := range p.nodes {
			unit := names.BMCUnit(n)
			if s := m.State(ctx, unit); s == "inactive" || s == "unknown" {
				continue
			}
			if err := m.Stop(ctx, unit); err != nil {
				fail(err)
				continue
			}
			log("    %-10s stopped %s", n, unit)
		}
		m.Close()
	}

	log("==> Nodes and storage")
	hv, err := hypervisor.Connect(opt.LibvirtURI)
	if err != nil {
		fail(err)
	} else {
		for _, n := range p.nodes {
			deleted, err := hv.DeleteNode(lab, n, names.DomainName(n))
			if err != nil {
				fail(err)
				continue
			}
			if deleted {
				log("    %-10s undefined %s", n, names.DomainName(n))
			}
		}
		deleted, err := hv.DeletePool(p.pool.Name, p.pool.Path, p.volumes)
		if err != nil {
			fail(err)
		} else if deleted {
			log("    pool       deleted %s (%s)", p.pool.Name, p.pool.Path)
		}
		hv.Close()
	}

	log("==> Networks")
	for _, n := range p.networks {
		// The bridge alias, not the recorded "created" flag, proves bmbox
		// owns a bridge: it is only ever set on bridges bmbox created.
		if n.External {
			log("    %-10s kept %s (external)", n.Name, n.Bridge)
			continue
		}
		deleted, err := network.DeleteBridge(n.Bridge, lab+"/"+n.Name)
		if errors.Is(err, network.ErrNotOwned) {
			log("    %-10s kept %s (not created by bmbox)", n.Name, n.Bridge)
			continue
		}
		if err != nil {
			fail(err)
			continue
		}
		if deleted {
			log("    %-10s deleted %s", n.Name, n.Bridge)
		}
	}
	for _, c := range p.sysctls {
		if otherLabNeeds(ws, lab, c.Key) {
			log("    sysctl %s kept at %s (used by another lab)", c.Key, c.New)
			continue
		}
		restored, err := network.RestoreSysctl(network.SysctlChange{Key: c.Key, Old: c.Old, New: c.New})
		if err != nil {
			fail(err)
		} else if restored {
			log("    sysctl %s restored to %s", c.Key, c.Old)
		}
	}

	if err := os.RemoveAll(filepath.Join(SerialRoot, lab)); err != nil {
		fail(err)
	}
	if len(errs) > 0 {
		log("Workspace kept at %s so destroy can be retried.", ws.LabDir(lab))
		return errors.Join(errs...)
	}
	if err := ws.RemoveLab(lab); err != nil {
		return err
	}
	log("\nLab %q destroyed.", lab)
	return nil
}

// otherLabNeeds reports whether another lab's state also changed key, in
// which case restoring it would break that lab.
func otherLabNeeds(ws *workspace.Workspace, lab, key string) bool {
	labs, _ := ws.Labs()
	for _, other := range labs {
		if other == lab {
			continue
		}
		st, _ := ws.LoadState(other)
		if st == nil {
			continue
		}
		for _, c := range st.Sysctls {
			if c.Key == key {
				return true
			}
		}
	}
	return false
}
