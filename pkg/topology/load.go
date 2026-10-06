package topology

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	DefaultCPUs        = 2
	DefaultMemory      = 4 * GiB
	DefaultDiskSize    = 20 * GiB
	DefaultMTU         = 1500
	DefaultStorageRoot = "/var/lib/libvirt/bmbox"
	DefaultBMCAddress  = "127.0.0.1"
	DefaultRedfishPort = 8300
	DefaultIPMIPort    = 6330

	// MaxIfNameLen is IFNAMSIZ-1 on Linux.
	MaxIfNameLen = 15
)

// Load reads, decodes (rejecting unknown fields), defaults and validates a
// topology file.
func Load(path string) (*Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func Parse(data []byte) (*Topology, error) {
	var t Topology
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("topology is empty")
		}
		return nil, fmt.Errorf("parse topology: %w", err)
	}
	t.ApplyDefaults()
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

// ApplyDefaults fills every unset field so later stages never need to
// special-case zero values.
func (t *Topology) ApplyDefaults() {
	d := &t.Spec.Defaults
	if d.CPUs == 0 {
		d.CPUs = DefaultCPUs
	}
	if d.Memory == 0 {
		d.Memory = DefaultMemory
	}
	if len(d.Disks) == 0 {
		d.Disks = []Disk{{Size: DefaultDiskSize}}
	}
	if len(d.Boot) == 0 {
		d.Boot = []string{BootNetwork, BootDisk}
	}
	if t.Spec.Storage.Path == "" && t.Metadata.Name != "" {
		t.Spec.Storage.Path = DefaultStorageRoot + "/" + t.Metadata.Name
	}

	for i := range t.Spec.Networks {
		n := &t.Spec.Networks[i]
		if n.Bridge == "" && n.Name != "" && !n.External {
			n.Bridge = defaultBridgeName(t.Metadata.Name, n.Name)
		}
		if n.MTU == 0 {
			n.MTU = DefaultMTU
		}
	}

	for i := range t.Spec.Nodes {
		n := &t.Spec.Nodes[i]
		if n.CPUs == 0 {
			n.CPUs = d.CPUs
		}
		if n.Memory == 0 {
			n.Memory = d.Memory
		}
		if len(n.Disks) == 0 {
			n.Disks = append([]Disk(nil), d.Disks...)
		}
		for j := range n.Disks {
			if n.Disks[j].Size == 0 {
				n.Disks[j].Size = DefaultDiskSize
			}
		}
		if len(n.Boot) == 0 {
			n.Boot = append([]string(nil), d.Boot...)
		}
		for j := range n.NICs {
			if n.NICs[j].MAC == "" {
				n.NICs[j].MAC = generateMAC(t.Metadata.Name, n.Name, j)
			}
		}
		if n.BMC != nil {
			if n.BMC.Type == "" {
				n.BMC.Type = BMCRedfish
			}
			if n.BMC.Address == "" {
				n.BMC.Address = DefaultBMCAddress
			}
			if n.BMC.Username == "" {
				n.BMC.Username = "admin"
			}
			if n.BMC.Password == "" {
				n.BMC.Password = "password"
			}
		}
	}
	t.assignBMCPorts()
}

// assignBMCPorts gives every BMC without an explicit port the next free one
// from its type's base, skipping ports claimed explicitly elsewhere.
func (t *Topology) assignBMCPorts() {
	used := map[int]bool{}
	for _, n := range t.Spec.Nodes {
		if n.BMC != nil && n.BMC.Port != 0 {
			used[n.BMC.Port] = true
		}
	}
	next := map[string]int{BMCRedfish: DefaultRedfishPort, BMCIPMI: DefaultIPMIPort}
	for i := range t.Spec.Nodes {
		b := t.Spec.Nodes[i].BMC
		if b == nil || b.Port != 0 {
			continue
		}
		p, ok := next[b.Type]
		if !ok {
			continue // unknown type, reported by Validate
		}
		for used[p] {
			p++
		}
		b.Port, used[p], next[b.Type] = p, true, p+1
	}
}

// BMCUnit is the systemd unit running a node's BMC.
func (t *Topology) BMCUnit(node string) string {
	return "bmbox-" + t.Metadata.Name + "-" + node + "-bmc.service"
}

// DomainName is the libvirt domain name of a node.
func (t *Topology) DomainName(node string) string {
	return "bmbox-" + t.Metadata.Name + "-" + node
}

// PoolName is the libvirt storage pool holding the lab's disks and NVRAM.
func (t *Topology) PoolName() string {
	return "bmbox-" + t.Metadata.Name
}

func (t *Topology) Network(name string) *Network {
	for i := range t.Spec.Networks {
		if t.Spec.Networks[i].Name == name {
			return &t.Spec.Networks[i]
		}
	}
	return nil
}

// defaultBridgeName returns "bmb-<lab>-<net>" when it fits in IFNAMSIZ,
// otherwise a stable hashed name.
func defaultBridgeName(lab, net string) string {
	if name := "bmb-" + lab + "-" + net; len(name) <= MaxIfNameLen {
		return name
	}
	sum := sha256.Sum256([]byte(lab + "/" + net))
	return fmt.Sprintf("bmb-%x", sum[:5])
}

// generateMAC derives a stable, locally administered QEMU-range MAC
// (52:54:00:xx:xx:xx) so re-running `up` never changes a node's identity.
func generateMAC(lab, node string, idx int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d", lab, node, idx)))
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", sum[0], sum[1], sum[2])
}
