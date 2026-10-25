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
	// Netboot is what nodes boot over the network from the lab's DHCP
	// networks. Without it, those networks only hand out addresses.
	Netboot *Netboot `yaml:"netboot"`
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
	// DHCP, when set, makes bmbox serve DHCP (and netboot) on the network
	// from its gateway address. Requires CIDR (IPv4).
	DHCP *DHCP `yaml:"dhcp"`
}

type DHCP struct {
	// Range of dynamic addresses, "first-last". Defaults to every host
	// address of the CIDR on the larger side of the gateway.
	Range string `yaml:"range"`
}

// Netboot is served over TFTP: UEFI firmware loads iPXE, which runs either
// a generated script booting Kernel/Initrd with Cmdline, or Script.
type Netboot struct {
	// Kernel and Initrd are absolute paths on the host (copied to the TFTP
	// root) or http(s):// URLs iPXE fetches directly.
	Kernel  string `yaml:"kernel"`
	Initrd  string `yaml:"initrd"`
	Cmdline string `yaml:"cmdline"`
	// Script is a custom iPXE script (absolute path), instead of Kernel.
	Script string `yaml:"script"`
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
	// IP is a fixed DHCP address for the NIC, on a network with dhcp.
	IP string `yaml:"ip"`
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
	// Password, when empty, is generated on the first `up` and kept; see
	// `bmbox credentials`. PasswordFile (absolute path) or PasswordEnv (a
	// variable bmbox reads at `up`) keep it out of the topology instead.
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"passwordFile"`
	PasswordEnv  string `yaml:"passwordEnv"`
}

const (
	BootNetwork = "network"
	BootDisk    = "disk"

	BMCRedfish = "redfish"
	BMCIPMI    = "ipmi"
)
