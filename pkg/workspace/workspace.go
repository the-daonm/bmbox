// Package workspace manages bmbox's state directory, shared by every user
// of the host (labs share its bridges, ports and subnets):
//
//	/var/lib/bmbox/
//	├── tools.json             where the BMC and netboot programs were found
//	└── labs/<lab>/
//	    ├── topology.yaml      snapshot of the applied topology
//	    ├── state.json         what `up` created (bridges, sysctls, volumes, domains)
//	    ├── domains/<node>.xml rendered libvirt domain definitions
//	    └── bmc/<node>/        generated BMC config and credentials
//
// Older versions kept labs in ~/.bmbox of the invoking user. Those are
// still read, and moved here by the next `up` (see Lock).
//
// Node disks and NVRAM live in a libvirt storage pool (see pkg/hypervisor)
// because libvirtd/QEMU must be able to reach them, which is not true for a
// user's home directory (mode 0750, or libvirtd running in a container).
package workspace

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"
)

const (
	EnvHome = "BMBOX_HOME"
	// SystemRoot is the default workspace.
	SystemRoot = "/var/lib/bmbox"
)

type Workspace struct {
	Root string
	// legacy is the per-user workspace of older versions (~/.bmbox), read
	// until its labs are moved to Root. Empty when BMBOX_HOME is set.
	legacy string
	// uid/gid own the files of a BMBOX_HOME workspace when bmbox runs
	// under sudo, so the invoking user can still read and clean it.
	uid, gid int
}

// Open resolves the workspace root: $BMBOX_HOME, else SystemRoot. Nothing
// is created until something is written, so read-only commands work for
// users who cannot write it.
func Open() (*Workspace, error) {
	ws := &Workspace{Root: SystemRoot, uid: -1, gid: -1}
	home, uid, gid := invokingUser()
	if v := os.Getenv(EnvHome); v != "" {
		ws.Root = v
		ws.uid, ws.gid = uid, gid
		return ws, nil
	}
	if home != "" {
		if old := filepath.Join(home, ".bmbox"); old != ws.Root {
			ws.legacy = old
		}
	}
	return ws, nil
}

// invokingUser returns the home directory of the user who ran bmbox (the
// sudo caller, not root), and their uid/gid when running under sudo.
func invokingUser() (home string, uid, gid int) {
	uid, gid = -1, -1
	home, _ = os.UserHomeDir()
	if os.Geteuid() == 0 {
		if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
			if u, err := user.Lookup(name); err == nil {
				home = u.HomeDir
				uid, _ = strconv.Atoi(u.Uid)
				gid, _ = strconv.Atoi(u.Gid)
			}
		}
	}
	return home, uid, gid
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// LabDir is the lab's directory: in Root, or in the legacy workspace while
// the lab has not been moved yet.
func (w *Workspace) LabDir(lab string) string {
	dir := filepath.Join(w.Root, "labs", lab)
	if w.legacy != "" && !exists(dir) {
		if old := w.legacyLabDir(lab); exists(old) {
			return old
		}
	}
	return dir
}

func (w *Workspace) legacyLabDir(lab string) string {
	if w.legacy == "" {
		return ""
	}
	return filepath.Join(w.legacy, "labs", lab)
}

// BMCDir holds a node's generated BMC config and credentials.
func (w *Workspace) BMCDir(lab, node string) string {
	return filepath.Join(w.LabDir(lab), "bmc", node)
}

// Labs lists the labs that have a workspace directory, in Root or in the
// legacy workspace.
func (w *Workspace) Labs() ([]string, error) {
	seen := map[string]bool{}
	var labs []string
	for _, root := range []string{w.Root, w.legacy} {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, "labs"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return labs, err
		}
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				seen[e.Name()] = true
				labs = append(labs, e.Name())
			}
		}
	}
	sort.Strings(labs)
	return labs, nil
}

// migrate copies a lab of the legacy workspace into Root. The BMC
// directory is left out: it holds paths into the old location, so `up`
// regenerates it and restarts the BMCs on the new files. The legacy copy
// stays until RemoveLegacyLab, because running BMCs still use it.
func (w *Workspace) migrate(lab string) error {
	old := w.legacyLabDir(lab)
	dst := filepath.Join(w.Root, "labs", lab)
	if old == "" || !exists(old) || exists(dst) {
		return nil
	}
	tmp := dst + ".migrating"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	err := filepath.WalkDir(old, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(old, path)
		if rel == "bmc" || rel == ".lock" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(tmp, rel)
		if d.IsDir() {
			return w.mkdir(target)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return w.write(target, data, info.Mode().Perm())
	})
	if err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("move lab %s from %s to %s: %w", lab, old, dst, err)
	}
	return os.Rename(tmp, dst)
}

// RemoveLegacyLab deletes the legacy copy of a lab that was moved to Root.
// Call it once nothing uses the old files any more (after `up`).
func (w *Workspace) RemoveLegacyLab(lab string) error {
	old := w.legacyLabDir(lab)
	if old == "" || !exists(filepath.Join(w.Root, "labs", lab)) {
		return nil
	}
	return os.RemoveAll(old)
}

// RemoveLab deletes everything the workspace holds for a lab, including a
// legacy copy.
func (w *Workspace) RemoveLab(lab string) error {
	if old := w.legacyLabDir(lab); old != "" {
		if err := os.RemoveAll(old); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Join(w.Root, "labs", lab))
}

func (w *Workspace) mkdir(dir string) error {
	// Create each missing component so ownership is fixed on all of them.
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := w.mkdir(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	return w.chown(dir)
}

func (w *Workspace) chown(path string) error {
	if w.uid < 0 {
		return nil
	}
	return os.Lchown(path, w.uid, w.gid)
}

// WriteFile writes a file under the workspace atomically (temp + rename).
func (w *Workspace) WriteFile(path string, data []byte) error {
	return w.write(path, data, 0o644)
}

// WriteSecret is WriteFile for credentials: readable by the owner only.
func (w *Workspace) WriteSecret(path string, data []byte) error {
	return w.write(path, data, 0o600)
}

func (w *Workspace) write(path string, data []byte, mode os.FileMode) error {
	if err := w.mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := w.chown(tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// StateVersion is the format of state.json written by this bmbox. Files
// without a version are version 0, which differs only by the missing field.
// Bump it when a change needs older files converted, and convert them in
// upgradeState.
const StateVersion = 1

// LabState is persisted after each successful `up` and is what `destroy`
// will use to tear the lab down.
type LabState struct {
	Version   int            `json:"version"`
	Lab       string         `json:"lab"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Libvirt   string         `json:"libvirt"`
	Networks  []NetworkState `json:"networks"`
	Sysctls   []SysctlState  `json:"sysctls,omitempty"`
	Pool      PoolState      `json:"pool"`
	Nodes     []NodeState    `json:"nodes"`
	DHCP      *DHCPState     `json:"dhcp,omitempty"`
}

// DHCPState is the lab's dnsmasq (DHCP and netboot).
type DHCPState struct {
	Unit      string `json:"unit"`
	Dir       string `json:"dir"`
	LeaseFile string `json:"leaseFile"`
}

type NetworkState struct {
	Name     string        `json:"name"`
	Bridge   string        `json:"bridge"`
	Created  bool          `json:"created"`
	External bool          `json:"external"`
	Address  string        `json:"address,omitempty"`
	Sysctls  []SysctlState `json:"sysctls,omitempty"`
}

type SysctlState struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

type PoolState struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type NodeState struct {
	Name         string    `json:"name"`
	Domain       string    `json:"domain"`
	UUID         string    `json:"uuid"`
	State        string    `json:"state"`
	Disks        []string  `json:"disks"`
	NVRAM        string    `json:"nvram"`
	Loader       string    `json:"loader"`
	SerialSocket string    `json:"serialSocket"`
	MACs         []string  `json:"macs"`
	BMC          *BMCState `json:"bmc,omitempty"`
}

type BMCState struct {
	Type     string `json:"type"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Unit     string `json:"unit"`
	Endpoint string `json:"endpoint"`
}

func (w *Workspace) statePath(lab string) string {
	return filepath.Join(w.LabDir(lab), "state.json")
}

// LoadState returns the previous state, or nil if the lab was never brought up.
func (w *Workspace) LoadState(lab string) (*LabState, error) {
	b, err := os.ReadFile(w.statePath(lab))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s LabState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", w.statePath(lab), err)
	}
	if err := upgradeState(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", w.statePath(lab), err)
	}
	return &s, nil
}

// upgradeState converts a state read from disk to StateVersion. A state
// from a newer bmbox is refused: acting on fields this version does not
// know could leave resources behind.
func upgradeState(s *LabState) error {
	if s.Version > StateVersion {
		return fmt.Errorf("state format %d was written by a newer bmbox (this one reads up to %d); upgrade bmbox", s.Version, StateVersion)
	}
	// 0 -> 1: only the version field was added.
	s.Version = StateVersion
	return nil
}

func (w *Workspace) SaveState(s *LabState) error {
	s.Version = StateVersion
	s.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return w.WriteFile(w.statePath(s.Lab), append(b, '\n'))
}

// SaveTopology snapshots the applied topology. It may hold BMC passwords,
// so only root can read it.
func (w *Workspace) SaveTopology(lab string, raw []byte) error {
	return w.WriteSecret(filepath.Join(w.LabDir(lab), "topology.yaml"), raw)
}

func (w *Workspace) SaveDomainXML(lab, node, xml string) (string, error) {
	p := filepath.Join(w.LabDir(lab), "domains", node+".xml")
	return p, w.WriteFile(p, []byte(xml))
}

// Lock takes an exclusive per-lab lock so two bmbox commands never change
// the same lab at once. Call the returned function to release it.
//
// A lab still in the legacy workspace is copied to Root first, so the
// command that locks it works on the new location from then on.
func (w *Workspace) Lock(lab string) (func(), error) {
	if err := w.migrate(lab); err != nil {
		return nil, err
	}
	if err := w.mkdir(w.LabDir(lab)); err != nil {
		return nil, err
	}
	path := filepath.Join(w.LabDir(lab), ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	_ = w.chown(path)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another bmbox command is already running for lab %q", lab)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Tools remembers where the BMC and netboot programs were found, so they
// only have to be given once per host.
type Tools struct {
	SushyEmulator string `json:"sushyEmulator,omitempty"`
	VBMC          string `json:"vbmc,omitempty"`
	Dnsmasq       string `json:"dnsmasq,omitempty"`
	IPXE          string `json:"ipxe,omitempty"`
}

func (w *Workspace) toolsPath() string { return filepath.Join(w.Root, "tools.json") }

// LoadTools returns the remembered tool paths, from the legacy workspace
// if Root has none yet.
func (w *Workspace) LoadTools() Tools {
	var t Tools
	b, err := os.ReadFile(w.toolsPath())
	if os.IsNotExist(err) && w.legacy != "" {
		b, err = os.ReadFile(filepath.Join(w.legacy, "tools.json"))
	}
	if err == nil {
		_ = json.Unmarshal(b, &t)
	}
	return t
}

func (w *Workspace) SaveTools(t Tools) error {
	if t == w.LoadTools() && exists(w.toolsPath()) {
		return nil
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return w.WriteFile(w.toolsPath(), append(b, '\n'))
}
