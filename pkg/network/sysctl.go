package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// procSys is a variable so tests can point it at a temp dir.
var procSys = "/proc/sys"

// SysctlChange records a kernel parameter bmbox modified, so that `destroy`
// can restore the previous value.
type SysctlChange struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

// Keys use "/" as separator (net/ipv6/conf/eth0.10/disable_ipv6) because
// interface names may themselves contain dots.
func sysctlPath(key string) string {
	return filepath.Join(procSys, filepath.FromSlash(key))
}

func ReadSysctl(key string) (string, error) {
	b, err := os.ReadFile(sysctlPath(key))
	if err != nil {
		return "", fmt.Errorf("read sysctl %s: %w", key, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// EnsureSysctl sets key to want. It returns nil (no change) when the value is
// already correct.
func EnsureSysctl(key, want string) (*SysctlChange, error) {
	old, err := ReadSysctl(key)
	if err != nil {
		return nil, err
	}
	if old == want {
		return nil, nil
	}
	if err := os.WriteFile(sysctlPath(key), []byte(want), 0o644); err != nil {
		return nil, fmt.Errorf("write sysctl %s=%s: %w", key, want, err)
	}
	return &SysctlChange{Key: key, Old: old, New: want}, nil
}

// RestoreSysctl undoes a change, but only while the value is still the one
// bmbox set: if someone changed it since, theirs wins. Missing keys (e.g. the
// per-interface tree of a deleted bridge) are ignored.
func RestoreSysctl(c SysctlChange) (restored bool, err error) {
	cur, err := ReadSysctl(c.Key)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || cur != c.New {
		return false, err
	}
	changed, err := EnsureSysctl(c.Key, c.Old)
	return changed != nil, err
}

// EnsureHostSysctls applies host-wide settings the lab needs. Values are only
// ever raised to what bmbox requires, never lowered, because the host may be
// shared with other workloads (OpenStack, Docker, ...).
func EnsureHostSysctls(needForwarding bool) ([]SysctlChange, error) {
	var changes []SysctlChange
	if needForwarding {
		c, err := EnsureSysctl("net/ipv4/ip_forward", "1")
		if err != nil {
			return changes, err
		}
		if c != nil {
			changes = append(changes, *c)
		}
	}
	return changes, nil
}
