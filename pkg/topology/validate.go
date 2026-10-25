package topology

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nameRE    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

const (
	maxLabNameLen  = 24
	maxNameLen     = 32
	minMemory      = 256 * MiB
	minDiskSize    = 1 * MiB
	maxCPUs        = 256
	maxDisksPerVM  = 8
	maxNICsPerVM   = 8
	minMTU, maxMTU = 68, 65535
	// maxIPMIPassword is what IPMI 2.0 carries (bmc.MaxIPMIPassword).
	maxIPMIPassword = 20
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

// reservedLabNames are entries of bmbox's data directory (/var/lib/bmbox)
// that are not labs: per-lab netboot data lives in /var/lib/bmbox/<lab>.
var reservedLabNames = map[string]bool{"labs": true, "tools": true, "ca": true}

func (v *validator) labName(path, s string) {
	v.name(path, s, maxLabNameLen)
	if reservedLabNames[s] {
		v.addf(path, "%q is reserved by bmbox, choose another name", s)
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

// ValidateLabName checks a lab name given outside a topology (e.g. --lab)
// with the rules of metadata.name. Lab names become path components, so
// this must pass before one is used.
func ValidateLabName(name string) error {
	v := &validator{}
	v.labName("lab name", name)
	if len(v.problems) > 0 {
		return fmt.Errorf("%s", v.problems[0])
	}
	return nil
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
	v.labName("metadata.name", t.Metadata.Name)

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
	t.validateNetboot(v)

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
		case n.Bridge == "" && n.External:
			v.addf(path+".bridge", "is required for an external network (name the existing bridge)")
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
		if n.DHCP != nil {
			validateDHCP(v, path, n)
		}
	}
}

func validateDHCP(v *validator, path string, n Network) {
	if n.CIDR == "" {
		v.addf(path+".dhcp", "needs cidr: bmbox serves DHCP from the network's gateway address")
		return
	}
	gw, err := ParseGateway(n.CIDR)
	if err != nil {
		return // reported on cidr
	}
	if !gw.Addr().Is4() {
		v.addf(path+".dhcp", "is IPv4 only, %s is not", n.CIDR)
		return
	}
	first, last, err := ParseRange(n.DHCP.Range)
	if err != nil {
		v.addf(path+".dhcp.range", "%v", err)
		return
	}
	lo, hi := hostBounds(gw)
	g := gw.Addr()
	switch {
	case first.Less(lo) || hi.Less(last):
		v.addf(path+".dhcp.range", "%s is not within the host addresses of %s (%s-%s)", n.DHCP.Range, n.CIDR, lo, hi)
	case !g.Less(first) && !last.Less(g):
		v.addf(path+".dhcp.range", "%s contains the gateway %s", n.DHCP.Range, g)
	}
}

func (t *Topology) validateNetboot(v *validator) {
	nb := t.Spec.Netboot
	if nb == nil {
		return
	}
	const p = "spec.netboot"
	if len(t.DHCPNetworks()) == 0 {
		v.addf(p, "needs a network with dhcp to boot from")
	}
	switch {
	case nb.Kernel == "" && nb.Script == "":
		v.addf(p, "set kernel (with optional initrd and cmdline) or script")
	case nb.Kernel != "" && nb.Script != "":
		v.addf(p, "kernel and script are mutually exclusive")
	case nb.Script != "" && (nb.Initrd != "" || nb.Cmdline != ""):
		v.addf(p, "initrd and cmdline only apply to kernel")
	}
	for _, f := range []struct{ name, val string }{{"kernel", nb.Kernel}, {"initrd", nb.Initrd}, {"script", nb.Script}} {
		if f.val != "" && !filepath.IsAbs(f.val) && !isURL(f.val) {
			v.addf(p+"."+f.name, "%q must be an absolute path or an http(s) URL", f.val)
		}
		if strings.ContainsAny(f.val, " \t\n") {
			v.addf(p+"."+f.name, "%q must not contain whitespace", f.val)
		}
	}
	if strings.ContainsAny(nb.Cmdline, "\n\r") {
		v.addf(p+".cmdline", "must be a single line")
	}
}

func (t *Topology) validateNodes(v *validator) {
	if len(t.Spec.Nodes) == 0 {
		v.addf("spec.nodes", "at least one node is required")
	}
	names := map[string]bool{}
	macs := map[string]string{}
	ips := map[netip.Addr]string{}
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
			if nic.IP != "" {
				t.validateNICAddress(v, p, nic, ips)
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
			if _, err := netip.ParseAddr(b.Address); err != nil {
				v.addf(path+".bmc.address", "%q is not a valid IP address", b.Address)
			}
			if b.Username == "" || strings.ContainsAny(b.Username, ": \t\n") {
				v.addf(path+".bmc.username", "%q must be non-empty without ':' or whitespace", b.Username)
			}
			set := 0
			for _, s := range []string{b.Password, b.PasswordFile, b.PasswordEnv} {
				if s != "" {
					set++
				}
			}
			if set > 1 {
				v.addf(path+".bmc", "set only one of password, passwordFile and passwordEnv")
			}
			if b.PasswordFile != "" && !filepath.IsAbs(b.PasswordFile) {
				v.addf(path+".bmc.passwordFile", "must be an absolute path, got %q", b.PasswordFile)
			}
			if b.PasswordEnv != "" && !envNameRE.MatchString(b.PasswordEnv) {
				v.addf(path+".bmc.passwordEnv", "%q is not a valid environment variable name", b.PasswordEnv)
			}
			if b.Type == BMCIPMI && b.UseTLS() {
				v.addf(path+".bmc.tls", "is for Redfish BMCs only (IPMI has no TLS)")
			}
			if b.Type == BMCIPMI && len(b.Password) > maxIPMIPassword {
				v.addf(path+".bmc.password", "is longer than the %d characters IPMI allows", maxIPMIPassword)
			}
			if b.Port < 1 || b.Port > 65535 {
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

// validateNICAddress checks a fixed address is a free host address of a
// DHCP network.
func (t *Topology) validateNICAddress(v *validator, p string, nic NIC, ips map[netip.Addr]string) {
	ip, err := netip.ParseAddr(nic.IP)
	if err != nil {
		v.addf(p+".ip", "%q is not a valid IP address", nic.IP)
		return
	}
	n := t.Network(nic.Network)
	if n == nil {
		return // reported on network
	}
	if n.DHCP == nil {
		v.addf(p+".ip", "needs dhcp on network %q (the address is handed out by bmbox's DHCP)", n.Name)
		return
	}
	gw, err := ParseGateway(n.CIDR)
	if err != nil {
		return
	}
	lo, hi := hostBounds(gw)
	switch {
	case !gw.Contains(ip) || ip.Less(lo) || hi.Less(ip):
		v.addf(p+".ip", "%s is not a host address of %s", ip, n.CIDR)
	case ip == gw.Addr():
		v.addf(p+".ip", "%s is the gateway of %s", ip, n.Name)
	default:
		if other, ok := ips[ip]; ok {
			v.addf(p+".ip", "%s already used by %s", ip, other)
		}
		ips[ip] = p
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

// ParseRange parses a DHCP range "first-last".
func ParseRange(r string) (first, last netip.Addr, err error) {
	a, b, ok := strings.Cut(r, "-")
	if ok {
		first, err = netip.ParseAddr(strings.TrimSpace(a))
		if err == nil {
			last, err = netip.ParseAddr(strings.TrimSpace(b))
		}
	}
	if !ok || err != nil {
		return first, last, fmt.Errorf("%q is not a range of the form FIRST-LAST", r)
	}
	if last.Less(first) {
		return first, last, fmt.Errorf("%q ends before it starts", r)
	}
	return first, last, nil
}

// hostBounds returns the first and last host address of an IPv4 prefix.
func hostBounds(p netip.Prefix) (first, last netip.Addr) {
	p = p.Masked()
	b := p.Addr().As4()
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	bcast := n | (1<<(32-p.Bits()) - 1)
	return p.Addr().Next(), netip.AddrFrom4([4]byte{byte(bcast >> 24), byte(bcast >> 16), byte(bcast >> 8), byte(bcast)}).Prev()
}

// defaultDHCPRange is the larger run of host addresses on either side of
// the gateway.
func defaultDHCPRange(cidr string) (first, last netip.Addr, ok bool) {
	gw, err := ParseGateway(cidr)
	if err != nil || !gw.Addr().Is4() {
		return first, last, false
	}
	lo, hi := hostBounds(gw)
	g := gw.Addr()
	below, above := addrDistance(lo, g), addrDistance(g, hi)
	if above >= below {
		return g.Next(), hi, above > 0
	}
	return lo, g.Prev(), true
}

func addrDistance(a, b netip.Addr) uint32 {
	x, y := a.As4(), b.As4()
	u := func(v [4]byte) uint32 { return uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3]) }
	return u(y) - u(x)
}

// isURL reports whether s is an http(s) URL iPXE can fetch.
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
