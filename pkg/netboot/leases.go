package netboot

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// Lease is one line of the dnsmasq lease file.
type Lease struct {
	Expiry   time.Time
	MAC      string
	IP       string
	Hostname string
}

// ReadLeases parses a dnsmasq lease file
// ("<expiry> <mac> <ip> <hostname|*> <client-id|*>"). A missing file has
// no leases.
func ReadLeases(path string) ([]Lease, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Lease
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		exp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue // DHCPv6 "duid" line
		}
		l := Lease{Expiry: time.Unix(exp, 0), MAC: strings.ToLower(fields[1]), IP: fields[2]}
		if fields[3] != "*" {
			l.Hostname = fields[3]
		}
		out = append(out, l)
	}
	return out, sc.Err()
}
