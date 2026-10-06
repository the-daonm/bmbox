package console

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyUntilEscape(t *testing.T) {
	var dst bytes.Buffer
	err := copyUntilEscape(&dst, strings.NewReader("ls -l\r\x1dnot sent"))
	if !errors.Is(err, errDetached) {
		t.Fatalf("err = %v, want errDetached", err)
	}
	if got := dst.String(); got != "ls -l\r" {
		t.Errorf("forwarded %q", got)
	}
}

func TestTailLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "serial.log")
	os.WriteFile(p, []byte(">>Start PXE over IPv4.\r\nPXE-E18: Server response timeout.\r\n>>Start PXE over IPv6.\r\n"), 0o644)
	if got := string(tailLines(p, 2)); got != "PXE-E18: Server response timeout.\r\n>>Start PXE over IPv6." {
		t.Errorf("tail 2 = %q", got)
	}
	if got := string(tailLines(p, 10)); !strings.HasPrefix(got, ">>Start PXE over IPv4.") {
		t.Errorf("tail 10 = %q", got)
	}
	if tailLines(p, 0) != nil || tailLines(filepath.Join(t.TempDir(), "missing"), 5) != nil {
		t.Error("expected no history")
	}
}

func TestStripScreenControlKeepsText(t *testing.T) {
	in := "\x1b[2J\x1b[01;01H\x1b[=3h\x1b[2J\x1b[01;01H\r\n>>Start PXE over IPv4.\r\n\x1b[1m\x1b[33mShell> \x1b[0m"
	want := "\r\n>>Start PXE over IPv4.\r\n\x1b[1m\x1b[33mShell> \x1b[0m"
	if got := string(stripScreenControl([]byte(in))); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
