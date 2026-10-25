// Package systemd runs bmbox's long-lived helpers (BMCs, dnsmasq) as
// transient systemd service units over D-Bus, so they keep serving after
// bmbox exits, are restarted on failure and log to the journal.
package systemd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/godbus/dbus/v5"

	"bmbox/pkg/logging"
)

type Manager struct {
	conn *sdbus.Conn
}

func Connect(ctx context.Context) (*Manager, error) {
	conn, err := sdbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to systemd: %w", err)
	}
	return &Manager{conn: conn}, nil
}

func (m *Manager) Close() { m.conn.Close() }

// Service describes a transient service unit.
type Service struct {
	Unit        string
	Description string
	Cmd         []string
	Env         []string
}

// Start (re)starts a transient service, replacing any running instance.
// It is restarted by systemd when it fails.
func (m *Manager) Start(ctx context.Context, s Service) error {
	if err := m.Stop(ctx, s.Unit); err != nil {
		return err
	}
	props := []sdbus.Property{
		sdbus.PropDescription(s.Description),
		sdbus.PropExecStart(s.Cmd, false),
		{Name: "Restart", Value: dbus.MakeVariant("on-failure")},
		{Name: "RestartUSec", Value: dbus.MakeVariant(uint64(2 * time.Second / time.Microsecond))},
	}
	logging.Debugf("systemd: start %s: %s", s.Unit, strings.Join(s.Cmd, " "))
	if len(s.Env) > 0 {
		props = append(props, sdbus.Property{Name: "Environment", Value: dbus.MakeVariant(s.Env)})
	}
	done := make(chan string, 1)
	if _, err := m.conn.StartTransientUnitContext(ctx, s.Unit, "replace", props, done); err != nil {
		return fmt.Errorf("start %s: %w", s.Unit, err)
	}
	if r := <-done; r != "done" {
		return fmt.Errorf("start %s: job %s", s.Unit, r)
	}
	return nil
}

// Stop stops a unit and clears its failed state. A unit that does not exist
// is not an error.
func (m *Manager) Stop(ctx context.Context, unit string) error {
	logging.Debugf("systemd: stop %s", unit)
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

// UnitDir is where persistent unit files are installed.
var UnitDir = "/etc/systemd/system"

// InstallUnit writes a persistent unit file, reloads systemd when it
// changed and enables the unit, so it starts at boot. It reports whether
// the file changed.
func (m *Manager) InstallUnit(ctx context.Context, name, content string) (bool, error) {
	path := filepath.Join(UnitDir, name)
	changed := false
	if old, err := os.ReadFile(path); err != nil || string(old) != content {
		logging.Debugf("systemd: write %s", path)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
			return false, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return false, err
		}
		if err := m.conn.ReloadContext(ctx); err != nil {
			return true, fmt.Errorf("systemd daemon-reload: %w", err)
		}
		changed = true
	}
	logging.Debugf("systemd: enable %s", name)
	if _, _, err := m.conn.EnableUnitFilesContext(ctx, []string{name}, false, true); err != nil {
		return changed, fmt.Errorf("enable %s: %w", name, err)
	}
	return changed, nil
}

// RemoveUnit disables a unit installed by InstallUnit and deletes its
// file. It reports whether there was one.
func (m *Manager) RemoveUnit(ctx context.Context, name string) (bool, error) {
	path := filepath.Join(UnitDir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	logging.Debugf("systemd: disable and remove %s", name)
	if _, err := m.conn.DisableUnitFilesContext(ctx, []string{name}, false); err != nil {
		return false, fmt.Errorf("disable %s: %w", name, err)
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	if err := m.conn.ReloadContext(ctx); err != nil {
		return true, fmt.Errorf("systemd daemon-reload: %w", err)
	}
	return true, nil
}

// State returns the unit's ActiveState ("active", "failed", "inactive"...).
func (m *Manager) State(ctx context.Context, unit string) string {
	p, err := m.conn.GetUnitPropertyContext(ctx, unit, "ActiveState")
	if err != nil {
		return "unknown"
	}
	s, _ := p.Value.Value().(string)
	return s
}

// Units lists the loaded units matching a glob pattern, as name ->
// description.
func (m *Manager) Units(ctx context.Context, pattern string) (map[string]string, error) {
	units, err := m.conn.ListUnitsByPatternsContext(ctx, nil, []string{pattern})
	if err != nil {
		return nil, fmt.Errorf("list units %s: %w", pattern, err)
	}
	out := map[string]string{}
	for _, u := range units {
		out[u.Name] = u.Description
	}
	return out, nil
}
