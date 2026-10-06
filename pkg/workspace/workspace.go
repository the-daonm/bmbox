// Package workspace manages bmbox's local state directory:
//
//	~/.bmbox/
//	└── labs/<lab>/
//	    ├── topology.yaml      snapshot of the applied topology
//	    ├── state.json         what `up` created (bridges, sysctls, volumes, domains)
//	    └── domains/<node>.xml rendered libvirt domain definitions
//
// Node disks and NVRAM live in a libvirt storage pool (see pkg/hypervisor)
// because libvirtd/QEMU must be able to reach them, which is not true for a
// user's home directory (mode 0750, or libvirtd running in a container).
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

const EnvHome = "BMBOX_HOME"

type Workspace struct {
	Root string
	// uid/gid own the files when bmbox runs under sudo, so the invoking
	// user can still read and clean their workspace.
	uid, gid int
}

// Open resolves the workspace root: $BMBOX_HOME, else ~/.bmbox of the
// invoking user (the sudo caller, not root).
func Open() (*Workspace, error) {
	ws := &Workspace{uid: -1, gid: -1}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
			if u, err := user.Lookup(name); err == nil {
				home = u.HomeDir
				ws.uid, _ = strconv.Atoi(u.Uid)
				ws.gid, _ = strconv.Atoi(u.Gid)
			}
		}
	}
	ws.Root = filepath.Join(home, ".bmbox")
	if v := os.Getenv(EnvHome); v != "" {
		ws.Root = v
	}
	if err := ws.mkdir(ws.Root); err != nil {
		return nil, err
	}
	return ws, nil
}

func (w *Workspace) LabDir(lab string) string { return filepath.Join(w.Root, "labs", lab) }

// BMCDir holds a node's generated BMC config and credentials.
func (w *Workspace) BMCDir(lab, node string) string {
	return filepath.Join(w.LabDir(lab), "bmc", node)
}

// Labs lists the labs that have a workspace directory.
func (w *Workspace) Labs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(w.Root, "labs"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	var labs []string
	for _, e := range entries {
		if e.IsDir() {
			labs = append(labs, e.Name())
		}
	}
	return labs, err
}

// RemoveLab deletes everything the workspace holds for a lab.
func (w *Workspace) RemoveLab(lab string) error {
	return os.RemoveAll(w.LabDir(lab))
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

// LabState is persisted after each successful `up` and is what `destroy`
// will use to tear the lab down.
type LabState struct {
	Lab       string         `json:"lab"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Libvirt   string         `json:"libvirt"`
	Networks  []NetworkState `json:"networks"`
	Sysctls   []SysctlState  `json:"sysctls,omitempty"`
	Pool      PoolState      `json:"pool"`
	Nodes     []NodeState    `json:"nodes"`
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
	return &s, nil
}

func (w *Workspace) SaveState(s *LabState) error {
	s.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return w.WriteFile(w.statePath(s.Lab), append(b, '\n'))
}

func (w *Workspace) SaveTopology(lab string, raw []byte) error {
	return w.WriteFile(filepath.Join(w.LabDir(lab), "topology.yaml"), raw)
}

func (w *Workspace) SaveDomainXML(lab, node, xml string) (string, error) {
	p := filepath.Join(w.LabDir(lab), "domains", node+".xml")
	return p, w.WriteFile(p, []byte(xml))
}
