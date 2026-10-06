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

func TestSanitizeHistory(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"boot start clears are dropped",
			"\x1b[2J\x1b[01;01H\x1b[=3h\x1b[2J\x1b[01;01H\r\n>>Start PXE over IPv4.\r\n",
			"\r\n>>Start PXE over IPv4.\r\n"},
		{"colours kept",
			"\x1b[1m\x1b[33mShell> \x1b[0m",
			"\x1b[1m\x1b[33mShell> \x1b[0m"},
		{"cursor-addressed rows become indented lines",
			"\x1b[02;03HBoot Manager Menu\x1b[04;03HUEFI QEMU DVD-ROM\x1b[05;03HUEFI PXEv4",
			"  Boot Manager Menu\r\n  UEFI QEMU DVD-ROM\r\n  UEFI PXEv4"},
		{"moves along the same row become spaces",
			"\x1b[03;01HSelect Language\x1b[03;30H<Standard English>",
			"Select Language              <Standard English>"},
		{"shell echo stays on one line",
			"\x1b[05;01HShell> e\x1b[05;09Hx\x1b[05;10Hi\x1b[05;11Ht",
			"Shell> exit"},
		{"cursor forward becomes spaces",
			"Press\x1b[3CESC",
			"Press   ESC"},
		{"private modes dropped",
			"\x1b[?25lhidden cursor\x1b[?25h",
			"hidden cursor"},
		{"other terminal-changing sequences dropped",
			"a\x1b[s\x1b[u\x1b7\x1b8\x1b[3L\x1b[2M\x1b[10X\x1b[S\x1b[>1c\x1b]0;title\x07\x1b(Bb\x07",
			"ab"},
		{"truncated sequence at the end dropped",
			"Shell> \x1b[05;",
			"Shell> "},
		{"lone ESC at the end dropped",
			"done\x1b",
			"done"},
	}
	for _, c := range cases {
		if got := string(sanitizeHistory([]byte(c.in))); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestTailLinesSkipsPartialFirstLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "serial.log")
	// > 64 KiB without the window starting on a line boundary
	big := strings.Repeat("x", 70<<10) + "\x1b[05;01Hfirst full line\r\nlast line\r\n"
	os.WriteFile(p, []byte(big), 0o644)
	got := string(tailLines(p, 50))
	if got != "last line" {
		t.Errorf("tail = %q", got)
	}
}

func TestEscapeName(t *testing.T) {
	if EscapeName() != "^]" {
		t.Errorf("EscapeName = %q", EscapeName())
	}
}
