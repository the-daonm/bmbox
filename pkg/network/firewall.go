package network

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ForwardChain holds bmbox's rules in the filter table. FORWARD jumps to
// it first, while any lab has a rule.
const ForwardChain = "BMBOX-FORWARD"

// Firewall keeps traffic between the ports of a lab bridge flowing when
// br_netfilter is loaded (bridge-nf-call-iptables=1, e.g. by Docker or
// Neutron): bridged frames then traverse the iptables FORWARD chain, whose
// policy may be DROP. The per-bridge nf_call_iptables knob cannot help: the
// kernel ORs it with the host-wide setting. So, like libvirt, bmbox accepts
// "-i <bridge> -o <bridge>" for its own bridges, in IPv4 and IPv6, through
// the host's own iptables front-ends (legacy or nft) so its rules land in
// the same FORWARD chain as the host's.
//
// Rules carry the network's owner ("bmbox:<lab>/<net>") as comment, which
// is how they are found again.
type Firewall struct {
	bins []string // iptables, ip6tables: those found
	run  func(bin string, args ...string) (string, error)
}

// NewFirewall finds iptables and ip6tables. A host without them has no
// iptables FORWARD chain to get past.
func NewFirewall() *Firewall {
	f := &Firewall{run: runIPTables}
	for _, b := range []string{"iptables", "ip6tables"} {
		if p, err := exec.LookPath(b); err == nil {
			f.bins = append(f.bins, p)
		}
	}
	return f
}

func runIPTables(bin string, args ...string) (string, error) {
	out, err := exec.Command(bin, append([]string{"-w", "-t", "filter"}, args...)...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Tools names the front-ends in use, e.g. "iptables, ip6tables".
func (f *Firewall) Tools() string {
	var names []string
	for _, b := range f.bins {
		names = append(names, b[strings.LastIndex(b, "/")+1:])
	}
	return strings.Join(names, ", ")
}

func (f *Firewall) Available() bool { return len(f.bins) > 0 }

func bridgeRule(owner, bridge string) []string {
	return []string{"-i", bridge, "-o", bridge, "-m", "comment", "--comment", OwnerAlias(owner), "-j", "ACCEPT"}
}

// AllowBridge makes sure traffic within bridge is accepted. It reports
// whether a rule had to be added.
func (f *Firewall) AllowBridge(owner, bridge string) (added bool, err error) {
	for _, bin := range f.bins {
		if _, err := f.run(bin, "-S", ForwardChain); err != nil {
			if _, err := f.run(bin, "-N", ForwardChain); err != nil {
				return added, err
			}
		}
		if _, err := f.run(bin, "-C", "FORWARD", "-j", ForwardChain); err != nil {
			if _, err := f.run(bin, "-I", "FORWARD", "1", "-j", ForwardChain); err != nil {
				return added, err
			}
		}
		rule := bridgeRule(owner, bridge)
		if _, err := f.run(bin, append([]string{"-C", ForwardChain}, rule...)...); err == nil {
			continue
		}
		if _, err := f.run(bin, append([]string{"-A", ForwardChain}, rule...)...); err != nil {
			return added, err
		}
		added = true
	}
	return added, nil
}

// LabRules finds the bridges with rules of a lab, keyed by network name.
func (f *Firewall) LabRules(lab string) (map[string]string, error) {
	out := map[string]string{}
	for _, bin := range f.bins {
		rules, err := f.rules(bin)
		if err != nil {
			return out, err
		}
		for _, r := range rules {
			if net, ok := strings.CutPrefix(r.owner, lab+"/"); ok {
				out[net] = r.bridge
			}
		}
	}
	return out, nil
}

// Remove deletes the rules of the owners for which match is true, then the
// chain and its jump once no rule is left. It returns the owners removed.
func (f *Firewall) Remove(match func(owner string) bool) ([]string, error) {
	seen := map[string]bool{}
	var removed []string
	var errs []error
	for _, bin := range f.bins {
		rules, err := f.rules(bin)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		left := len(rules)
		for _, r := range rules {
			if r.owner == "" || !match(r.owner) {
				continue
			}
			if _, err := f.run(bin, append([]string{"-D", ForwardChain}, r.args...)...); err != nil {
				errs = append(errs, err)
				continue
			}
			left--
			if !seen[r.owner] {
				seen[r.owner] = true
				removed = append(removed, r.owner)
			}
		}
		if left == 0 {
			errs = append(errs, f.removeChain(bin))
		}
	}
	return removed, errors.Join(errs...)
}

// removeChain drops the jump and the (empty) chain, if present.
func (f *Firewall) removeChain(bin string) error {
	for {
		if _, err := f.run(bin, "-C", "FORWARD", "-j", ForwardChain); err != nil {
			break
		}
		if _, err := f.run(bin, "-D", "FORWARD", "-j", ForwardChain); err != nil {
			return err
		}
	}
	if _, err := f.run(bin, "-S", ForwardChain); err != nil {
		return nil // no chain
	}
	_, err := f.run(bin, "-X", ForwardChain)
	return err
}

type fwRule struct {
	owner  string // "<lab>/<net>"
	bridge string
	args   []string // rule spec without "-A <chain>"
}

// rules lists the chain's rules; a missing chain has none. Anything that
// is not a bmbox bridge rule is counted but never matched.
func (f *Firewall) rules(bin string) ([]fwRule, error) {
	out, err := f.run(bin, "-S", ForwardChain)
	if err != nil {
		return nil, nil
	}
	var rules []fwRule
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "-A" || fields[1] != ForwardChain {
			continue
		}
		// -S quotes comments; bmbox's never contain spaces, so dropping the
		// quotes gives back the arguments the rule was created with.
		r := fwRule{}
		for _, f := range fields[2:] {
			r.args = append(r.args, strings.Trim(f, `"`))
		}
		for i := 0; i+1 < len(r.args); i++ {
			switch r.args[i] {
			case "--comment":
				if owner, ok := strings.CutPrefix(r.args[i+1], aliasPrefix); ok {
					r.owner = owner
				}
			case "-i":
				r.bridge = r.args[i+1]
			}
		}
		rules = append(rules, r)
	}
	return rules, nil
}
