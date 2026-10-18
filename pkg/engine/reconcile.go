package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"bmbox/pkg/hypervisor"
	"bmbox/pkg/network"
	"bmbox/pkg/systemd"
	"bmbox/pkg/topology"
)

// orphans are resources bmbox created for the lab that the topology no
// longer declares (a node, network, BMC or disk removed from the file).
type orphans struct {
	nodes   []string
	units   map[string]string // node -> unit of a BMC no longer declared
	volumes []string
	bridges map[string]string // network -> bridge
	rules   map[string]string // network -> bridge of a FORWARD rule
	dhcp    string            // dnsmasq unit of a lab with no DHCP network left
}

func (o orphans) empty() bool {
	return len(o.nodes) == 0 && len(o.units) == 0 && len(o.volumes) == 0 && len(o.bridges) == 0 &&
		len(o.rules) == 0 && o.dhcp == ""
}

// findOrphans compares what is discovered on the host with the topology.
func findOrphans(t *topology.Topology, d discovered, poolVolumes []string) orphans {
	o := orphans{units: map[string]string{}, bridges: map[string]string{}, rules: map[string]string{}}
	declared, withBMC, expected := map[string]bool{}, map[string]bool{}, map[string]bool{}
	nodes := append([]string(nil), d.nodes...)
	for _, n := range t.Spec.Nodes {
		declared[n.Name] = true
		withBMC[n.Name] = n.BMC != nil
		for i := range n.Disks {
			expected[DiskVolumeName(n.Name, i)] = true
		}
		expected[NVRAMVolumeName(n.Name)] = true
		nodes = append(nodes, n.Name)
	}
	for _, n := range d.nodes {
		if !declared[n] {
			o.nodes = append(o.nodes, n)
		}
	}
	for n, u := range d.units {
		if !withBMC[n] {
			o.units[n] = u
			nodes = append(nodes, n)
		}
	}
	owned := ownsVolume(nodes)
	for _, v := range poolVolumes {
		if owned(v) && !expected[v] {
			o.volumes = append(o.volumes, v)
		}
	}
	for n, br := range d.bridges {
		if t.Network(n) == nil {
			o.bridges[n] = br
		}
	}
	for n, br := range d.rules {
		if net := t.Network(n); net == nil || net.External || net.Bridge != br {
			o.rules[n] = br
		}
	}
	if d.dhcp != "" && len(t.DHCPNetworks()) == 0 {
		o.dhcp = d.dhcp
	}
	sort.Strings(o.nodes)
	sort.Strings(o.volumes)
	return o
}

// stopNetboot stops the lab's dnsmasq unit (if any) and removes its config,
// leases and TFTP root.
func stopNetboot(ctx context.Context, sd *systemd.Manager, lab, unit string) error {
	if unit != "" {
		if err := sd.Stop(ctx, unit); err != nil {
			return err
		}
	}
	return os.RemoveAll(NetbootDir(lab))
}

// reconcile reports orphans, and removes them when opt.Prune is set.
func reconcile(ctx context.Context, t *topology.Topology, hv *hypervisor.Client, sd *systemd.Manager,
	opt Options, log func(string, ...any)) error {
	lab := t.Metadata.Name
	d, err := discover(ctx, lab, hv, sd)
	if err != nil {
		return err
	}
	vols, err := hv.PoolVolumes(t.PoolName())
	if err != nil {
		return err
	}
	o := findOrphans(t, d, vols)
	if o.empty() {
		return nil
	}

	if !opt.Prune {
		log("==> Not in topology any more (kept; run 'bmbox up --prune' to remove)")
		for _, n := range o.nodes {
			log("    node    %s (%s)", n, t.DomainName(n))
		}
		for n, u := range o.units {
			log("    bmc     %s (%s)", n, u)
		}
		for _, v := range o.volumes {
			log("    volume  %s", v)
		}
		for n, br := range o.bridges {
			log("    network %s (%s)", n, br)
		}
		for n, br := range o.rules {
			log("    firewall rule for %s (%s)", n, br)
		}
		if o.dhcp != "" {
			log("    dhcp    %s", o.dhcp)
		}
		return nil
	}

	log("==> Pruning resources no longer in topology")
	var errs []error
	if o.dhcp != "" {
		if err := stopNetboot(ctx, sd, lab, o.dhcp); err != nil {
			errs = append(errs, err)
		} else {
			log("    dhcp    %s stopped", o.dhcp)
		}
	}
	for n, u := range o.units {
		if err := sd.Stop(ctx, u); err != nil {
			errs = append(errs, err)
			continue
		}
		log("    bmc     %s stopped", n)
	}
	for _, n := range o.nodes {
		if _, err := hv.DeleteNode(lab, n, t.DomainName(n)); err != nil {
			errs = append(errs, err)
			continue
		}
		log("    node    %s undefined", n)
	}
	if err := hv.DeleteVolumes(t.PoolName(), o.volumes); err != nil {
		errs = append(errs, err)
	} else {
		for _, v := range o.volumes {
			log("    volume  %s deleted", v)
		}
	}
	for n, br := range o.bridges {
		if _, err := network.DeleteBridge(br, lab+"/"+n); err != nil {
			errs = append(errs, err)
			continue
		}
		log("    network %s deleted (%s)", n, br)
	}
	if len(o.rules) > 0 {
		removed, err := network.NewFirewall().Remove(func(owner string) bool {
			n, ok := strings.CutPrefix(owner, lab+"/")
			_, orphan := o.rules[n]
			return ok && orphan
		})
		errs = append(errs, err)
		for _, r := range removed {
			log("    firewall rule for %s removed", strings.TrimPrefix(r, lab+"/"))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	return nil
}
