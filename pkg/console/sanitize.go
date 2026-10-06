package console

import (
	"bytes"
	"strconv"
	"strings"
)

// maxPad caps the spaces emitted for one cursor move.
const maxPad = 200

// sanitizeHistory turns a raw serial log into text that can be printed into
// the user's terminal without repainting it. It is an allowlist: printable
// text, CR, LF, TAB and colour (SGR, ESC[...m) sequences are kept; every
// other control or escape sequence is dropped. Cursor moves are rendered
// instead of executed: a move to another row starts a new line indented to
// the target column, a move along the same row becomes spaces. Screens drawn
// by cursor addressing (UEFI Boot Manager, GRUB, shell echo) therefore stay
// readable line by line. Incomplete sequences at the end are discarded.
func sanitizeHistory(b []byte) []byte {
	s := &screen{row: 1, col: 1}
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			e, ok := parseEscape(b[i:])
			if !ok { // truncated sequence: drop the rest
				return s.out.Bytes()
			}
			s.escape(e, b[i:i+e.n])
			i += e.n
		case c == '\n':
			s.out.WriteByte(c)
			s.row++
			i++
		case c == '\r':
			s.out.WriteByte(c)
			s.col = 1
			i++
		case c == '\t' || c >= 0x20 && c != 0x7f:
			s.out.WriteByte(c)
			if c < 0x80 || c >= 0xc0 { // not a UTF-8 continuation byte
				s.col++
			}
			i++
		default: // other C0 controls (BEL, BS, SO/SI...)
			i++
		}
	}
	return s.out.Bytes()
}

// screen tracks where the cursor would be, to render cursor moves as text.
type screen struct {
	out      bytes.Buffer
	row, col int
}

func (s *screen) escape(e esc, raw []byte) {
	if e.final == 0 {
		return // non-CSI sequence: dropped
	}
	switch e.final {
	case 'm':
		s.out.Write(raw)
	case 'H', 'f': // cursor position: row;col
		s.moveTo(e.param(0, 1), e.param(1, 1))
	case 'd': // row, same column
		s.moveTo(e.param(0, 1), s.col)
	case 'G': // column, same row
		s.moveTo(s.row, e.param(0, 1))
	case 'C': // forward
		s.moveTo(s.row, s.col+e.param(0, 1))
	case 'E': // next line
		s.moveTo(s.row+e.param(0, 1), 1)
	}
}

func (s *screen) moveTo(row, col int) {
	if row != s.row {
		if s.out.Len() > 0 && !bytes.HasSuffix(s.out.Bytes(), []byte("\n")) {
			s.out.WriteString("\r\n")
		}
		s.row, s.col = row, 1
	}
	if col > s.col {
		s.out.WriteString(strings.Repeat(" ", min(col-s.col, maxPad)))
		s.col = col
	}
	// Moving left cannot be rendered in a scrolling log; stay put.
}

// esc is a parsed escape sequence: its length and, for CSI, the final byte
// and parameters.
type esc struct {
	n      int
	final  byte
	params []string
}

// param returns the i-th numeric parameter, or def when absent or zero.
func (e esc) param(i, def int) int {
	if i >= len(e.params) {
		return def
	}
	v, err := strconv.Atoi(e.params[i])
	if err != nil || v < 1 {
		return def
	}
	return v
}

// parseEscape parses the escape sequence at the start of b (b[0] == ESC).
// ok is false when b ends before the sequence does.
func parseEscape(b []byte) (e esc, ok bool) {
	if len(b) < 2 {
		return e, false
	}
	switch b[1] {
	case '[': // CSI: params 0x30-0x3F, intermediates 0x20-0x2F, final 0x40-0x7E
		j := 2
		for j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f {
			j++
		}
		params := string(b[2:j])
		for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
			j++
		}
		if j >= len(b) {
			return e, false
		}
		if b[j] < 0x40 || b[j] > 0x7e {
			return esc{n: j}, true // malformed: drop what was parsed
		}
		e = esc{n: j + 1, final: b[j]}
		// Private sequences (ESC[?25l, ESC[=3h, ESC[>c) never move the
		// cursor or set colours: drop them.
		if params != "" && strings.ContainsAny(params[:1], "<=>?") {
			e.final = 'x'
			return e, true
		}
		if params != "" {
			e.params = strings.Split(params, ";")
		}
		return e, true
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: run to BEL or ST
		for j := 2; j < len(b); j++ {
			if b[1] == ']' && b[j] == 0x07 {
				return esc{n: j + 1}, true
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return esc{n: j + 2}, true
			}
		}
		return e, false
	case '(', ')', '*', '+', '#', '%': // charset / line attributes: one more byte
		if len(b) < 3 {
			return e, false
		}
		return esc{n: 3}, true
	}
	return esc{n: 2}, true // ESC c, ESC 7, ESC 8, ESC =, ESC >, ...
}
