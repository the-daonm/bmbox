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
