package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ninadkale98/remote-terminal/internal/paths"
	"github.com/ninadkale98/remote-terminal/internal/proto"
)

// PTY is a running shell attached to a pseudo terminal.
type PTY interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Pid() int
	Wait() error
}

const (
	defaultCols = 200
	defaultRows = 50
	bufferSize  = 4 << 20
)

var oscMarker = []byte("\x1b]133;D")

// Session is one shell that Claude and any number of viewers share.
type Session struct {
	Name    string
	shell   string
	pty     PTY
	buf     *Buffer
	state   string
	logPath string
	done    chan struct{}

	mu        sync.Mutex
	busy      bool
	pending   bool
	pendingN  int
	runStart  int64
	lastTyped string
	scriptN   int
	viewers   map[*viewer]struct{}
}

type viewer struct {
	ch chan []byte
}

func newSession(name string) (*Session, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	state := filepath.Join(paths.StateDir(), name)
	_ = os.Remove(state)
	argv, shell, err := shellArgv()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	p, err := startPTY(argv, sessionEnv(name, state), home, defaultCols, defaultRows)
	if err != nil {
		return nil, err
	}
	s := &Session{
		Name:    name,
		shell:   shell,
		pty:     p,
		buf:     NewBuffer(bufferSize),
		state:   state,
		logPath: filepath.Join(paths.LogDir(), name+".jsonl"),
		done:    make(chan struct{}),
		viewers: map[*viewer]struct{}{},
	}
	go s.readLoop()
	go func() {
		_ = p.Wait()
		_ = p.Close()
		close(s.done)
	}()
	return s, nil
}

func (s *Session) readLoop() {
	b := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(b)
		if n > 0 {
			chunk := append([]byte(nil), b[:n]...)
			s.buf.Write(chunk)
			s.mu.Lock()
			for v := range s.viewers {
				select {
				case v.ch <- chunk:
				default: // a slow viewer misses output rather than stalling the shell
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	s.mu.Lock()
	for v := range s.viewers {
		close(v.ch)
	}
	s.viewers = nil
	s.mu.Unlock()
}

func (s *Session) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *Session) info() proto.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proto.SessionInfo{Name: s.Name, Shell: s.shell, PID: s.pty.Pid(), Busy: s.busy || s.pending, Viewers: len(s.viewers)}
}

// readState returns the prompt count and last exit code written by the hook.
func (s *Session) readState() (int, int, bool) {
	b, err := os.ReadFile(s.state)
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(strings.TrimPrefix(string(b), "\ufeff"))
	if len(f) < 2 {
		return 0, 0, false
	}
	n, err1 := strconv.Atoi(f[0])
	c, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return n, c, true
}

func (s *Session) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, _, ok := s.readState(); ok {
			return nil
		}
		if s.exited() {
			return errors.New("the shell exited during startup: " + LastLines(s.buf.Tail(4096), 10))
		}
		if time.Now().After(deadline) {
			return errors.New("the shell did not show a prompt within " + timeout.String() +
				"; screen so far:\n" + LastLines(s.buf.Tail(4096), 10))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func busy(msg string) proto.Response {
	return proto.Response{V: proto.Proto, OK: false, Status: "busy", Error: msg}
}

// Run types cmd into the shared shell, waits for it to finish and returns its
// output and exit code. If it is still running at the timeout, Run returns
// status "running" and the command carries on in the session.
func (s *Session) Run(cmd string, timeout time.Duration) proto.Response {
	if err := s.waitReady(20 * time.Second); err != nil {
		return proto.Err(err.Error())
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return busy("another rterm command is running in this session")
	}
	n0, _, _ := s.readState()
	if s.pending && n0 <= s.pendingN {
		s.mu.Unlock()
		return busy("the previous command is still running; use read, wait or keys C-c")
	}
	s.pending = false
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	typed := cmd
	if strings.ContainsAny(cmd, "\r\n") {
		var err error
		if typed, err = s.saveScript(cmd); err != nil {
			return proto.Err("saving multi-line command: " + err.Error())
		}
	}
	start := s.buf.End()
	s.mu.Lock()
	s.runStart = start
	s.lastTyped = typed
	s.mu.Unlock()
	t0 := time.Now()
	if _, err := s.pty.Write([]byte(typed + "\r")); err != nil {
		return proto.Err("typing into the session: " + err.Error())
	}
	deadline := t0.Add(timeout)
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			out, _ := cleanRun(s.buf.Slice(start, -1), typed, false)
			return proto.Response{V: proto.Proto, OK: false, Error: "the shell exited", Output: out}
		case <-tick.C:
		}
		if n, code, ok := s.readState(); ok && n > n0 {
			raw, cut := s.settle(start)
			out, trunc := cleanRun(raw, typed, cut)
			c := code
			s.audit(map[string]any{"op": "run", "cmd": cmd, "exit_code": code, "ms": time.Since(t0).Milliseconds()})
			return proto.Response{V: proto.Proto, OK: true, Status: "done", ExitCode: &c, Output: out,
				Truncated: trunc, DurationMs: time.Since(t0).Milliseconds()}
		}
		if time.Now().After(deadline) {
			s.mu.Lock()
			s.pending = true
			s.pendingN = n0
			s.mu.Unlock()
			out, _ := cleanRun(s.buf.Slice(start, -1), typed, true)
			lines := strings.Split(out, "\n")
			if len(lines) > 50 {
				lines = lines[len(lines)-50:]
			}
			s.audit(map[string]any{"op": "run", "cmd": cmd, "status": "running", "ms": time.Since(t0).Milliseconds()})
			return proto.Response{V: proto.Proto, OK: true, Status: "running", Output: strings.Join(lines, "\n"),
				DurationMs: time.Since(t0).Milliseconds()}
		}
	}
}

// settle waits for the prompt marker (or for output to go quiet) and returns
// the command's raw output, cut at the marker when one was seen.
func (s *Session) settle(start int64) ([]byte, bool) {
	limit := time.Now().Add(1500 * time.Millisecond)
	for {
		raw := s.buf.Slice(start, -1)
		if i := bytes.Index(raw, oscMarker); i >= 0 {
			return raw[:i], true
		}
		if s.buf.Idle() > 250*time.Millisecond || time.Now().After(limit) {
			return raw, false
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (s *Session) saveScript(cmd string) (string, error) {
	s.mu.Lock()
	s.scriptN++
	n := s.scriptN
	s.mu.Unlock()
	path, invoke := scriptFile(paths.TmpDir(), fmt.Sprintf("%s-%d", s.Name, n))
	body := strings.ReplaceAll(cmd, "\r\n", "\n")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		return "", err
	}
	return invoke(path), nil
}

// Read returns the last n lines on screen.
func (s *Session) Read(n int) proto.Response {
	if n <= 0 {
		n = 60
	}
	return proto.Response{V: proto.Proto, OK: true, Output: LastLines(s.buf.Tail(256<<10), n)}
}

var namedKeys = map[string]string{
	"enter": "\r", "return": "\r", "tab": "\t", "esc": "\x1b", "escape": "\x1b",
	"bspace": "\x7f", "backspace": "\x7f", "space": " ", "up": "\x1b[A", "down": "\x1b[B",
	"right": "\x1b[C", "left": "\x1b[D", "home": "\x1b[H", "end": "\x1b[F",
	"pgup": "\x1b[5~", "pgdn": "\x1b[6~", "delete": "\x1b[3~", "del": "\x1b[3~",
}

// KeyBytes turns key names (Enter, C-c, Up …) or literal text into bytes.
func KeyBytes(keys []string) []byte {
	var out []byte
	for _, k := range keys {
		if v, ok := namedKeys[strings.ToLower(k)]; ok {
			out = append(out, v...)
			continue
		}
		if len(k) == 3 && (k[0] == 'C' || k[0] == 'c') && k[1] == '-' {
			c := k[2]
			if c >= 'a' && c <= 'z' {
				c -= 'a' - 'A'
			}
			if c >= '@' && c <= '_' {
				out = append(out, c&0x1f)
				continue
			}
		}
		out = append(out, k...)
	}
	return out
}

// Keys sends keystrokes to the session.
func (s *Session) Keys(keys []string) proto.Response {
	b := KeyBytes(keys)
	if len(b) == 0 {
		return proto.Err("no keys given")
	}
	if _, err := s.pty.Write(b); err != nil {
		return proto.Err(err.Error())
	}
	s.audit(map[string]any{"op": "keys", "keys": keys})
	return proto.Response{V: proto.Proto, OK: true}
}

// Wait blocks until pattern appears in the output of the latest command (or
// recent output if there was none), or until the timeout.
func (s *Session) Wait(pattern string, timeout time.Duration) proto.Response {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return proto.Err("bad pattern: " + err.Error())
	}
	s.mu.Lock()
	from, typed := s.runStart, s.lastTyped
	s.mu.Unlock()
	if from == 0 {
		from = s.buf.End() - 64<<10
	}
	tail := func(lines []string) string {
		if len(lines) > 30 {
			lines = lines[len(lines)-30:]
		}
		return strings.Join(lines, "\n")
	}
	deadline := time.Now().Add(timeout)
	for {
		changed := s.buf.Changed()
		// Match only the command's own output, not the echoed command line.
		lines := runLines(s.buf.Slice(from, -1), typed, true)
		if re.MatchString(strings.Join(lines, "\n")) {
			return proto.Response{V: proto.Proto, OK: true, Status: "matched", Output: tail(lines)}
		}
		left := time.Until(deadline)
		if left <= 0 {
			return proto.Response{V: proto.Proto, OK: true, Status: "timeout", Output: tail(lines)}
		}
		select {
		case <-changed:
		case <-s.done:
			return proto.Response{V: proto.Proto, OK: false, Error: "the shell exited", Output: tail(lines)}
		case <-time.After(left):
		}
	}
}

// Attach relays the session to a viewer until either side closes.
func (s *Session) Attach(conn net.Conn, r *bufio.Reader, cols, rows int, readOnly bool) {
	defer conn.Close()
	if cols > 0 && rows > 0 {
		_ = s.pty.Resize(cols, rows)
	}
	v := &viewer{ch: make(chan []byte, 1024)}
	s.mu.Lock()
	if s.viewers == nil {
		s.mu.Unlock()
		return
	}
	s.viewers[v] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.viewers, v)
		s.mu.Unlock()
	}()

	// Replay recent output, starting at a line boundary.
	replay := s.buf.Tail(64 << 10)
	if i := bytes.IndexByte(replay, '\n'); i >= 0 && len(replay) == 64<<10 {
		replay = replay[i+1:]
	}
	if _, err := conn.Write(append([]byte("\x1b[0m\r\n"), replay...)); err != nil {
		return
	}

	quit := make(chan struct{})
	go func() {
		defer close(quit)
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			if n > 0 && !readOnly {
				_, _ = s.pty.Write(b[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case chunk, ok := <-v.ch:
			if !ok {
				return
			}
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		case <-quit:
			return
		}
	}
}

func (s *Session) audit(rec map[string]any) {
	rec["time"] = time.Now().Format(time.RFC3339)
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
