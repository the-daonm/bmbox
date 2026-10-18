package network

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeIPTables models the filter table well enough for Firewall.
type fakeIPTables struct {
	chains map[string][]string // chain -> rule specs
}

func newFake() *fakeIPTables {
	return &fakeIPTables{chains: map[string][]string{"FORWARD": {"-j DOCKER-USER"}}}
}

func (f *fakeIPTables) run(_ string, args ...string) (string, error) {
	fail := errors.New("iptables: " + strings.Join(args, " "))
	op, chain := args[0], args[1]
	rules, ok := f.chains[chain]
	spec := strings.Join(args[2:], " ")
	switch op {
	case "-S":
		if !ok {
			return "", fail
		}
		out := "-N " + chain + "\n"
		for _, r := range rules {
			// Like iptables, quote comments in the listing.
			fields := strings.Fields(r)
			for i := range fields {
				if i > 0 && fields[i-1] == "--comment" {
					fields[i] = `"` + fields[i] + `"`
				}
			}
			out += "-A " + chain + " " + strings.Join(fields, " ") + "\n"
		}
		return out, nil
	case "-N":
		if ok {
			return "", fail
		}
		f.chains[chain] = nil
	case "-X":
		if !ok || len(rules) > 0 {
			return "", fail
		}
		delete(f.chains, chain)
	case "-C":
		if !slices.Contains(rules, spec) {
			return "", fail
		}
	case "-A":
		f.chains[chain] = append(rules, spec)
	case "-I": // -I chain 1 spec
		f.chains[chain] = append([]string{strings.Join(args[3:], " ")}, rules...)
	case "-D":
		i := slices.Index(rules, spec)
		if i < 0 {
			return "", fail
		}
		f.chains[chain] = slices.Delete(rules, i, i+1)
	}
	return "", nil
}

func TestFirewallLifecycle(t *testing.T) {
	fake := newFake()
	fw := &Firewall{bins: []string{"/usr/sbin/iptables"}, run: fake.run}

	for i := 0; i < 2; i++ { // idempotent
		added, err := fw.AllowBridge("lab1/pxe", "bmb-lab1-pxe")
		if err != nil || added != (i == 0) {
			t.Fatalf("AllowBridge #%d: added=%v err=%v", i, added, err)
		}
	}
	if _, err := fw.AllowBridge("lab2/pxe", "bmb-lab2-pxe"); err != nil {
		t.Fatal(err)
	}
	fake.chains[ForwardChain] = append(fake.chains[ForwardChain], "-s 10.0.0.0/8 -j ACCEPT") // not ours
	if got := fake.chains["FORWARD"]; len(got) != 2 || got[0] != "-j "+ForwardChain {
		t.Errorf("FORWARD = %q, want the bmbox jump first", got)
	}
	if r, _ := fw.LabRules("lab1"); len(r) != 1 || r["pxe"] != "bmb-lab1-pxe" {
		t.Errorf("LabRules(lab1) = %v", r)
	}

	removed, err := fw.Remove(func(o string) bool { return strings.HasPrefix(o, "lab1/") })
	if err != nil || !slices.Equal(removed, []string{"lab1/pxe"}) {
		t.Fatalf("Remove lab1: %v %v", removed, err)
	}
	if len(fake.chains[ForwardChain]) != 2 {
		t.Errorf("chain after removing lab1 = %q", fake.chains[ForwardChain])
	}

	// The foreign rule keeps the chain alive; once it is gone, the last lab
	// takes the chain and the jump with it.
	fake.chains[ForwardChain] = fake.chains[ForwardChain][:1]
	if _, err := fw.Remove(func(o string) bool { return strings.HasPrefix(o, "lab2/") }); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.chains[ForwardChain]; ok {
		t.Error("empty chain not removed")
	}
	if got := fake.chains["FORWARD"]; !slices.Equal(got, []string{"-j DOCKER-USER"}) {
		t.Errorf("FORWARD = %q, want the host's rules only", got)
	}
}
