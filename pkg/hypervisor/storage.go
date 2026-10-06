package hypervisor

import (
	"bytes"
	"fmt"

	"github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"
)

// Pool is a directory storage pool dedicated to one lab. Using libvirt's
// storage API (instead of writing files ourselves) means the files are
// created in libvirtd's own filesystem view with the right ownership, which
// also works when libvirtd runs in a container (e.g. Kolla nova_libvirt).
type Pool struct {
	c    *Client
	p    libvirt.StoragePool
	Name string
	Path string
}

// EnsurePool defines, builds, starts and autostarts the pool if needed.
func (c *Client) EnsurePool(name, path string) (*Pool, error) {
	p, err := c.l.StoragePoolLookupByName(name)
	switch {
	case err == nil:
		raw, err := c.l.StoragePoolGetXMLDesc(p, 0)
		if err != nil {
			return nil, err
		}
		var def libvirtxml.StoragePool
		if err := def.Unmarshal(raw); err != nil {
			return nil, err
		}
		if def.Target == nil || def.Target.Path != path {
			return nil, fmt.Errorf("storage pool %s already exists with a different path; expected %s", name, path)
		}
	case isErr(err, libvirt.ErrNoStoragePool):
		def := libvirtxml.StoragePool{
			Type: "dir",
			Name: name,
			Target: &libvirtxml.StoragePoolTarget{
				Path:        path,
				Permissions: &libvirtxml.StoragePoolTargetPermissions{Mode: "0755"},
			},
		}
		xml, err := def.Marshal()
		if err != nil {
			return nil, err
		}
		if p, err = c.l.StoragePoolDefineXML(xml, 0); err != nil {
			return nil, fmt.Errorf("define storage pool %s: %w", name, err)
		}
		if err := c.l.StoragePoolBuild(p, libvirt.StoragePoolBuildNew); err != nil {
			return nil, fmt.Errorf("build storage pool %s: %w", name, err)
		}
		if err := c.l.StoragePoolSetAutostart(p, 1); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("lookup storage pool %s: %w", name, err)
	}

	active, err := c.l.StoragePoolIsActive(p)
	if err != nil {
		return nil, err
	}
	if active == 0 {
		// (Re)build first: the directory may be missing if an earlier run
		// stopped between define and build, or someone removed it.
		// Building a dir pool whose directory exists is a no-op.
		if err := c.l.StoragePoolBuild(p, libvirt.StoragePoolBuildNew); err != nil {
			return nil, fmt.Errorf("build storage pool %s: %w", name, err)
		}
		if err := c.l.StoragePoolCreate(p, 0); err != nil {
			return nil, fmt.Errorf("start storage pool %s: %w", name, err)
		}
	} else if err := c.l.StoragePoolRefresh(p, 0); err != nil {
		return nil, err
	}
	return &Pool{c: c, p: p, Name: name, Path: path}, nil
}

// Volume describes a file in the pool.
type Volume struct {
	Name    string
	Path    string
	Created bool
}

// EnsureDisk creates a sparse qcow2 volume (allocation 0) of the requested
// capacity. An existing volume is kept as long as its size matches, so a
// re-run never wipes an installed OS.
func (p *Pool) EnsureDisk(name string, capacity uint64) (*Volume, error) {
	v, err := p.lookup(name)
	if err != nil {
		return nil, err
	}
	if v != nil {
		_, gotCap, _, err := p.c.l.StorageVolGetInfo(v.vol)
		if err != nil {
			return nil, err
		}
		if gotCap != capacity {
			return nil, fmt.Errorf("volume %s exists with %d bytes but topology asks for %d; destroy the lab to resize", name, gotCap, capacity)
		}
		return &v.Volume, nil
	}

	def := libvirtxml.StorageVolume{
		Name:       name,
		Capacity:   &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: capacity},
		Allocation: &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: 0},
		Target: &libvirtxml.StorageVolumeTarget{
			Format: &libvirtxml.StorageVolumeTargetFormat{Type: "qcow2"},
		},
	}
	return p.create(def, nil)
}

// EnsureNVRAM gives a node its own copy of the UEFI variable store. An
// existing NVRAM is never overwritten: it holds the node's boot entries.
func (p *Pool) EnsureNVRAM(name string, template []byte) (*Volume, error) {
	v, err := p.lookup(name)
	if err != nil {
		return nil, err
	}
	if v != nil {
		return &v.Volume, nil
	}
	def := libvirtxml.StorageVolume{
		Name:     name,
		Capacity: &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: uint64(len(template))},
		Target: &libvirtxml.StorageVolumeTarget{
			Format: &libvirtxml.StorageVolumeTargetFormat{Type: "raw"},
		},
	}
	return p.create(def, template)
}

// VolumePath returns the path a volume will have, without creating it.
func (p *Pool) VolumePath(name string) string { return p.Path + "/" + name }

type foundVolume struct {
	Volume
	vol libvirt.StorageVol
}

func (p *Pool) lookup(name string) (*foundVolume, error) {
	vol, err := p.c.l.StorageVolLookupByName(p.p, name)
	if isErr(err, libvirt.ErrNoStorageVol) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup volume %s: %w", name, err)
	}
	path, err := p.c.l.StorageVolGetPath(vol)
	if err != nil {
		return nil, err
	}
	return &foundVolume{Volume: Volume{Name: name, Path: path}, vol: vol}, nil
}

func (p *Pool) create(def libvirtxml.StorageVolume, content []byte) (*Volume, error) {
	xml, err := def.Marshal()
	if err != nil {
		return nil, err
	}
	vol, err := p.c.l.StorageVolCreateXML(p.p, xml, 0)
	if err != nil {
		return nil, fmt.Errorf("create volume %s: %w", def.Name, err)
	}
	if content != nil {
		err := p.c.l.StorageVolUpload(vol, bytes.NewReader(content), 0, uint64(len(content)), 0)
		if err != nil {
			_ = p.c.l.StorageVolDelete(vol, 0)
			return nil, fmt.Errorf("upload %s: %w", def.Name, err)
		}
	}
	path, err := p.c.l.StorageVolGetPath(vol)
	if err != nil {
		return nil, err
	}
	return &Volume{Name: def.Name, Path: path, Created: true}, nil
}
