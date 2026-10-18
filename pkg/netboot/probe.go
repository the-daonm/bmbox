package netboot

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// ProbeTFTP sends a read request for file and succeeds on any TFTP answer:
// the first data block, or an error such as "file not found". Either
// proves the server is up. A transfer that started is aborted.
func ProbeTFTP(server netip.AddrPort, file string, timeout time.Duration) error {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	// RRQ: opcode 1, filename, 0, mode, 0
	req := append([]byte{0, 1}, file...)
	req = append(append(req, 0), "octet\x00"...)
	if _, err := conn.WriteToUDPAddrPort(req, server); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1600)
	n, from, err := conn.ReadFromUDPAddrPort(buf)
	if err != nil {
		return fmt.Errorf("tftp %s: %w", server, err)
	}
	if n < 4 {
		return fmt.Errorf("tftp %s: short reply", server)
	}
	switch op := binary.BigEndian.Uint16(buf); op {
	case 3: // DATA: abort the transfer (ERROR 0) on the server's port
		_, _ = conn.WriteToUDPAddrPort([]byte{0, 5, 0, 0, 'p', 'r', 'o', 'b', 'e', 0}, from)
		return nil
	case 5, 6: // ERROR, OACK
		return nil
	default:
		return fmt.Errorf("tftp %s: unexpected opcode %d", server, op)
	}
}
