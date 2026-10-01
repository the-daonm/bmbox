package hypervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"bmbox/pkg/topology"
)

// Firmware is the UEFI image pair used by every node of a lab.
type Firmware struct {
	// Loader is the read-only OVMF CODE image (path as seen by libvirtd).
	Loader string
	// VarsTemplate is the pristine OVMF VARS image each node's NVRAM is
	// cloned from.
	VarsTemplate string
	// VarsData is the template content when it is readable locally; nil
	// means libvirt will clone the template itself on first boot.
	VarsData []byte
}

// Well-known locations used when libvirt does not advertise any loader.
var fallbackLoaders = []string{
	"/usr/share/OVMF/OVMF_CODE_4M.fd",      // Debian/Ubuntu
	"/usr/share/OVMF/OVMF_CODE.fd",         // older Debian/Ubuntu
	"/usr/share/edk2/x64/OVMF_CODE.4m.fd",  // Arch
	"/usr/share/edk2/ovmf/OVMF_CODE.fd",    // Fedora/RHEL
	"/usr/share/qemu/ovmf-x86_64-code.bin", // openSUSE
}

// ResolveFirmware picks the OVMF loader and VARS template: explicit topology
// values win, then whatever libvirt's domain capabilities advertise for
// q35/KVM, then well-known paths. Secure Boot images are skipped because
// they require SMM and enrolled keys, which plain PXE labs do not want.
func (c *Client) ResolveFirmware(o topology.UEFI) (Firmware, error) {
	fw := Firmware{Loader: o.Loader, VarsTemplate: o.VarsTemplate}

	if fw.Loader == "" {
		var candidates []string
		if c != nil {
			caps, err := c.domainCaps()
			if err != nil {
				return fw, err
			}
			if caps.OS != nil && caps.OS.Loader != nil {
				candidates = caps.OS.Loader.Values
			}
		}
		fw.Loader = pickLoader(candidates)
		if fw.Loader == "" {
			fw.Loader = pickLoader(existing(fallbackLoaders))
		}
		if fw.Loader == "" {
			return fw, fmt.Errorf("no UEFI (OVMF) firmware found; install ovmf/edk2-ovmf or set spec.uefi.loader")
		}
	}

	if fw.VarsTemplate == "" {
		for _, cand := range varsCandidates(fw.Loader) {
			if _, err := os.Stat(cand); err == nil {
				fw.VarsTemplate = cand
				break
			}
		}
		if fw.VarsTemplate == "" {
			if cands := varsCandidates(fw.Loader); len(cands) > 0 {
				fw.VarsTemplate = cands[0]
			} else {
				return fw, fmt.Errorf("cannot derive the VARS template for loader %s; set spec.uefi.varsTemplate", fw.Loader)
			}
		}
	}

	if data, err := os.ReadFile(fw.VarsTemplate); err == nil {
		fw.VarsData = data
	}
	return fw, nil
}

func (c *Client) domainCaps() (*libvirtxml.DomainCaps, error) {
	raw, err := c.l.ConnectGetDomainCapabilities(nil,
		libvirt.OptString{"x86_64"}, libvirt.OptString{"q35"}, libvirt.OptString{"kvm"}, 0)
	if err != nil {
		return nil, fmt.Errorf("query domain capabilities (is KVM available?): %w", err)
	}
	caps := &libvirtxml.DomainCaps{}
	if err := caps.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("parse domain capabilities: %w", err)
	}
	return caps, nil
}

func pickLoader(paths []string) string {
	best, bestScore := "", -1
	for _, p := range paths {
		base := strings.ToLower(filepath.Base(p))
		if !strings.Contains(base, "code") ||
			strings.Contains(base, "secboot") || strings.Contains(base, ".ms.") ||
			strings.Contains(base, "snakeoil") || strings.Contains(base, "secure") {
			continue
		}
		score := 0
		if strings.Contains(base, "4m") {
			score++ // 4 MiB flash layout is the current upstream default
		}
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	return best
}

// varsCandidates maps a CODE image to its matching VARS template names.
func varsCandidates(loader string) []string {
	dir, base := filepath.Split(loader)
	var out []string
	for _, r := range [][2]string{{"CODE", "VARS"}, {"code", "vars"}} {
		if strings.Contains(base, r[0]) {
			out = append(out, dir+strings.Replace(base, r[0], r[1], 1))
		}
	}
	return out
}

func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
