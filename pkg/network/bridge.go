// Package network creates the Linux bridges that connect lab nodes, using
// rtnetlink directly instead of shelling out to ip(8)/brctl(8).
package network

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// aliasPrefix marks bridges owned by bmbox (stored in IFLA_IFALIAS). bmbox
// never modifies or deletes a bridge that does not carry its alias.
const aliasPrefix = "bmbox:"

var sysClassNet = "/sys/class/net"

type BridgeSpec struct {
	Name string
	// Owner identifies the lab network, e.g. "demo/pxe".
	Owner    string
	MTU      int
	External bool
	// Gateway is assigned to the bridge when valid.
	Gateway netip.Prefix
}

type BridgeResult struct {
	Name     string         `json:"name"`
	Created  bool           `json:"created"`
	External bool           `json:"external"`
	Address  string         `json:"address,omitempty"`
	Sysctls  []SysctlChange `json:"sysctls,omitempty"`
}

func OwnerAlias(owner string) string { return aliasPrefix + owner }

// EnsureBridge makes the bridge exist with the requested configuration. It is
// idempotent: running it again on a bridge it created is a no-op.
func EnsureBridge(spec BridgeSpec) (*BridgeResult, error) {
	res := &BridgeResult{Name: spec.Name, External: spec.External}

	link, err := CheckBridge(spec)
	if err != nil {
		return nil, err
	}
	if spec.External {
		return res, nil
	}

	alias := OwnerAlias(spec.Owner)
	if link == nil {
		br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: spec.Name, MTU: spec.MTU}}
		if err := netlink.LinkAdd(br); err != nil {
			return nil, wrapPerm(fmt.Errorf("create bridge %s: %w", spec.Name, err))
		}
		res.Created = true
		if link, err = netlink.LinkByName(spec.Name); err != nil {
			return nil, err
		}
		if err := netlink.LinkSetAlias(link, alias); err != nil {
			_ = netlink.LinkDel(link)
			return nil, fmt.Errorf("tag bridge %s: %w", spec.Name, err)
		}
	}

	if err := configureBridge(link, spec, res); err != nil {
		if res.Created {
			_ = netlink.LinkDel(link)
		}
		return nil, err
	}
	return res, nil
}

// CheckBridge verifies, without changing anything, that the bridge can be
// created or reused: the name is free or already owned by this lab network,
// and the gateway subnet does not clash with an address on another
// interface. It returns the existing link, or nil if it must be created.
func CheckBridge(spec BridgeSpec) (netlink.Link, error) {
	link, err := netlink.LinkByName(spec.Name)
	var notFound netlink.LinkNotFoundError
	switch {
	case errors.As(err, &notFound):
		link = nil
	case err != nil:
		return nil, fmt.Errorf("lookup %s: %w", spec.Name, err)
	}

	if spec.External {
		if link == nil {
			return nil, fmt.Errorf("external bridge %q does not exist", spec.Name)
		}
		if link.Type() != "bridge" {
			return nil, fmt.Errorf("external interface %q is a %s, not a Linux bridge", spec.Name, link.Type())
		}
		return link, nil
	}

	if link != nil && (link.Type() != "bridge" || link.Attrs().Alias != OwnerAlias(spec.Owner)) {
		return nil, fmt.Errorf("interface %q already exists and is not managed by bmbox for %s (type=%s alias=%q); "+
			"pick another bridge name or mark the network external", spec.Name, spec.Owner, link.Type(), link.Attrs().Alias)
	}

	if spec.Gateway.IsValid() {
		addrs, err := netlink.AddrList(nil, netlink.FAMILY_ALL)
		if err != nil {
			return nil, fmt.Errorf("list host addresses: %w", err)
		}
		for _, a := range addrs {
			if link != nil && a.LinkIndex == link.Attrs().Index {
				continue
			}
			ip, ok := netip.AddrFromSlice(a.IP)
			if !ok {
				continue
			}
			ones, _ := a.Mask.Size()
			host := netip.PrefixFrom(ip.Unmap(), ones)
			if host.Overlaps(spec.Gateway.Masked()) {
				other := fmt.Sprintf("ifindex %d", a.LinkIndex)
				if l, err := netlink.LinkByIndex(a.LinkIndex); err == nil {
					other = l.Attrs().Name
				}
				return nil, fmt.Errorf("subnet %s of %s overlaps %s already configured on %s",
					spec.Gateway.Masked(), spec.Name, host, other)
			}
		}
	}
	return link, nil
}

func configureBridge(link netlink.Link, spec BridgeSpec, res *BridgeResult) error {
	if link.Attrs().MTU != spec.MTU {
		if err := netlink.LinkSetMTU(link, spec.MTU); err != nil {
			return fmt.Errorf("set MTU on %s: %w", spec.Name, err)
		}
	}

	// A lab segment behaves like a dumb switch: no STP, ports forward
	// immediately so PXE DHCP is not lost during the listening phase.
	for knob, val := range map[string]string{"stp_state": "0", "forward_delay": "0"} {
		p := filepath.Join(sysClassNet, spec.Name, "bridge", knob)
		if err := os.WriteFile(p, []byte(val), 0o644); err != nil {
			return fmt.Errorf("set %s on %s: %w", knob, spec.Name, err)
		}
	}

	// Avoid IPv6 SLAAC/RA chatter on IPv4-only segments.
	disableV6 := "1"
	if spec.Gateway.IsValid() && spec.Gateway.Addr().Is6() {
		disableV6 = "0"
	}
	c, err := EnsureSysctl("net/ipv6/conf/"+spec.Name+"/disable_ipv6", disableV6)
	if err != nil {
		return err
	}
	if c != nil {
		res.Sysctls = append(res.Sysctls, *c)
	}

	if spec.Gateway.IsValid() {
		addr := &netlink.Addr{IPNet: &net.IPNet{
			IP:   spec.Gateway.Addr().AsSlice(),
			Mask: net.CIDRMask(spec.Gateway.Bits(), spec.Gateway.Addr().BitLen()),
		}}
		if err := netlink.AddrReplace(link, addr); err != nil {
			return fmt.Errorf("assign %s to %s: %w", spec.Gateway, spec.Name, err)
		}
		res.Address = spec.Gateway.String()
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up %s: %w", spec.Name, err)
	}
	return nil
}

// DeleteBridge removes a bridge only if bmbox owns it for the given owner.
func DeleteBridge(name, owner string) error {
	link, err := netlink.LinkByName(name)
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if link.Attrs().Alias != OwnerAlias(owner) {
		return fmt.Errorf("refusing to delete %s: not owned by bmbox for %s", name, owner)
	}
	return wrapPerm(netlink.LinkDel(link))
}

func wrapPerm(err error) error {
	if errors.Is(err, unix.EPERM) {
		return fmt.Errorf("%w (CAP_NET_ADMIN required: run bmbox with sudo)", err)
	}
	return err
}
