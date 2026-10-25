// Package logging holds bmbox's progress and debug output settings: quiet
// and verbose modes, and timestamps when the output is not a terminal (a
// CI log, a file), so long runs can be followed in time.
package logging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

var (
	mu    sync.Mutex
	debug io.Writer = io.Discard
)

// SetDebug sends Debugf output to w (io.Discard turns it off).
func SetDebug(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	debug = w
}

// Debugf writes a line of debug output, shown with --verbose.
func Debugf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(debug, "debug: "+format+"\n", args...)
}

// WantTimestamps reports whether lines written to f should carry a
// timestamp: not on a terminal, and not under systemd, whose journal
// already stamps every line.
func WantTimestamps(f *os.File) bool {
	if os.Getenv("JOURNAL_STREAM") != "" {
		return false
	}
	return !term.IsTerminal(int(f.Fd()))
}

// Timestamped prefixes every line written through it with the UTC time.
type Timestamped struct {
	W   io.Writer
	Now func() time.Time

	mu      sync.Mutex
	midLine bool
}

func (t *Timestamped) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}
	var out bytes.Buffer
	for _, line := range bytes.SplitAfter(p, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if !t.midLine {
			out.WriteString(now().UTC().Format(time.RFC3339) + " ")
		}
		out.Write(line)
		t.midLine = line[len(line)-1] != '\n'
	}
	if _, err := t.W.Write(out.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}
