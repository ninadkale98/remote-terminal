package agent

import (
	"strconv"
	"strings"
)

// Render turns raw terminal output into plain text lines. It understands the
// handful of control sequences that matter for line-oriented output (carriage
// return, backspace, erase-in-line, cursor left/right, erase-characters) and
// drops every other escape sequence. partial reports that the last line had
// no newline yet, which is usually the shell prompt.
func Render(b []byte) (lines []string, partial bool) {
	r := &renderer{}
	rs := []rune(string(b))
	n := len(rs)
	for i := 0; i < n; {
		c := rs[i]
		switch {
		case c == 0x1b:
			if i+1 >= n {
				i = n
				continue
			}
			switch rs[i+1] {
			case '[':
				j := i + 2
				for j < n && !(rs[j] >= 0x40 && rs[j] <= 0x7e) {
					j++
				}
				if j >= n {
					i = n
					continue
				}
				r.csi(string(rs[i+2:j]), rs[j])
				i = j + 1
			case ']', 'P', '_', '^':
				j := i + 2
				for j < n {
					if rs[j] == 0x07 {
						j++
						break
					}
					if rs[j] == 0x1b && j+1 < n && rs[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				i = j
			default:
				j := i + 1
				for j < n && rs[j] >= 0x20 && rs[j] <= 0x2f {
					j++
				}
				i = j + 1
			}
		case c == '\r':
			r.col = 0
			i++
		case c == '\n':
			r.newline()
			i++
		case c == '\b':
			if r.col > 0 {
				r.col--
			}
			i++
		case c == '\t':
			for k := 8 - r.col%8; k > 0; k-- {
				r.put(' ')
			}
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			r.put(c)
			i++
		}
	}
	lines = r.lines
	if len(r.cur) > 0 {
		lines = append(lines, strings.TrimRight(string(r.cur), " "))
		partial = true
	}
	return lines, partial
}

type renderer struct {
	lines []string
	cur   []rune
	col   int
}

func (r *renderer) put(c rune) {
	for len(r.cur) < r.col {
		r.cur = append(r.cur, ' ')
	}
	if r.col < len(r.cur) {
		r.cur[r.col] = c
	} else {
		r.cur = append(r.cur, c)
	}
	r.col++
}

func (r *renderer) newline() {
	r.lines = append(r.lines, strings.TrimRight(string(r.cur), " "))
	r.cur = nil
	r.col = 0
}

func (r *renderer) csi(params string, final rune) {
	if strings.HasPrefix(params, "?") || strings.HasPrefix(params, ">") {
		return
	}
	num := func(def int) int {
		p := params
		if k := strings.IndexByte(p, ';'); k >= 0 {
			p = p[:k]
		}
		v, err := strconv.Atoi(p)
		if err != nil || v == 0 {
			return def
		}
		return v
	}
	switch final {
	case 'K':
		switch num(0) {
		case 0:
			if r.col < len(r.cur) {
				r.cur = r.cur[:r.col]
			}
		case 1:
			for k := 0; k < r.col && k < len(r.cur); k++ {
				r.cur[k] = ' '
			}
		case 2:
			r.cur = nil
		}
	case 'C':
		r.col += num(1)
	case 'D':
		r.col -= num(1)
		if r.col < 0 {
			r.col = 0
		}
	case 'G':
		r.col = num(1) - 1
	case 'X':
		for k := r.col; k < r.col+num(1) && k < len(r.cur); k++ {
			r.cur[k] = ' '
		}
	case 'H', 'f':
		if k := strings.IndexByte(params, ';'); k >= 0 {
			if v, err := strconv.Atoi(params[k+1:]); err == nil && v > 0 {
				r.col = v - 1
			}
		} else {
			r.col = 0
		}
	}
}

// LastLines returns the last n lines of rendered output as text.
func LastLines(b []byte, n int) string {
	lines, _ := Render(b)
	lines = trimBlank(lines)
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// cleanRun extracts a command's own output: it drops the echoed command line
// and, unless the end was cut at the hook's marker, the trailing prompt.
func cleanRun(raw []byte, typed string, cutAtMarker bool) (string, bool) {
	return truncate(runLines(raw, typed, cutAtMarker))
}

// runLines is cleanRun without the length limit.
func runLines(raw []byte, typed string, cutAtMarker bool) []string {
	lines, partial := Render(raw)
	if partial && !cutAtMarker && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	probe := strings.TrimSpace(typed)
	if len(probe) > 40 {
		probe = probe[:40]
	}
	if probe != "" {
		for i := 0; i < len(lines) && i < 4; i++ {
			if strings.Contains(lines[i], probe) {
				lines = lines[i+1:]
				break
			}
		}
	}
	return trimBlank(lines)
}

const (
	maxLineLen  = 2000
	headLines   = 100
	tailLines   = 300
	maxOutLines = headLines + tailLines
)

func truncate(lines []string) (string, bool) {
	cut := false
	for i, l := range lines {
		if len(l) > maxLineLen {
			lines[i] = l[:maxLineLen] + " …"
			cut = true
		}
	}
	if len(lines) > maxOutLines {
		omitted := len(lines) - maxOutLines
		out := append([]string{}, lines[:headLines]...)
		out = append(out, "… ["+strconv.Itoa(omitted)+" lines omitted] …")
		out = append(out, lines[len(lines)-tailLines:]...)
		lines = out
		cut = true
	}
	return strings.Join(lines, "\n"), cut
}
