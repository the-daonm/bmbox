package hypervisor

import (
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"
)

// MetadataNS tags domains created by bmbox so they are never confused with
// (or modified instead of) unrelated VMs on a shared hypervisor.
const (
	MetadataNS     = "https://bmbox.io/xmlns/libvirt/v1"
	metadataPrefix = "bmbox"
)

type NICSpec struct {
	MAC    string
	Bridge string
}

// NodeSpec is everything needed to render one node's domain XML. Paths are
// as seen by libvirtd.
type NodeSpec struct {
	Lab          string
	Node         string
	DomainName   string
	UUID         string
	CPUs         int
	MemoryBytes  uint64
	Disks        []string
	NICs         []NICSpec
	Boot         []string // "network" | "disk", in priority order
	Loader       string
	NVRAM        string
	VarsTemplate string
	SerialSocket string
	// SerialLog receives a copy of all serial output, so a console that
	// attaches mid-boot can show what came before.
	SerialLog string
}

// BuildDomainXML renders a q35 + OVMF machine that behaves like a server
// sitting powered off in a rack: UEFI with per-device boot order, PXE-capable
// virtio NICs and a serial console exposed on a UNIX socket.
func BuildDomainXML(s NodeSpec) (string, error) {
	port0 := uint(0)

	// Boot order is per device (not <os><boot dev>) so UEFI and the BMC
	// can reorder network vs disk without regenerating the domain. Like a
	// physical server, only the first NIC (the provisioning network)
	// network-boots: every extra NIC would add minutes of PXE/HTTP boot
	// timeouts when no provisioning server answers.
	var order uint
	bootOf := map[string][]*libvirtxml.DomainDeviceBoot{}
	for _, dev := range s.Boot {
		n := len(s.Disks)
		if dev == "network" {
			n = min(len(s.NICs), 1)
		}
		for i := 0; i < n; i++ {
			order++
			bootOf[dev] = append(bootOf[dev], &libvirtxml.DomainDeviceBoot{Order: order})
		}
	}
	boot := func(dev string, i int) *libvirtxml.DomainDeviceBoot {
		if i < len(bootOf[dev]) {
			return bootOf[dev][i]
		}
		return nil
	}

	var disks []libvirtxml.DomainDisk
	for i, path := range s.Disks {
		disks = append(disks, libvirtxml.DomainDisk{
			Device: "disk",
			Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "qcow2", Discard: "unmap"},
			Source: &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: path}},
			Target: &libvirtxml.DomainDiskTarget{Dev: "vd" + string(rune('a'+i)), Bus: "virtio"},
			Serial: fmt.Sprintf("%s-disk%d", s.Node, i),
			Boot:   boot("disk", i),
		})
	}

	var nics []libvirtxml.DomainInterface
	for i, nic := range s.NICs {
		nics = append(nics, libvirtxml.DomainInterface{
			MAC: &libvirtxml.DomainInterfaceMAC{Address: nic.MAC},
			Source: &libvirtxml.DomainInterfaceSource{
				Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: nic.Bridge},
			},
			Model: &libvirtxml.DomainInterfaceModel{Type: "virtio"},
			Boot:  boot("network", i),
		})
	}

	serialSrc := &libvirtxml.DomainChardevSource{
		UNIX: &libvirtxml.DomainChardevSourceUNIX{Mode: "bind", Path: s.SerialSocket},
	}

	nvram := &libvirtxml.DomainNVRam{NVRam: s.NVRAM, Template: s.VarsTemplate}

	dom := &libvirtxml.Domain{
		Type: "kvm",
		Name: s.DomainName,
		UUID: s.UUID,
		Metadata: &libvirtxml.DomainMetadata{XML: fmt.Sprintf(
			`<%s:node xmlns:%s=%q lab=%q name=%q/>`, metadataPrefix, metadataPrefix, MetadataNS, s.Lab, s.Node)},
		Memory:        &libvirtxml.DomainMemory{Value: uint(s.MemoryBytes), Unit: "bytes"},
		CurrentMemory: &libvirtxml.DomainCurrentMemory{Value: uint(s.MemoryBytes), Unit: "bytes"},
		VCPU:          &libvirtxml.DomainVCPU{Placement: "static", Value: uint(s.CPUs)},
		OS: &libvirtxml.DomainOS{
			Type:   &libvirtxml.DomainOSType{Arch: "x86_64", Machine: "q35", Type: "hvm"},
			Loader: &libvirtxml.DomainLoader{Path: s.Loader, Readonly: "yes", Secure: "no", Type: "pflash"},
			NVRam:  nvram,
		},
		Features: &libvirtxml.DomainFeatureList{
			ACPI: &libvirtxml.DomainFeature{},
			APIC: &libvirtxml.DomainFeatureAPIC{},
		},
		CPU:        &libvirtxml.DomainCPU{Mode: "host-passthrough", Check: "none"},
		Clock:      &libvirtxml.DomainClock{Offset: "utc"},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
		Devices: &libvirtxml.DomainDeviceList{
			Disks:      disks,
			Interfaces: nics,
			Serials: []libvirtxml.DomainSerial{{
				Source: serialSrc,
				Log:    serialLog(s.SerialLog),
				Target: &libvirtxml.DomainSerialTarget{Type: "isa-serial", Port: &port0},
			}},
			Consoles: []libvirtxml.DomainConsole{{
				Source: serialSrc,
				Target: &libvirtxml.DomainConsoleTarget{Type: "serial", Port: &port0},
			}},
			// Local-only VNC is kept for debugging firmware menus.
			Graphics: []libvirtxml.DomainGraphic{{
				VNC: &libvirtxml.DomainGraphicVNC{AutoPort: "yes", Listen: "127.0.0.1"},
			}},
			Videos: []libvirtxml.DomainVideo{{Model: libvirtxml.DomainVideoModel{Type: "virtio", Heads: 1}}},
			RNGs: []libvirtxml.DomainRNG{{
				Model:   "virtio",
				Backend: &libvirtxml.DomainRNGBackend{Random: &libvirtxml.DomainRNGBackendRandom{Device: "/dev/urandom"}},
			}},
			// Physical servers have no balloon device.
			MemBalloon: &libvirtxml.DomainMemBalloon{Model: "none"},
		},
	}
	return dom.Marshal()
}

func serialLog(path string) *libvirtxml.DomainChardevLog {
	if path == "" {
		return nil
	}
	// Truncated at every power-on, so it holds the current boot only.
	return &libvirtxml.DomainChardevLog{File: path}
}

// DefineResult reports what DefineNode did.
type DefineResult struct {
	UUID    string
	State   string
	Created bool
	// Skipped is true when the domain is running and was left untouched.
	Skipped bool
	XML     string
}

// DefineNode creates or updates a persistent domain without starting it, so
// the node waits in SHUTOFF like a racked server until its BMC powers it on.
func (c *Client) DefineNode(s NodeSpec) (*DefineResult, error) {
	res := &DefineResult{}

	dom, err := c.l.DomainLookupByName(s.DomainName)
	switch {
	case err == nil:
		owned, err := c.ownedBy(dom, s.Lab, s.Node)
		if err != nil {
			return nil, err
		}
		if !owned {
			return nil, fmt.Errorf("domain %s exists but was not created by bmbox for %s/%s; refusing to touch it", s.DomainName, s.Lab, s.Node)
		}
		state, err := c.domainState(dom)
		if err != nil {
			return nil, err
		}
		s.UUID = formatUUID(dom.UUID)
		res.UUID, res.State = s.UUID, state
		if state != "shutoff" {
			res.Skipped = true
			return res, nil
		}
	case libvirt.IsNotFound(err):
		res.Created = true
	default:
		return nil, fmt.Errorf("lookup domain %s: %w", s.DomainName, err)
	}

	xml, err := BuildDomainXML(s)
	if err != nil {
		return nil, err
	}
	dom, err = c.l.DomainDefineXMLFlags(xml, libvirt.DomainDefineValidate)
	if err != nil {
		return nil, fmt.Errorf("define domain %s: %w", s.DomainName, err)
	}
	// Return libvirt's normalized definition (with PCI addresses, emulator…).
	if res.XML, err = c.l.DomainGetXMLDesc(dom, libvirt.DomainXMLInactive); err != nil {
		return nil, err
	}
	res.UUID = formatUUID(dom.UUID)
	res.State, err = c.domainState(dom)
	return res, err
}

func (c *Client) ownedBy(dom libvirt.Domain, lab, node string) (bool, error) {
	l, n, err := c.nodeMeta(dom)
	return err == nil && l == lab && n == node, err
}

// nodeMeta reads the lab/node a domain was created for; empty when the
// domain carries no bmbox metadata.
func (c *Client) nodeMeta(dom libvirt.Domain) (lab, node string, err error) {
	md, err := c.l.DomainGetMetadata(dom, int32(libvirt.DomainMetadataElement), libvirt.OptString{MetadataNS}, libvirt.DomainAffectConfig)
	if isErr(err, libvirt.ErrNoDomainMetadata) || isErr(err, libvirt.ErrNoDomain) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var m struct {
		Lab  string `xml:"lab,attr"`
		Name string `xml:"name,attr"`
	}
	if err := xml.Unmarshal([]byte(md), &m); err != nil {
		return "", "", nil
	}
	return m.Lab, m.Name, nil
}

// LabNodes finds every node of a lab that has a domain, by its ownership
// metadata, so nodes no longer in the topology or state are found too.
func (c *Client) LabNodes(lab string) ([]string, error) {
	doms, _, err := c.l.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	var nodes []string
	for _, d := range doms {
		l, n, err := c.nodeMeta(d)
		if err != nil {
			return nil, err
		}
		if l == lab && n != "" {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

func (c *Client) domainState(dom libvirt.Domain) (string, error) {
	st, _, err := c.l.DomainGetState(dom, 0)
	if err != nil {
		return "", err
	}
	return stateName(libvirt.DomainState(st)), nil
}

func stateName(s libvirt.DomainState) string {
	switch s {
	case libvirt.DomainRunning:
		return "running"
	case libvirt.DomainBlocked:
		return "blocked"
	case libvirt.DomainPaused:
		return "paused"
	case libvirt.DomainShutdown:
		return "shutting-down"
	case libvirt.DomainShutoff:
		return "shutoff"
	case libvirt.DomainCrashed:
		return "crashed"
	case libvirt.DomainPmsuspended:
		return "pmsuspended"
	}
	return "unknown"
}

func formatUUID(u libvirt.UUID) string {
	h := hex.EncodeToString(u[:])
	return strings.Join([]string{h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]}, "-")
}
