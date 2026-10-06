package console

import (
	"bytes"
	"errors"
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
