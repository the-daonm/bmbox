// Package topology defines the declarative bmbox lab description
// (topology.yaml), its defaults and its validation rules.
package topology

const (
	APIVersion = "bmbox.io/v1alpha1"
	Kind       = "Topology"
)

// Topology is the root document of a topology.yaml file.
type Topology struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	// Name identifies the lab. It prefixes every resource bmbox creates
	// (bridges, storage pool, libvirt domains).
	Name string `yaml:"name"`
}

type Spec struct {
	Defaults NodeDefaults `yaml:"defaults"`
	Storage  Storage      `yaml:"storage"`
	UEFI     UEFI         `yaml:"uefi"`
	Networks []Network    `yaml:"networks"`
	Nodes    []Node       `yaml:"nodes"`
}

// NodeDefaults are applied to every node that leaves the field unset.
type NodeDefaults struct {
	CPUs   int      `yaml:"cpus"`
	Memory Size     `yaml:"memory"`
	Disks  []Disk   `yaml:"disks"`
	Boot   []string `yaml:"boot"`
}

// Storage selects where node disks and NVRAM files live. Path is the
// directory as seen by libvirtd (which may run in a container), not
// necessarily by the bmbox process.
type Storage struct {
	Path string `yaml:"path"`
}

// UEFI optionally pins the firmware images. When empty, bmbox asks libvirt
// (domain capabilities) which OVMF loader it supports.
type UEFI struct {
	Loader       string `yaml:"loader"`
	VarsTemplate string `yaml:"varsTemplate"`
}

type Network struct {
	Name string `yaml:"name"`
	// Bridge is the Linux bridge name. Generated from the lab and network
	// names when empty.
	Bridge string `yaml:"bridge"`
	// External attaches nodes to an existing bridge that bmbox neither
	// creates, reconfigures nor deletes (e.g. an Ironic provisioning bridge).
	External bool `yaml:"external"`
	// CIDR, when set, assigns the first host address to the bridge so the
	// host can act as gateway / provisioning server on that segment.
	CIDR string `yaml:"cidr"`
	MTU  int    `yaml:"mtu"`
}

type Node struct {
	Name   string   `yaml:"name"`
	CPUs   int      `yaml:"cpus"`
	Memory Size     `yaml:"memory"`
	Disks  []Disk   `yaml:"disks"`
	NICs   []NIC    `yaml:"nics"`
	Boot   []string `yaml:"boot"`
	BMC    *BMC     `yaml:"bmc"`
}

type Disk struct {
	Size Size `yaml:"size"`
}

type NIC struct {
	Network string `yaml:"network"`
	// MAC is generated deterministically from lab/node/index when empty.
	MAC string `yaml:"mac"`
}

// BMC describes the out-of-band controller emulated for the node. Each node
// gets its own endpoint, like the dedicated BMC of a physical server.
type BMC struct {
	Type string `yaml:"type"` // redfish | ipmi
	// Address the BMC listens on. Defaults to 127.0.0.1 so power control is
	// not exposed on a shared host unless asked for.
	Address string `yaml:"address"`
	// Port is assigned from DefaultRedfishPort / DefaultIPMIPort when empty.
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

const (
	BootNetwork = "network"
	BootDisk    = "disk"

	BMCRedfish = "redfish"
	BMCIPMI    = "ipmi"
)
