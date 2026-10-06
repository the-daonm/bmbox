// Package console attaches the user's terminal to a node's serial port,
// exposed by QEMU as a UNIX socket.
package console

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"golang.org/x/term"
)

// EscapeByte (Ctrl-]) detaches from the console, like telnet and virsh.
const EscapeByte = 0x1d

// EscapeName is how EscapeByte is shown to users ("^]").
func EscapeName() string { return fmt.Sprintf("^%c", EscapeByte+'@') }

// Options configures Attach.
type Options struct {
	Socket string
	// Log is the node's serial log; its last Tail lines are shown before
	// live output. The firmware can stay silent for minutes (e.g. waiting
	// for a DHCP offer), and without them a console attached mid-boot looks
	// frozen.
	Log  string
	Tail int
	In   *os.File
	Out  io.Writer
	// OnConnect runs once the socket is connected, before any output, so a
	// "Connected" banner is never shown for a failed attach.
	OnConnect func()
}

// End says why a console session ended.
type End int

const (
	// Detached: the user pressed the escape key.
	Detached End = iota
	// Closed: QEMU closed the socket, i.e. the node powered off.
	Closed
)

// Attach copies between the terminal and the serial socket until the user
// detaches or the node powers off.
func Attach(o Options) (End, error) {
	history := tailLines(o.Log, o.Tail)
	conn, err := net.Dial("unix", o.Socket)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return Closed, fmt.Errorf("no serial console at %s: is the node powered on?", o.Socket)
	case errors.Is(err, os.ErrPermission):
		return Closed, fmt.Errorf("%w (the console socket is owned by the QEMU user: run with sudo)", err)
	case err != nil:
		return Closed, err
	}
	defer conn.Close()

	if term.IsTerminal(int(o.In.Fd())) {
		old, err := term.MakeRaw(int(o.In.Fd()))
		if err != nil {
			return Closed, err
		}
		defer term.Restore(int(o.In.Fd()), old)
	}
	if o.OnConnect != nil {
		o.OnConnect()
	}

	if clean := sanitizeHistory(history); len(clean) > 0 {
		o.Out.Write(clean)
		// Reset attributes the history may have left set, then mark where
		// live output starts. Live output is passed through untouched, as
		// with virsh console.
		io.WriteString(o.Out, "\x1b[0m\r\n--- live ---\r\n")
	}

	type result struct {
		end End
		err error
	}
	done := make(chan result, 2)
	go func() {
		_, err := io.Copy(o.Out, conn)
		done <- result{Closed, err}
	}()
	go func() {
		// EOF on stdin (e.g. `bmbox console n1 < /dev/null > boot.log`)
		// only ends input; output keeps flowing until the node stops.
		err := copyUntilEscape(conn, o.In)
		switch {
		case errors.Is(err, errDetached):
			done <- result{Detached, nil}
		case !errors.Is(err, io.EOF):
			done <- result{Detached, err}
		}
	}()
	r := <-done
	if errors.Is(r.err, net.ErrClosed) {
		r.err = nil
	}
	return r.end, r.err
}

var errDetached = errors.New("detached")

// tailLines returns the last n lines of a file (nothing if n <= 0 or the
// file is missing). Only the end of the file is read; when that window
// starts mid-file its first, partial line is skipped.
func tailLines(path string, n int) []byte {
	if n <= 0 || path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const window = 64 << 10
	partial := false
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		if _, err := f.Seek(fi.Size()-window, io.SeekStart); err == nil {
			partial = true
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	if partial {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return nil
		}
		data = data[i+1:]
	}
	data = bytes.TrimRight(data, "\r\n")
	for i, cut := len(data)-1, 0; i >= 0; i-- {
		if data[i] == '\n' {
			if cut++; cut == n {
				return data[i+1:]
			}
		}
	}
	return data
}

// copyUntilEscape forwards keystrokes and stops at the escape byte.
func copyUntilEscape(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 1024)
	for {
		n, err := src.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i] == EscapeByte {
				if _, werr := dst.Write(buf[:i]); werr != nil {
					return werr
				}
				return errDetached
			}
		}
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}
