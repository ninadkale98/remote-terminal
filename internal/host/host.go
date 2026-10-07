// Package host implements `rterm host …`: the commands that run on the
// machine Claude controls.
package host

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/rterm/rterm/internal/agent"
	"github.com/rterm/rterm/internal/paths"
	"github.com/rterm/rterm/internal/proto"
)

const usage = `rterm host — commands for the machine Claude controls

  rterm host init [--name NAME]    set up this machine and print the pairing line
  rterm host status                show the agent and its sessions
  rterm host start | stop          start or stop the session agent
  rterm host attach [--session S] [--readonly]
                                   watch a session in this terminal (Ctrl-] detaches)
  rterm host authorize --key KEY   allow a client's SSH key (normally done by 'rterm add')

Used over SSH by the client: host rpc, host serve, host version.
`

// Main dispatches `rterm host <sub>`.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 2
	}
	switch args[0] {
	case "init":
		return initCmd(args[1:])
	case "serve":
		if err := agent.Serve(); err != nil {
			fmt.Fprintln(os.Stderr, "rterm agent:", err)
			return 1
		}
		return 0
	case "start":
		if err := EnsureAgent(); err != nil {
			fmt.Fprintln(os.Stderr, "✗", err)
			return 1
		}
		fmt.Println("✓ agent running")
		return 0
	case "stop":
		if _, err := agent.Call(proto.Request{Op: "shutdown"}); err != nil {
			fmt.Println("agent was not running")
			return 0
		}
		fmt.Println("✓ agent stopped")
		return 0
	case "status":
		return statusCmd()
	case "rpc":
		return rpc(os.Stdin, os.Stdout)
	case "attach":
		return attach(args[1:])
	case "authorize":
		return authorize(args[1:])
	case "version":
		fmt.Println(proto.Version)
		return 0
	default:
		fmt.Print(usage)
		return 2
	}
}

func versionResp() proto.Response {
	host, _ := os.Hostname()
	return proto.Response{V: proto.Proto, OK: true, Version: proto.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host}
}

// rpc reads one request from stdin, forwards it to the local agent (starting
// it if needed) and writes the response to stdout.
func rpc(in io.Reader, out io.Writer) int {
	var req proto.Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		_ = proto.Write(out, proto.Err("reading request: "+err.Error()))
		return 1
	}
	if req.Op == "version" {
		_ = proto.Write(out, versionResp())
		return 0
	}
	if err := EnsureAgent(); err != nil {
		_ = proto.Write(out, proto.Err(err.Error()))
		return 1
	}
	resp, err := agent.Call(req)
	if err != nil {
		resp = proto.Err("talking to the agent: " + err.Error())
	}
	_ = proto.Write(out, resp)
	return 0
}

// EnsureAgent starts the session agent in the background if it isn't running.
func EnsureAgent() error {
	if _, err := agent.Call(proto.Request{Op: "status"}); err == nil {
		return nil
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := startDetached(exe, "host", "serve"); err != nil {
		return fmt.Errorf("starting the agent: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := agent.Call(proto.Request{Op: "status"}); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	logTail, _ := os.ReadFile(paths.AgentLog())
	if len(logTail) > 800 {
		logTail = logTail[len(logTail)-800:]
	}
	return fmt.Errorf("the agent did not start; %s says:\n%s", paths.AgentLog(), strings.TrimSpace(string(logTail)))
}

func statusCmd() int {
	resp, err := agent.Call(proto.Request{Op: "status"})
	if err != nil {
		fmt.Println("✗ agent not running (start it with: rterm host start)")
		return 1
	}
	fmt.Printf("✓ agent %s running on %s (%s/%s)\n", resp.Version, resp.Hostname, resp.OS, resp.Arch)
	printSessions(resp.Sessions)
	return 0
}

func printSessions(ss []proto.SessionInfo) {
	sort.Slice(ss, func(i, j int) bool { return ss[i].Name < ss[j].Name })
	for _, s := range ss {
		state := "idle"
		if s.Busy {
			state = "busy"
		}
		fmt.Printf("  session %-12s %-10s pid %-7d %s, %d watching\n", s.Name, s.Shell, s.PID, state, s.Viewers)
	}
}

func attach(args []string) int {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	session := fs.String("session", proto.DefaultSession, "session name")
	readOnly := fs.Bool("readonly", false, "watch only; your keystrokes are not sent")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := EnsureAgent(); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}
	cols, rows := 0, 0
	if term.IsTerminal(int(os.Stdout.Fd())) {
		cols, rows, _ = term.GetSize(int(os.Stdout.Fd()))
	}
	conn, r, resp, err := agent.Open(proto.Request{Op: "attach", Session: *session, Cols: cols, Rows: rows, ReadOnly: *readOnly})
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ connecting to the agent:", err)
		return 1
	}
	defer conn.Close()
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "✗", resp.Error)
		return 1
	}
	host, _ := os.Hostname()
	mode := ""
	if *readOnly {
		mode = " read-only,"
	}
	restore := func() {}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if st, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
			restore = func() { _ = term.Restore(int(os.Stdin.Fd()), st) }
		}
	}
	enableVT()
	fmt.Fprintf(os.Stdout, "\x1b[2m[rterm] watching session %q on %s,%s Ctrl-] detaches\x1b[0m\r\n", *session, host, mode)

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(os.Stdout, r)
		close(done)
	}()
	go func() {
		b := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(b)
			if n > 0 {
				if i := bytes.IndexByte(b[:n], 0x1d); i >= 0 {
					_, _ = conn.Write(b[:i])
					conn.Close()
					return
				}
				if _, werr := conn.Write(b[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	<-done
	restore()
	fmt.Fprintf(os.Stdout, "\r\n\x1b[2m[rterm] detached\x1b[0m\r\n")
	return 0
}

func authorize(args []string) int {
	fs := flag.NewFlagSet("authorize", flag.ContinueOnError)
	key := fs.String("key", "", "public key (default: read from stdin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	k := strings.TrimSpace(*key)
	if k == "" {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		k = strings.TrimSpace(line)
	}
	if !(strings.HasPrefix(k, "ssh-") || strings.HasPrefix(k, "ecdsa-") || strings.HasPrefix(k, "sk-")) {
		fmt.Fprintln(os.Stderr, "✗ that doesn't look like an SSH public key")
		return 2
	}
	where, err := installKey(k)
	for _, w := range where {
		fmt.Println("✓ key added to", w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}
	return 0
}

// appendKey adds key to an authorized_keys file unless it's already there.
func appendKey(path, key string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing, _ := os.ReadFile(path)
	fields := strings.Fields(key)
	for _, line := range strings.Split(string(existing), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && len(fields) >= 2 && f[0] == fields[0] && f[1] == fields[1] {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + key + "\n")
	return err
}

func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	hn, _ := os.Hostname()
	name := fs.String("name", strings.ToLower(strings.Split(hn, ".")[0]), "name the client will use for this machine")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Printf("Setting up %s as an rterm host\n\n", hn)
	if err := paths.Ensure(); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}

	// 1. Install the binary at a fixed path the client can call over SSH.
	_, _ = agent.Call(proto.Request{Op: "shutdown"})
	time.Sleep(300 * time.Millisecond)
	installed := filepath.Join(paths.BinDir(), paths.ExeName())
	if err := installSelf(installed); err != nil {
		fmt.Fprintln(os.Stderr, "✗ installing to", installed+":", err)
		return 1
	}
	fmt.Println("✓ installed", installed)

	// 2. OS-specific setup: SSH server, PATH, autostart.
	sshOK := osInit(installed)

	// 3. Start the agent from the installed copy and check the shell hook.
	if err := startDetached(installed, "host", "serve"); err != nil {
		fmt.Fprintln(os.Stderr, "✗ starting the agent:", err)
		return 1
	}
	if err := waitAgent(10 * time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}
	resp, err := agent.Call(proto.Request{Op: "run", Cmd: "echo rterm-ready", TimeoutMs: 30000})
	switch {
	case err != nil:
		fmt.Println("✗ session check failed:", err)
	case !resp.OK:
		fmt.Println("✗ session check failed:", resp.Error)
	case resp.Status != "done" || !strings.Contains(resp.Output, "rterm-ready"):
		fmt.Printf("✗ session check gave unexpected output (status %s):\n%s\n", resp.Status, resp.Output)
	default:
		st, _ := agent.Call(proto.Request{Op: "status"})
		shell := ""
		for _, s := range st.Sessions {
			if s.Name == proto.DefaultSession {
				shell = s.Shell
			}
		}
		fmt.Printf("✓ session %q is ready (%s, completion hook working)\n", proto.DefaultSession, shell)
	}

	// 4. Pairing line.
	user := loginName()
	quotedBin := "'" + strings.ReplaceAll(installed, "'", `'\''`) + "'"
	ips := localIPs()
	fmt.Println()
	if !sshOK {
		fmt.Println("Fix the SSH server step above first, then pair from machine 1 with:")
	} else {
		fmt.Println("Done. On machine 1 (where Claude runs), pair with:")
	}
	if len(ips) == 0 {
		fmt.Printf("\n  rterm add %s '%s@<this-machine-ip>' --remote-bin %s\n", *name, user, quotedBin)
	}
	for _, ip := range ips {
		fmt.Printf("\n  rterm add %s '%s@%s' --remote-bin %s      # via %s\n", *name, user, ip.addr, quotedBin, ip.iface)
	}
	if len(ips) > 1 {
		fmt.Println("\nUse the address machine 1 can reach (on a VPN, usually the VPN adapter's).")
	}
	return 0
}

func waitAgent(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := agent.Call(proto.Request{Op: "status"}); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	b, _ := os.ReadFile(paths.AgentLog())
	return errors.New("the agent did not start:\n" + strings.TrimSpace(string(b)))
}

func installSelf(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if s, err := filepath.EvalSymlinks(src); err == nil {
		src = s
	}
	if d, err := filepath.EvalSymlinks(dst); err == nil && d == src {
		return nil
	}
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, in, 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

type ipAddr struct{ addr, iface string }

func localIPs() []ipAddr {
	var out []ipAddr
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			out = append(out, ipAddr{ip.String(), ifc.Name})
		}
	}
	return out
}

func sshListening() bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:22", time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
