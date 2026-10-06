package hypervisor

import (
	"fmt"
	"strings"

	"github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"
)

// NodeState returns the power state of a lab node's domain, or "absent".
func (c *Client) NodeState(domainName string) (string, error) {
	dom, err := c.l.DomainLookupByName(domainName)
	if libvirt.IsNotFound(err) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	return c.domainState(dom)
}

// DeleteNode powers off and undefines a domain bmbox created for lab/node,
// including its snapshots metadata and NVRAM. A missing domain is fine; a
// domain not owned by bmbox is refused.
func (c *Client) DeleteNode(lab, node, domainName string) (deleted bool, err error) {
	dom, err := c.l.DomainLookupByName(domainName)
	if libvirt.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owned, err := c.ownedBy(dom, lab, node)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, fmt.Errorf("refusing to delete domain %s: not created by bmbox for %s/%s", domainName, lab, node)
	}
	if st, err := c.domainState(dom); err == nil && st != "shutoff" {
		if err := c.l.DomainDestroy(dom); err != nil && !libvirt.IsNotFound(err) {
			return false, fmt.Errorf("power off %s: %w", domainName, err)
		}
	}
	flags := libvirt.DomainUndefineManagedSave | libvirt.DomainUndefineSnapshotsMetadata |
		libvirt.DomainUndefineNvram | libvirt.DomainUndefineCheckpointsMetadata
	if err := c.l.DomainUndefineFlags(dom, flags); err != nil {
		return false, fmt.Errorf("undefine %s: %w", domainName, err)
	}
	return true, nil
}

// DeletePool removes the given volumes, then the pool and its directory.
// The pool is kept if it holds files bmbox did not create.
func (c *Client) DeletePool(name, path string, volumes []string) (deleted bool, err error) {
	p, err := c.l.StoragePoolLookupByName(name)
	if isErr(err, libvirt.ErrNoStoragePool) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := c.l.StoragePoolGetXMLDesc(p, 0)
	if err != nil {
		return false, err
	}
	var def libvirtxml.StoragePool
	if err := def.Unmarshal(raw); err != nil {
		return false, err
	}
	if def.Target == nil || def.Target.Path != path {
		return false, fmt.Errorf("refusing to delete pool %s: path is not %s", name, path)
	}

	active, err := c.l.StoragePoolIsActive(p)
	if err != nil {
		return false, err
	}
	if active == 0 {
		if err := c.l.StoragePoolCreate(p, 0); err != nil {
			return false, fmt.Errorf("start pool %s to clean it: %w", name, err)
		}
	}
	_ = c.l.StoragePoolRefresh(p, 0)

	for _, v := range volumes {
		vol, err := c.l.StorageVolLookupByName(p, v)
		if isErr(err, libvirt.ErrNoStorageVol) {
			continue // e.g. NVRAM already removed with the domain
		}
		if err != nil {
			return false, err
		}
		if err := c.l.StorageVolDelete(vol, 0); err != nil {
			return false, fmt.Errorf("delete volume %s: %w", v, err)
		}
	}

	left, _, err := c.l.StoragePoolListAllVolumes(p, 1, 0)
	if err != nil {
		return false, err
	}
	if len(left) > 0 {
		var names []string
		for _, v := range left {
			names = append(names, v.Name)
		}
		return false, fmt.Errorf("keeping pool %s: it still holds files bmbox did not create: %s", name, strings.Join(names, ", "))
	}

	if err := c.l.StoragePoolDestroy(p); err != nil {
		return false, fmt.Errorf("stop pool %s: %w", name, err)
	}
	if err := c.l.StoragePoolDelete(p, libvirt.StoragePoolDeleteNormal); err != nil {
		return false, fmt.Errorf("remove pool directory %s: %w", path, err)
	}
	if err := c.l.StoragePoolUndefine(p); err != nil {
		return false, fmt.Errorf("undefine pool %s: %w", name, err)
	}
	return true, nil
}
