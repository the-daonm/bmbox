package logging

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestTimestampedPrefixesEveryLine(t *testing.T) {
	var buf bytes.Buffer
	ts := &Timestamped{W: &buf, Now: func() time.Time { return time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC) }}
	fmt.Fprint(ts, "==> Networks\n    pxe ")
	fmt.Fprint(ts, "created\n\nLab up\n")
	want := "2026-10-25T01:02:03Z ==> Networks\n" +
		"2026-10-25T01:02:03Z     pxe created\n" +
		"2026-10-25T01:02:03Z \n" +
		"2026-10-25T01:02:03Z Lab up\n"
	if buf.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestDebugfIsOffByDefault(t *testing.T) {
	var buf bytes.Buffer
	Debugf("hidden")
	SetDebug(&buf)
	defer SetDebug(nil)
	Debugf("run %s", "iptables -S")
	if buf.String() != "debug: run iptables -S\n" {
		t.Errorf("got %q", buf.String())
	}
}
