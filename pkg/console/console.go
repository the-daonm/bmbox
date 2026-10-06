// Package console attaches the user's terminal to a node's serial port,
// exposed by QEMU as a UNIX socket.
package console

import (
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

// Attach copies between the terminal and the serial socket until the user
// presses Ctrl-] or the node powers off (QEMU closes the socket).
func Attach(socket string, in *os.File, out io.Writer) error {
	conn, err := net.Dial("unix", socket)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("no serial console at %s: is the node powered on?", socket)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%w (the console socket is owned by the QEMU user: run with sudo)", err)
	case err != nil:
		return err
	}
	defer conn.Close()

	if term.IsTerminal(int(in.Fd())) {
		old, err := term.MakeRaw(int(in.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(in.Fd()), old)
	}

	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(out, conn)
		done <- err
	}()
	go func() {
		// EOF on stdin (e.g. `bmbox console n1 < /dev/null > boot.log`)
		// only ends input; output keeps flowing until the node stops.
		if err := copyUntilEscape(conn, in); !errors.Is(err, io.EOF) {
			done <- err
		}
	}()
	err = <-done
	if errors.Is(err, errDetached) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

var errDetached = errors.New("detached")

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
