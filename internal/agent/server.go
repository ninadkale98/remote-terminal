package agent

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/rterm/rterm/internal/paths"
	"github.com/rterm/rterm/internal/proto"
)

// Info is what agent.json holds so local commands can reach the agent.
type Info struct {
	Addr    string `json:"addr"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Agent owns the sessions on this machine and serves requests on localhost.
type Agent struct {
	mu       sync.Mutex
	sessions map[string]*Session
	token    string
}

var validName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Serve runs the agent in the foreground until it is told to stop.
func Serve() error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	if _, err := Call(proto.Request{Op: "status"}); err == nil {
		return errors.New("an rterm agent is already running on this machine")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	a := &Agent{sessions: map[string]*Session{}, token: hex.EncodeToString(tok)}
	info := Info{Addr: ln.Addr().String(), Token: a.token, PID: os.Getpid(), Version: proto.Version}
	b, _ := json.Marshal(info)
	tmp := paths.AgentFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, paths.AgentFile()); err != nil {
		return err
	}
	if _, err := a.session(proto.DefaultSession); err != nil {
		fmt.Fprintln(os.Stderr, "rterm agent: starting the default session:", err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go a.handle(conn)
	}
}

func (a *Agent) session(name string) (*Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[name]; ok && !s.exited() {
		return s, nil
	}
	s, err := newSession(name)
	if err != nil {
		return nil, err
	}
	a.sessions[name] = s
	go func() {
		<-s.done
		a.mu.Lock()
		if a.sessions[name] == s {
			delete(a.sessions, name)
		}
		a.mu.Unlock()
	}()
	return s, nil
}

func (a *Agent) status() proto.Response {
	host, _ := os.Hostname()
	resp := proto.Response{V: proto.Proto, OK: true, Version: proto.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host}
	a.mu.Lock()
	for _, s := range a.sessions {
		resp.Sessions = append(resp.Sessions, s.info())
	}
	a.mu.Unlock()
	return resp
}

func (a *Agent) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	var req proto.Request
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := proto.ReadLine(r, &req); err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	reply := func(resp proto.Response) {
		_ = proto.Write(conn, resp)
		conn.Close()
	}
	if req.Token != a.token {
		reply(proto.Err("bad agent token"))
		return
	}
	name := req.Session
	if name == "" {
		name = proto.DefaultSession
	}
	if !validName.MatchString(name) {
		reply(proto.Err("session names may use letters, digits, - and _ (max 32)"))
		return
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond

	switch req.Op {
	case "status":
		reply(a.status())
		return
	case "shutdown":
		reply(proto.Response{V: proto.Proto, OK: true})
		a.mu.Lock()
		for _, s := range a.sessions {
			_ = s.pty.Close()
		}
		a.mu.Unlock()
		_ = os.Remove(paths.AgentFile())
		os.Exit(0)
	case "kill":
		a.mu.Lock()
		s, ok := a.sessions[name]
		a.mu.Unlock()
		if ok {
			_ = s.pty.Close()
		}
		reply(proto.Response{V: proto.Proto, OK: true})
		return
	case "run", "read", "keys", "wait", "attach":
	default:
		reply(proto.Err("unknown op " + req.Op))
		return
	}

	s, err := a.session(name)
	if err != nil {
		reply(proto.Err("starting session " + name + ": " + err.Error()))
		return
	}
	switch req.Op {
	case "run":
		if req.Cmd == "" {
			reply(proto.Err("no command given"))
			return
		}
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		reply(s.Run(req.Cmd, timeout))
	case "read":
		reply(s.Read(req.Lines))
	case "keys":
		reply(s.Keys(req.Keys))
	case "wait":
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		reply(s.Wait(req.Pattern, timeout))
	case "attach":
		if err := proto.Write(conn, proto.Response{V: proto.Proto, OK: true}); err != nil {
			conn.Close()
			return
		}
		s.Attach(conn, r, req.Cols, req.Rows, req.ReadOnly)
	}
}

// LoadInfo reads agent.json.
func LoadInfo() (Info, error) {
	var info Info
	b, err := os.ReadFile(paths.AgentFile())
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(b, &info)
	return info, err
}

// Open connects to the local agent and sends req, returning the connection
// for ops (attach) that keep it open.
func Open(req proto.Request) (net.Conn, *bufio.Reader, proto.Response, error) {
	var resp proto.Response
	info, err := LoadInfo()
	if err != nil {
		return nil, nil, resp, err
	}
	conn, err := net.DialTimeout("tcp", info.Addr, 2*time.Second)
	if err != nil {
		return nil, nil, resp, err
	}
	req.Token = info.Token
	if err := proto.Write(conn, req); err != nil {
		conn.Close()
		return nil, nil, resp, err
	}
	r := bufio.NewReader(conn)
	if err := proto.ReadLine(r, &resp); err != nil {
		conn.Close()
		return nil, nil, resp, err
	}
	return conn, r, resp, nil
}

// Call sends one request to the local agent and returns its response.
func Call(req proto.Request) (proto.Response, error) {
	conn, _, resp, err := Open(req)
	if err != nil {
		return resp, err
	}
	conn.Close()
	return resp, nil
}
