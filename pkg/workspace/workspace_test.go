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
