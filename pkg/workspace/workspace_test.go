package workspace

import (
	"strings"
	"testing"
)

func TestLockIsExclusivePerLab(t *testing.T) {
	ws := &Workspace{Root: t.TempDir(), uid: -1, gid: -1}
	unlock, err := ws.Lock("lab1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Lock("lab1"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock: %v", err)
	}
	other, err := ws.Lock("lab2")
	if err != nil {
		t.Fatalf("other lab blocked: %v", err)
	}
	other()
	unlock()
	again, err := ws.Lock("lab1")
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

func TestToolsRoundTrip(t *testing.T) {
	ws := &Workspace{Root: t.TempDir(), uid: -1, gid: -1}
	want := Tools{SushyEmulator: "/venv/bin/sushy-emulator", VBMC: "/venv/bin/vbmc"}
	if err := ws.SaveTools(want); err != nil {
		t.Fatal(err)
	}
	if got := ws.LoadTools(); got != want {
		t.Errorf("LoadTools = %+v", got)
	}
}

func TestStateVersion(t *testing.T) {
	ws := &Workspace{Root: t.TempDir(), uid: -1, gid: -1}
	if err := ws.SaveState(&LabState{Lab: "lab1"}); err != nil {
		t.Fatal(err)
	}
	st, err := ws.LoadState("lab1")
	if err != nil || st.Version != StateVersion {
		t.Fatalf("saved state: %+v, %v", st, err)
	}

	// A state from before versioning loads as the current version.
	if err := ws.WriteFile(ws.statePath("old"), []byte(`{"lab":"old","nodes":[{"name":"n1"}]}`)); err != nil {
		t.Fatal(err)
	}
	st, err = ws.LoadState("old")
	if err != nil || st.Version != StateVersion || len(st.Nodes) != 1 {
		t.Fatalf("unversioned state: %+v, %v", st, err)
	}

	// A state from a newer bmbox is refused.
	if err := ws.WriteFile(ws.statePath("new"), []byte(`{"version":99,"lab":"new"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.LoadState("new"); err == nil || !strings.Contains(err.Error(), "newer bmbox") {
		t.Fatalf("newer state: %v", err)
	}
}

func TestLegacyLabIsMovedOnLock(t *testing.T) {
	root, legacy := t.TempDir(), t.TempDir()
	old := &Workspace{Root: legacy, uid: -1, gid: -1}
	if err := old.SaveState(&LabState{Lab: "lab1"}); err != nil {
		t.Fatal(err)
	}
	if err := old.WriteFile(old.BMCDir("lab1", "n1")+"/sushy.conf", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := old.SaveTools(Tools{VBMC: "/venv/bin/vbmc"}); err != nil {
		t.Fatal(err)
	}

	ws := &Workspace{Root: root, legacy: legacy, uid: -1, gid: -1}
	if labs, _ := ws.Labs(); len(labs) != 1 || labs[0] != "lab1" {
		t.Fatalf("Labs = %v", labs)
	}
	if got := ws.LabDir("lab1"); got != legacy+"/labs/lab1" {
		t.Fatalf("before the move LabDir = %s", got)
	}
	if got := ws.LoadTools().VBMC; got != "/venv/bin/vbmc" {
		t.Fatalf("legacy tools not read: %q", got)
	}

	unlock, err := ws.Lock("lab1")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if got := ws.LabDir("lab1"); got != root+"/labs/lab1" {
		t.Fatalf("after the move LabDir = %s", got)
	}
	if st, err := ws.LoadState("lab1"); err != nil || st == nil {
		t.Fatalf("moved state: %v, %v", st, err)
	}
	if exists(ws.BMCDir("lab1", "n1")) {
		t.Error("BMC dir was copied; up must regenerate it")
	}
	if !exists(legacy + "/labs/lab1") {
		t.Fatal("legacy copy removed before up re-pointed the BMCs")
	}
	if err := ws.RemoveLegacyLab("lab1"); err != nil || exists(legacy+"/labs/lab1") {
		t.Fatalf("RemoveLegacyLab: %v", err)
	}
	if labs, _ := ws.Labs(); len(labs) != 1 {
		t.Fatalf("Labs after the move = %v", labs)
	}
}

func TestRemoveLabRemovesLegacyCopy(t *testing.T) {
	root, legacy := t.TempDir(), t.TempDir()
	ws := &Workspace{Root: root, legacy: legacy, uid: -1, gid: -1}
	for _, w := range []*Workspace{{Root: legacy, uid: -1, gid: -1}, {Root: root, uid: -1, gid: -1}} {
		if err := w.SaveState(&LabState{Lab: "lab1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.RemoveLab("lab1"); err != nil {
		t.Fatal(err)
	}
	if labs, _ := ws.Labs(); len(labs) != 0 {
		t.Fatalf("Labs after RemoveLab = %v", labs)
	}
}
