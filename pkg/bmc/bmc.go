// Package bmc runs one emulated BMC per node (Redfish via sushy-tools, IPMI
// via virtualbmc) as a systemd transient unit, so the BMCs keep serving
// after bmbox exits, are restarted on failure and log to the journal.
package bmc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	sdbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/godbus/dbus/v5"
)

const (
	TypeRedfish = "redfish"
	TypeIPMI    = "ipmi"

	EnvSushyEmulator = "BMBOX_SUSHY_EMULATOR"
	EnvVBMC          = "BMBOX_VBMC"
)

// Spec is everything needed to run one node's BMC.
type Spec struct {
	Lab        string
	Node       string
	Domain     string
	UUID       string
	Type       string
	Address    string
	Port       int
	Username   string
	Password   string
	LibvirtURI string
	// Loader is reported to sushy-tools as the UEFI boot loader so boot
	// mode queries match the domain definition.
	Loader string
	Unit   string
	// Dir holds the BMC's generated config and credentials.
	Dir string
}

// Endpoint is the URL or host:port a client uses to reach the BMC.
func (s Spec) Endpoint() string {
	hp := net.JoinHostPort(s.Address, fmt.Sprint(s.Port))
	if s.Type == TypeRedfish {
		return "http://" + hp + "/redfish/v1/Systems/" + s.UUID
	}
	return hp
}

// FileWriter persists generated files; Secret files must not be world
// readable.
type FileWriter interface {
	WriteFile(path string, data []byte) error
	WriteSecret(path string, data []byte) error
}

// Tools are the external programs the BMCs are built on.
type Tools struct {
	SushyEmulator string
	// VBMCPython is the interpreter that can import virtualbmc.
	VBMCPython string
}

// FindTools locates sushy-emulator and virtualbmc from explicit paths, then
// $BMBOX_SUSHY_EMULATOR / $BMBOX_VBMC, then $PATH. Only the types in use
// are required.
func FindTools(sushy, vbmc string, need map[string]bool) (Tools, error) {
	var t Tools
	var err error
	if need[TypeRedfish] {
		if t.SushyEmulator, err = findProgram(sushy, EnvSushyEmulator, "sushy-emulator"); err != nil {
			return t, fmt.Errorf("redfish BMC needs sushy-tools: %w", err)
		}
	}
	if need[TypeIPMI] {
		path, err := findProgram(vbmc, EnvVBMC, "vbmc")
		if err != nil {
			return t, fmt.Errorf("ipmi BMC needs virtualbmc: %w", err)
		}
		if t.VBMCPython, err = interpreterOf(path); err != nil {
			return t, err
		}
	}
	return t, nil
}

func findProgram(explicit, env, name string) (string, error) {
	for _, p := range []string{explicit, os.Getenv(env)} {
		if p != "" {
			if _, err := os.Stat(p); err != nil {
				return "", err
			}
			return p, nil
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH (sudo resets PATH; set %s=/path/to/%s)", name, env, name)
	}
	return p, nil
}

// interpreterOf reads the python interpreter from a console script's
// shebang, so a virtualenv install is used with its own site-packages.
func interpreterOf(script string) (string, error) {
	f, err := os.Open(script)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#!"))
	if !strings.HasPrefix(line, "#!") || len(fields) == 0 {
		return "", fmt.Errorf("%s has no interpreter line", script)
	}
	if filepath.Base(fields[0]) == "env" && len(fields) > 1 {
		return exec.LookPath(fields[1])
	}
	return fields[0], nil
}

// Manager starts and stops BMC units through systemd's D-Bus API.
type Manager struct {
	conn  *sdbus.Conn
	tools Tools
}

func NewManager(ctx context.Context, tools Tools) (*Manager, error) {
	conn, err := sdbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to systemd: %w", err)
	}
	return &Manager{conn: conn, tools: tools}, nil
}

func (m *Manager) Close() { m.conn.Close() }

// State returns the unit's ActiveState ("active", "failed", "inactive"...).
func (m *Manager) State(ctx context.Context, unit string) string {
	p, err := m.conn.GetUnitPropertyContext(ctx, unit, "ActiveState")
	if err != nil {
		return "unknown"
	}
	s, _ := p.Value.Value().(string)
	return s
}

type Result struct {
	Started bool
}

// Ensure writes the BMC's config and makes sure its unit runs with it. A
// running unit whose config did not change is left alone.
func (m *Manager) Ensure(ctx context.Context, s Spec, w FileWriter) (*Result, error) {
	var (
		cmd     []string
		changed bool
		err     error
	)
	switch s.Type {
	case TypeRedfish:
		cmd, changed, err = writeRedfish(s, w, m.tools)
	case TypeIPMI:
		cmd, changed, err = writeIPMI(s, w, m.tools)
	default:
		return nil, fmt.Errorf("unknown BMC type %q", s.Type)
	}
	if err != nil {
		return nil, err
	}

	if !changed && m.State(ctx, s.Unit) == "active" {
		return &Result{}, nil
	}
	if err := m.Stop(ctx, s.Unit); err != nil {
		return nil, err
	}
	props := []sdbus.Property{
		sdbus.PropDescription(fmt.Sprintf("bmbox %s BMC for %s/%s", s.Type, s.Lab, s.Node)),
		sdbus.PropExecStart(cmd, false),
		{Name: "Restart", Value: dbus.MakeVariant("on-failure")},
		{Name: "RestartUSec", Value: dbus.MakeVariant(uint64(2 * time.Second / time.Microsecond))},
		{Name: "Environment", Value: dbus.MakeVariant([]string{"PYTHONUNBUFFERED=1"})},
	}
	done := make(chan string, 1)
	if _, err := m.conn.StartTransientUnitContext(ctx, s.Unit, "replace", props, done); err != nil {
		return nil, fmt.Errorf("start %s: %w", s.Unit, err)
	}
	if r := <-done; r != "done" {
		return nil, fmt.Errorf("start %s: job %s", s.Unit, r)
	}
	if err := m.waitReady(ctx, s); err != nil {
		return nil, err
	}
	return &Result{Started: true}, nil
}

// Stop stops a unit and clears its failed state. A unit that does not exist
// is not an error.
func (m *Manager) Stop(ctx context.Context, unit string) error {
	done := make(chan string, 1)
	if _, err := m.conn.StopUnitContext(ctx, unit, "replace", done); err != nil {
		var de dbus.Error
		if errors.As(err, &de) && strings.HasSuffix(de.Name, "NoSuchUnit") {
			return nil
		}
		return fmt.Errorf("stop %s: %w", unit, err)
	}
	<-done
	_ = m.conn.ResetFailedUnitContext(ctx, unit)
	return nil
}

func (m *Manager) waitReady(ctx context.Context, s Spec) error {
	deadline := time.Now().Add(20 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if st := m.State(ctx, s.Unit); st == "failed" {
			break
		}
		if s.Type == TypeRedfish {
			last = probeRedfish(s)
		} else {
			last = probeUDP(s.Port)
		}
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s BMC for %s did not become ready (%v); see: journalctl -u %s", s.Type, s.Node, last, s.Unit)
}

// ErrAddrNotLocal means the BMC address is not configured on the host (yet).
var ErrAddrNotLocal = errors.New("address not configured on this host")

// CheckPort verifies nothing else listens on the BMC's port.
func CheckPort(typ, addr string, port int) error {
	hp := net.JoinHostPort(addr, fmt.Sprint(port))
	var err error
	if typ == TypeIPMI {
		var c net.PacketConn
		if c, err = net.ListenPacket("udp", hp); err == nil {
			c.Close()
		}
	} else {
		var l net.Listener
		if l, err = net.Listen("tcp", hp); err == nil {
			l.Close()
		}
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return ErrAddrNotLocal
	case errors.Is(err, syscall.EADDRINUSE):
		return fmt.Errorf("port %s is already in use on the host", hp)
	}
	return err
}
