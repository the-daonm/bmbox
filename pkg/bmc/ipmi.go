package bmc

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ipmiLauncher runs a single virtualbmc VirtualBMC in the foreground. Using
// the class directly (instead of vbmcd) keeps every node's BMC an
// independent process with no shared daemon state.
//
//go:embed ipmi_bmc.py
var ipmiLauncher []byte

type ipmiConfig struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Domain     string `json:"domain"`
	LibvirtURI string `json:"libvirt_uri"`
}

func writeIPMI(s Spec, w FileWriter, t Tools) (cmd []string, changed bool, err error) {
	launcher := filepath.Join(s.Dir, "ipmi_bmc.py")
	cfgPath := filepath.Join(s.Dir, "ipmi.json")

	cfg, err := json.MarshalIndent(ipmiConfig{
		Username: s.Username, Password: s.Password,
		Address: s.Address, Port: s.Port,
		Domain: s.Domain, LibvirtURI: s.LibvirtURI,
	}, "", "  ")
	if err != nil {
		return nil, false, err
	}

	for _, f := range []struct {
		path   string
		data   []byte
		secret bool
	}{{launcher, ipmiLauncher, false}, {cfgPath, cfg, true}} {
		if old, err := os.ReadFile(f.path); err == nil && bytes.Equal(old, f.data) {
			continue
		}
		write := w.WriteFile
		if f.secret {
			write = w.WriteSecret
		}
		if err := write(f.path, f.data); err != nil {
			return nil, false, err
		}
		changed = true
	}
	return []string{t.VBMCPython, launcher, cfgPath}, changed, nil
}

// probeUDP succeeds once something is bound to the UDP port.
func probeUDP(port int) error {
	want := fmt.Sprintf(":%04X ", port)
	for _, p := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// "  sl  local_address rem_address ..." -> field 1 is ADDR:PORT
			fields := strings.Fields(sc.Text())
			if len(fields) > 1 && strings.HasSuffix(fields[1]+" ", want) {
				f.Close()
				return nil
			}
		}
		f.Close()
	}
	return fmt.Errorf("udp port %d not bound yet", port)
}
