package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bmbox/pkg/bmc"
	"bmbox/pkg/hypervisor"
	"bmbox/pkg/network"
	"bmbox/pkg/topology"
	"bmbox/pkg/workspace"
)

// discovered is what is actually present on the host for a lab, found
// through bmbox's ownership marks (domain metadata, bridge alias, unit
// description) rather than names, so resources that dropped out of the
// topology or state are still found.
type discovered struct {
	nodes   []string
	units   map[string]string // node -> unit
	bridges map[string]string // network -> bridge
}

func discover(ctx context.Context, lab string, hv *hypervisor.Client, m *bmc.Manager) (discovered, error) {
	d := discovered{units: map[string]string{}, bridges: map[string]string{}}
	var errs []error
	if hv != nil {
		nodes, err := hv.LabNodes(lab)
		errs = append(errs, err)
		d.nodes = nodes
	}
	if m != nil {
		units, err := m.LabUnits(ctx, lab)
		errs = append(errs, err)
		if units != nil {
			d.units = units
		}
	}
	bridges, err := network.LabBridges(lab)
	errs = append(errs, err)
	if bridges != nil {
		d.bridges = bridges
	}
	return d, errors.Join(errs...)
}

// labPlan is what destroy removes: the union of the recorded state, the
// topology and what is discovered on the host. Every step re-checks
// ownership, so names derived from the topology are safe to try.
type labPlan struct {
	lab      string
	nodes    []string
	pool     workspace.PoolState
	networks []workspace.NetworkState
	sysctls  []workspace.SysctlState
}

func planDestroy(lab string, t *topology.Topology, st *workspace.LabState, d discovered) labPlan {
	p := labPlan{lab: lab}
	names := &topology.Topology{Metadata: topology.Metadata{Name: lab}}
	seenNode, seenNet := map[string]bool{}, map[string]bool{}
	addNode := func(n string) {
		if !seenNode[n] {
			seenNode[n] = true
			p.nodes = append(p.nodes, n)
		}
	}
	addNet := func(n workspace.NetworkState) {
		if !seenNet[n.Name] {
			seenNet[n.Name] = true
			p.networks = append(p.networks, n)
		}
	}

	if st != nil {
		for _, n := range st.Nodes {
			addNode(n.Name)
		}
		for _, n := range st.Networks {
			addNet(n)
		}
		p.pool = st.Pool
		p.sysctls = st.Sysctls
	}
	if t != nil {
		for _, n := range t.Spec.Nodes {
			addNode(n.Name)
		}
		for _, n := range t.Spec.Networks {
			addNet(workspace.NetworkState{Name: n.Name, Bridge: n.Bridge, External: n.External})
		}
		if p.pool.Name == "" {
			p.pool = workspace.PoolState{Name: t.PoolName(), Path: t.Spec.Storage.Path}
		}
	}
	for _, n := range d.nodes {
		addNode(n)
	}
	for n := range d.units {
		addNode(n)
	}
	for n, br := range d.bridges {
		addNet(workspace.NetworkState{Name: n, Bridge: br})
	}
	if p.pool.Name == "" {
		p.pool = workspace.PoolState{Name: names.PoolName(), Path: topology.DefaultStorageRoot + "/" + lab}
	}
	return p
}

// ownsVolume reports whether a pool volume follows bmbox's naming for one of
// the given nodes (<node>-disk<N>.qcow2 or <node>-VARS.fd).
func ownsVolume(nodes []string) func(string) bool {
	return func(v string) bool {
		for _, n := range nodes {
			if v == NVRAMVolumeName(n) {
				return true
			}
			rest, ok := strings.CutPrefix(v, n+"-disk")
			if ok && strings.HasSuffix(rest, ".qcow2") {
				idx := strings.TrimSuffix(rest, ".qcow2")
				if idx != "" && strings.Trim(idx, "0123456789") == "" {
					return true
				}
			}
		}
		return false
	}
}

// Destroy removes everything `up` created for a lab, in reverse order:
// BMCs, domains, storage, bridges, host sysctls, runtime and workspace
// files. It keeps going after an error and reports all of them.
func Destroy(ctx context.Context, lab string, t *topology.Topology, ws *workspace.Workspace, opt Options) error {
	log := func(format string, args ...any) { fmt.Fprintf(opt.Out, format+"\n", args...) }
	if os.Geteuid() != 0 {
		return errors.New("bmbox destroy must run as root: sudo bmbox destroy ...")
	}
	unlock, err := ws.Lock(lab)
	if err != nil {
		return err
	}
	defer unlock()

	st, err := ws.LoadState(lab)
	if err != nil {
		return err
	}
	names := &topology.Topology{Metadata: topology.Metadata{Name: lab}}
	var errs []error
	fail := func(err error) {
		errs = append(errs, err)
		log("    error: %v", err)
	}

	m, err := bmc.NewManager(ctx, bmc.Tools{})
	if err != nil {
		fail(err)
		m = nil
	} else {
		defer m.Close()
	}
	hv, err := hypervisor.Connect(opt.LibvirtURI)
	if err != nil {
		fail(err)
		hv = nil
	} else {
		defer hv.Close()
	}
	d, err := discover(ctx, lab, hv, m)
	if err != nil {
		fail(err)
	}
	if st == nil && t == nil && len(d.nodes) == 0 && len(d.units) == 0 && len(d.bridges) == 0 {
		if st == nil {
			_ = ws.RemoveLab(lab) // only the lock we just created
		}
		return fmt.Errorf("nothing found for lab %q (no state in %s, no topology, nothing on the host)", lab, ws.LabDir(lab))
	}
	p := planDestroy(lab, t, st, d)

	log("==> BMCs")
	if m != nil {
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
	}

	log("==> Nodes and storage")
	if hv != nil {
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
		deleted, err := hv.DeletePool(p.pool.Name, p.pool.Path, ownsVolume(p.nodes))
		if err != nil {
			fail(err)
		} else if deleted {
			log("    pool       deleted %s (%s)", p.pool.Name, p.pool.Path)
		}
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
