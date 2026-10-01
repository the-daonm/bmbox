package topology

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

const (
	maxLabNameLen  = 24
	maxNameLen     = 32
	minMemory      = 256 * MiB
	minDiskSize    = 1 * MiB
	maxCPUs        = 256
	maxDisksPerVM  = 8
	maxNICsPerVM   = 8
	minMTU, maxMTU = 68, 65535
)

// ValidationError collects every problem found in a topology so the user can
// fix them in one pass.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid topology (%d problem(s)):\n  - %s",
		len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

type validator struct{ problems []string }

func (v *validator) addf(path, format string, args ...any) {
	v.problems = append(v.problems, path+": "+fmt.Sprintf(format, args...))
}

func (v *validator) name(path, s string, maxLen int) {
	switch {
	case s == "":
		v.addf(path, "is required")
	case len(s) > maxLen:
		v.addf(path, "%q is longer than %d characters", s, maxLen)
	case !nameRE.MatchString(s):
		v.addf(path, "%q must be lowercase alphanumerics and '-', not starting or ending with '-'", s)
	}
}

func (v *validator) boot(path string, boot []string) {
	seen := map[string]bool{}
	for i, b := range boot {
		p := fmt.Sprintf("%s[%d]", path, i)
		if b != BootNetwork && b != BootDisk {
			v.addf(p, "unknown boot device %q (want %q or %q)", b, BootNetwork, BootDisk)
		}
		if seen[b] {
			v.addf(p, "duplicate boot device %q", b)
		}
		seen[b] = true
	}
}

// Validate checks a defaulted topology. Call ApplyDefaults first.
func (t *Topology) Validate() error {
	v := &validator{}

	if t.APIVersion != APIVersion {
		v.addf("apiVersion", "must be %q, got %q", APIVersion, t.APIVersion)
	}
	if t.Kind != Kind {
		v.addf("kind", "must be %q, got %q", Kind, t.Kind)
	}
	v.name("metadata.name", t.Metadata.Name, maxLabNameLen)

	if !filepath.IsAbs(t.Spec.Storage.Path) {
		v.addf("spec.storage.path", "must be an absolute path, got %q", t.Spec.Storage.Path)
	}
	if p := t.Spec.UEFI.Loader; p != "" && !filepath.IsAbs(p) {
		v.addf("spec.uefi.loader", "must be an absolute path, got %q", p)
	}
	if p := t.Spec.UEFI.VarsTemplate; p != "" && !filepath.IsAbs(p) {
		v.addf("spec.uefi.varsTemplate", "must be an absolute path, got %q", p)
	}
	v.boot("spec.defaults.boot", t.Spec.Defaults.Boot)

	t.validateNetworks(v)
	t.validateNodes(v)

	if len(v.problems) > 0 {
		return &ValidationError{Problems: v.problems}
	}
	return nil
}

func (t *Topology) validateNetworks(v *validator) {
	names := map[string]bool{}
	bridges := map[string]string{}
	var prefixes []struct {
		path string
		p    netip.Prefix
	}

	for i, n := range t.Spec.Networks {
		path := fmt.Sprintf("spec.networks[%d]", i)
		v.name(path+".name", n.Name, maxNameLen)
		if names[n.Name] {
			v.addf(path+".name", "duplicate network %q", n.Name)
		}
		names[n.Name] = true

		switch {
		case n.Bridge == "":
			v.addf(path+".bridge", "is required")
		case len(n.Bridge) > MaxIfNameLen:
			v.addf(path+".bridge", "%q exceeds the Linux limit of %d characters", n.Bridge, MaxIfNameLen)
		case strings.ContainsAny(n.Bridge, "/: \t") || n.Bridge == "." || n.Bridge == "..":
			v.addf(path+".bridge", "%q is not a valid interface name", n.Bridge)
		}
		if other, ok := bridges[n.Bridge]; ok {
			v.addf(path+".bridge", "bridge %q already used by network %q", n.Bridge, other)
		}
		bridges[n.Bridge] = n.Name

		if n.MTU < minMTU || n.MTU > maxMTU {
			v.addf(path+".mtu", "%d is out of range [%d, %d]", n.MTU, minMTU, maxMTU)
		}

		if n.CIDR != "" {
			if n.External {
				v.addf(path+".cidr", "cannot be set on an external bridge (bmbox does not configure it)")
			}
			if _, err := ParseGateway(n.CIDR); err != nil {
				v.addf(path+".cidr", "%v", err)
			} else {
				p := netip.MustParsePrefix(n.CIDR).Masked()
				for _, o := range prefixes {
					if o.p.Overlaps(p) {
						v.addf(path+".cidr", "%s overlaps %s (%s)", p, o.p, o.path)
					}
				}
				prefixes = append(prefixes, struct {
					path string
					p    netip.Prefix
				}{path + ".cidr", p})
			}
		}
	}
}

func (t *Topology) validateNodes(v *validator) {
	if len(t.Spec.Nodes) == 0 {
		v.addf("spec.nodes", "at least one node is required")
	}
	names := map[string]bool{}
	macs := map[string]string{}
	bmcPorts := map[int]string{}

	for i, n := range t.Spec.Nodes {
		path := fmt.Sprintf("spec.nodes[%d]", i)
		v.name(path+".name", n.Name, maxNameLen)
		if names[n.Name] {
			v.addf(path+".name", "duplicate node %q", n.Name)
		}
		names[n.Name] = true

		if n.CPUs < 1 || n.CPUs > maxCPUs {
			v.addf(path+".cpus", "%d is out of range [1, %d]", n.CPUs, maxCPUs)
		}
		if n.Memory < minMemory {
			v.addf(path+".memory", "%s is below the minimum of %s", n.Memory, minMemory)
		}

		if len(n.Disks) > maxDisksPerVM {
			v.addf(path+".disks", "at most %d disks are supported", maxDisksPerVM)
		}
		for j, d := range n.Disks {
			if d.Size < minDiskSize {
				v.addf(fmt.Sprintf("%s.disks[%d].size", path, j), "%s is below the minimum of %s", d.Size, minDiskSize)
			}
		}

		if len(n.NICs) > maxNICsPerVM {
			v.addf(path+".nics", "at most %d NICs are supported", maxNICsPerVM)
		}
		for j, nic := range n.NICs {
			p := fmt.Sprintf("%s.nics[%d]", path, j)
			if nic.Network == "" {
				v.addf(p+".network", "is required")
			} else if t.Network(nic.Network) == nil {
				v.addf(p+".network", "unknown network %q", nic.Network)
			}
			hw, err := net.ParseMAC(nic.MAC)
			switch {
			case err != nil || len(hw) != 6:
				v.addf(p+".mac", "%q is not a valid 48-bit MAC address", nic.MAC)
			case hw[0]&1 == 1:
				v.addf(p+".mac", "%q is a multicast address", nic.MAC)
			default:
				key := hw.String()
				if other, ok := macs[key]; ok {
					v.addf(p+".mac", "%s already used by %s", key, other)
				}
				macs[key] = p
			}
		}

		v.boot(path+".boot", n.Boot)
		for _, b := range n.Boot {
			if b == BootNetwork && len(n.NICs) == 0 {
				v.addf(path+".boot", "boots from network but has no NICs")
			}
			if b == BootDisk && len(n.Disks) == 0 {
				v.addf(path+".boot", "boots from disk but has no disks")
			}
		}

		if b := n.BMC; b != nil {
			if b.Type != BMCRedfish && b.Type != BMCIPMI {
				v.addf(path+".bmc.type", "unknown BMC type %q (want %q or %q)", b.Type, BMCRedfish, BMCIPMI)
			}
			if b.Type == BMCIPMI && b.Port == 0 {
				v.addf(path+".bmc.port", "is required for IPMI")
			}
			if b.Port < 0 || b.Port > 65535 {
				v.addf(path+".bmc.port", "%d is not a valid port", b.Port)
			}
			if b.Port != 0 {
				if other, ok := bmcPorts[b.Port]; ok {
					v.addf(path+".bmc.port", "port %d already used by node %q", b.Port, other)
				}
				bmcPorts[b.Port] = n.Name
			}
		}
	}
}

// ParseGateway interprets a network CIDR. "10.0.0.0/24" puts the gateway on
// the first host address (10.0.0.1/24); "10.0.0.254/24" uses that address.
func ParseGateway(cidr string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a valid CIDR", cidr)
	}
	if p.Bits() >= p.Addr().BitLen()-1 {
		return netip.Prefix{}, fmt.Errorf("%q is too small to host a gateway and nodes", cidr)
	}
	addr := p.Addr()
	if addr == p.Masked().Addr() {
		addr = addr.Next()
	}
	return netip.PrefixFrom(addr, p.Bits()), nil
}
