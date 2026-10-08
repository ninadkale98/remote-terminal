// Package client implements the commands that run on machine 1, where
// Claude runs: pairing, run/read/keys/wait, watch and doctor.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ninadkale98/remote-terminal/internal/docs"
	"github.com/ninadkale98/remote-terminal/internal/paths"
	"github.com/ninadkale98/remote-terminal/internal/proto"
)

// Host is one paired machine.
type Host struct {
	Name      string `json:"name"`
	User      string `json:"user,omitempty"`
	Addr      string `json:"addr,omitempty"`
	Port      int    `json:"port,omitempty"`
	RemoteBin string `json:"remote_bin,omitempty"`
	OS        string `json:"os,omitempty"`
	Transport string `json:"transport,omitempty"` // "ssh" (default) or "local" (testing)
}

func loadHosts() (map[string]Host, error) {
	hosts := map[string]Host{}
	b, err := os.ReadFile(paths.HostsFile())
	if errors.Is(err, os.ErrNotExist) {
		return hosts, nil
	}
	if err != nil {
		return nil, err
	}
	return hosts, json.Unmarshal(b, &hosts)
}

func saveHosts(hosts map[string]Host) error {
	if err := os.MkdirAll(paths.Dir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(hosts, "", "  ")
	return os.WriteFile(paths.HostsFile(), b, 0o600)
}

func getHost(name string) (Host, error) {
	hosts, err := loadHosts()
	if err != nil {
		return Host{}, err
	}
	h, ok := hosts[name]
	if !ok {
		return Host{}, fmt.Errorf("no machine named %q; pair it with: rterm add %s user@address", name, name)
	}
	return h, nil
}

// remoteCmd is the command line run on the host through SSH.
func (h Host) remoteCmd(args ...string) string {
	bin := h.RemoteBin
	if bin == "" {
		bin = "rterm"
	}
	if strings.ContainsAny(bin, " \t") {
		bin = `"` + bin + `"`
	}
	return bin + " " + strings.Join(args, " ")
}

func (h Host) command(tty bool, args ...string) *exec.Cmd {
	if h.Transport == "local" {
		return exec.Command(h.RemoteBin, args...)
	}
	sshArgs := []string{"-o", "ConnectTimeout=15"}
	if tty {
		sshArgs = append(sshArgs, "-t")
	} else {
		sshArgs = append(sshArgs, "-o", "BatchMode=yes")
	}
	sshArgs = append(sshArgs, h.Name, h.remoteCmd(args...))
	return exec.Command("ssh", sshArgs...)
}

// call sends one request to the host's agent and returns its response.
func (h Host) call(req proto.Request) (proto.Response, error) {
	var resp proto.Response
	body, _ := json.Marshal(req)
	cmd := h.command(false, "host", "rpc")
	cmd.Stdin = bytes.NewReader(append(body, '\n'))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &resp) == nil {
			return resp, nil
		}
	}
	msg := strings.TrimSpace(errb.String() + "\n" + out.String())
	if runErr != nil && msg == "" {
		msg = runErr.Error()
	}
	return resp, fmt.Errorf("could not reach rterm on %s: %s", h.Name, msg)
}

// ---------------------------------------------------------------- commands

// SessionHelp summarizes the per-machine commands.
const SessionHelp = `rterm NAME[:SESSION] <command>

  run [--timeout 120] "cmd"   type cmd into the persistent shell, wait, print output,
                              exit with its code (124 = still running at timeout)
  read [--lines 60]           show the current screen
  keys KEY…                   send keys: Enter Tab Esc Up Down C-c C-d … or literal text
  wait [--timeout 60] REGEX   wait until the latest command's output matches (124 = timeout)
  status                      list sessions on that machine
  kill                        restart this session's shell

State (directory, variables, jobs) persists between calls. Full manual: rterm guide
`

// Session runs `rterm <name>[:session] <verb> …`.
func Session(target string, args []string) int {
	name, session := target, proto.DefaultSession
	if i := strings.IndexByte(target, ':'); i >= 0 {
		name, session = target[:i], target[i+1:]
	}
	h, err := getHost(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 2
	}
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: rterm %s run|read|keys|wait|status|kill …\n", target)
		return 2
	}
	verb, rest := args[0], args[1:]
	flags, rest := takeFlags(rest, true)
	if verb == "help" || verb == "--help" || verb == "-h" || flags.bool("help") {
		fmt.Print(SessionHelp)
		return 0
	}
	req := proto.Request{Op: verb, Session: session}
	switch verb {
	case "run":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, `usage: rterm `+target+` run [--timeout SECONDS] "command"`)
			return 2
		}
		req.Cmd = strings.Join(rest, " ")
		req.TimeoutMs = flags.seconds("timeout", 120) * 1000
	case "read":
		req.Lines = flags.int("lines", 60)
	case "keys":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: rterm "+target+" keys C-c | Enter | Up | \"text\" …")
			return 2
		}
		req.Keys = rest
	case "wait":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, `usage: rterm `+target+` wait [--timeout SECONDS] "regex"`)
			return 2
		}
		req.Pattern = strings.Join(rest, " ")
		req.TimeoutMs = flags.seconds("timeout", 60) * 1000
	case "status", "kill":
	default:
		fmt.Fprintf(os.Stderr, "rterm: unknown command %q (run, read, keys, wait, status, kill)\n", verb)
		return 2
	}
	resp, err := h.call(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 255
	}
	if resp.Output != "" {
		fmt.Println(resp.Output)
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "rterm:", resp.Error)
		if resp.Status == "busy" {
			return 125
		}
		return 1
	}
	switch verb {
	case "run":
		secs := float64(resp.DurationMs) / 1000
		if resp.Status == "running" {
			fmt.Fprintf(os.Stderr, "[rterm: still running after %.0fs — check with: rterm %s read | rterm %s wait \"<regex>\" | stop with: rterm %s keys C-c]\n",
				secs, target, target, target)
			return 124
		}
		code := 0
		if resp.ExitCode != nil {
			code = *resp.ExitCode
		}
		note := ""
		if resp.Truncated {
			note = ", output trimmed"
		}
		fmt.Fprintf(os.Stderr, "[rterm: exit %d, %.1fs%s]\n", code, secs, note)
		if code < 0 || code > 255 {
			return 1
		}
		return code
	case "wait":
		if resp.Status != "matched" {
			fmt.Fprintln(os.Stderr, "[rterm: pattern not seen before the timeout]")
			return 124
		}
	case "status":
		fmt.Printf("%s: rterm %s on %s (%s/%s)\n", h.Name, resp.Version, resp.Hostname, resp.OS, resp.Arch)
		sort.Slice(resp.Sessions, func(i, j int) bool { return resp.Sessions[i].Name < resp.Sessions[j].Name })
		for _, s := range resp.Sessions {
			state := "idle"
			if s.Busy {
				state = "busy"
			}
			fmt.Printf("  %s:%s  %s, %s, %d watching\n", h.Name, s.Name, s.Shell, state, s.Viewers)
		}
	}
	return 0
}

// Watch attaches this terminal to a session on the host.
func Watch(args []string) int {
	flags, rest := takeFlags(args)
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rterm watch NAME[:SESSION] [--readonly]")
		return 2
	}
	name, session := rest[0], proto.DefaultSession
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name, session = name[:i], name[i+1:]
	}
	h, err := getHost(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 2
	}
	a := []string{"host", "attach", "--session", session}
	if flags.bool("readonly") {
		a = append(a, "--readonly")
	}
	cmd := h.command(true, a...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 1
	}
	return 0
}

// List prints paired machines.
func List() int {
	hosts, err := loadHosts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 1
	}
	if len(hosts) == 0 {
		fmt.Println("No machines paired yet. Run 'rterm host init' on the other machine, then 'rterm add'.")
		return 0
	}
	names := make([]string, 0, len(hosts))
	for n := range hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := hosts[n]
		where := h.User + "@" + h.Addr
		if h.Transport == "local" {
			where = "this machine (local test)"
		}
		fmt.Printf("%-12s %-30s %s\n", n, where, h.OS)
	}
	return 0
}

// Remove unpairs a machine.
func Remove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rterm remove NAME")
		return 2
	}
	hosts, err := loadHosts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 1
	}
	delete(hosts, args[0])
	_ = saveHosts(hosts)
	_ = writeSSHBlock(args[0], "")
	_ = writeClaudeNote(hosts)
	_, _ = docs.InstallSkill(machinesText(hosts))
	fmt.Printf("✓ removed %s (its key stays authorized on that machine until you delete it from authorized_keys)\n", args[0])
	return 0
}

// Doctor checks the link to a host step by step.
func Doctor(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: rterm doctor NAME")
		return 2
	}
	h, err := getHost(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}
	return doctor(h)
}

func doctor(h Host) int {
	ok := true
	if h.Transport != "local" {
		if _, err := exec.LookPath("ssh"); err != nil {
			fmt.Println("✗ ssh is not installed on this machine")
			return 1
		}
	}
	v, err := h.call(proto.Request{Op: "version"})
	if err != nil {
		fmt.Println("✗", err)
		return 1
	}
	fmt.Printf("✓ reached %s (%s/%s), rterm %s\n", v.Hostname, v.OS, v.Arch, v.Version)
	if v.Version != proto.Version {
		fmt.Printf("✗ version mismatch: this machine has %s, %s has %s; install the same release on both\n", proto.Version, h.Name, v.Version)
		ok = false
	}
	st, err := h.call(proto.Request{Op: "status"})
	switch {
	case err != nil:
		fmt.Println("✗", err)
		ok = false
	case !st.OK:
		fmt.Println("✗ agent:", st.Error)
		ok = false
	default:
		fmt.Printf("✓ agent running with %d session(s)\n", len(st.Sessions))
		for _, s := range st.Sessions {
			fmt.Printf("    %s:%s  %s\n", h.Name, s.Name, s.Shell)
		}
	}
	if !ok {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- pairing

var validName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Add pairs a machine: key, SSH config entry, checks, CLAUDE.md note.
func Add(args []string) int {
	flags, rest := takeFlags(args)
	if len(rest) < 1 || (len(rest) < 2 && !flags.bool("local")) {
		fmt.Fprintln(os.Stderr, "usage: rterm add NAME USER@ADDRESS [--remote-bin PATH] [--port N]")
		return 2
	}
	name := rest[0]
	if !validName.MatchString(name) {
		fmt.Fprintln(os.Stderr, "rterm: names may use letters, digits, - and _")
		return 2
	}
	hosts, err := loadHosts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rterm:", err)
		return 1
	}

	if flags.bool("local") { // test mode: the "remote" is this machine
		exe, _ := os.Executable()
		h := Host{Name: name, Transport: "local", RemoteBin: exe}
		if v, err := h.call(proto.Request{Op: "version"}); err == nil {
			h.OS = v.OS
		}
		hosts[name] = h
		_ = saveHosts(hosts)
		_ = writeClaudeNote(hosts)
		_ = InstallSkill()
		fmt.Println("✓ added", name, "(local test transport)")
		return doctor(h)
	}

	target := rest[1]
	at := strings.LastIndexByte(target, '@')
	if at <= 0 || at == len(target)-1 {
		fmt.Fprintln(os.Stderr, "rterm: give the address as USER@ADDRESS, e.g. ninad@10.20.4.15")
		return 2
	}
	h := Host{Name: name, User: target[:at], Addr: target[at+1:], Port: flags.int("port", 22), RemoteBin: flags.str("remote-bin", "rterm")}
	for _, tool := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s is not installed (install OpenSSH client)\n", tool)
			return 1
		}
	}

	// 1. Key.
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(sshDir, 0o700)
	key := filepath.Join(sshDir, "rterm_ed25519")
	if _, err := os.Stat(key); err != nil {
		hn, _ := os.Hostname()
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "rterm@"+hn, "-f", key).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ creating a key: %v %s\n", err, out)
			return 1
		}
		fmt.Println("✓ created key", key)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}

	base := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=15", "-p", strconv.Itoa(h.Port), "-l", h.User}
	keyAuth := func() bool {
		a := append(append([]string{}, base...), "-i", key, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", h.Addr, h.remoteCmd("host", "version"))
		return exec.Command("ssh", a...).Run() == nil
	}

	// 2. Authorize the key (one password prompt).
	if keyAuth() {
		fmt.Println("✓ key already authorized on", h.Addr)
	} else {
		fmt.Printf("Copying your key to %s. Enter %s's password for that machine when asked.\n", h.Addr, h.User)
		a := append(append([]string{}, base...), h.Addr, h.remoteCmd("host", "authorize"))
		cmd := exec.Command("ssh", a...)
		cmd.Stdin = bytes.NewReader(pub)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "✗ could not authorize the key over SSH.")
			fmt.Fprintln(os.Stderr, "  Check that machine 2 is reachable on port", h.Port, "and that 'rterm host init' ran there,")
			fmt.Fprintln(os.Stderr, "  and that --remote-bin matches the path it printed.")
			return 1
		}
		if !keyAuth() {
			fmt.Fprintln(os.Stderr, "✗ the key was copied but SSH still asks for a password.")
			fmt.Fprintln(os.Stderr, "  On a Windows machine 2, open an Administrator PowerShell there and run:")
			fmt.Fprintf(os.Stderr, "    rterm host authorize --key \"%s\"\n", strings.TrimSpace(string(pub)))
			fmt.Fprintln(os.Stderr, "  then run this 'rterm add' command again.")
			return 1
		}
		fmt.Println("✓ key authorized; no more passwords")
	}

	// 3. SSH config entry with connection reuse.
	block := fmt.Sprintf(`Host %s
  HostName %s
  User %s
  Port %d
  IdentityFile %s
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
  ControlMaster auto
  ControlPath ~/.ssh/rterm-%%C
  ControlPersist 10m
  ServerAliveInterval 30
`, name, h.Addr, h.User, h.Port, key)
	if err := writeSSHBlock(name, block); err != nil {
		fmt.Fprintln(os.Stderr, "✗ writing ~/.ssh/config:", err)
		return 1
	}
	fmt.Printf("✓ ~/.ssh/config: Host %s (connection reuse on)\n", name)

	// 4. Check and save.
	if v, err := h.call(proto.Request{Op: "version"}); err == nil {
		h.OS = v.OS
	}
	hosts[name] = h
	if err := saveHosts(hosts); err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		return 1
	}
	rc := doctor(h)
	if err := writeClaudeNote(hosts); err == nil {
		fmt.Println("✓ told Claude about", name, "in ~/.claude/CLAUDE.md")
	}
	_ = InstallSkill()
	if rc == 0 {
		fmt.Printf("\nReady. Watch it with:  rterm watch %s\nTry it with:           rterm %s run \"%s\"\n", name, name, helloCmd(h.OS))
	}
	return rc
}

func helloCmd(goos string) string {
	if goos == "windows" {
		return "Get-Date; hostname"
	}
	return "date; hostname"
}

const sshBegin, sshEnd = "# >>> rterm %s >>>", "# <<< rterm %s <<<"

// writeSSHBlock puts (or removes, if block is empty) this host's entry at
// the top of ~/.ssh/config, so it takes precedence over broader entries.
func writeSSHBlock(name, block string) error {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".ssh", "config")
	old, _ := os.ReadFile(path)
	begin, end := fmt.Sprintf(sshBegin, name), fmt.Sprintf(sshEnd, name)
	text := string(old)
	if i := strings.Index(text, begin); i >= 0 {
		if j := strings.Index(text[i:], end); j >= 0 {
			text = text[:i] + strings.TrimLeft(text[i+j+len(end):], "\n")
		}
	}
	if block != "" {
		text = begin + "\n" + block + end + "\n\n" + text
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

const noteBegin, noteEnd = "<!-- rterm:begin -->", "<!-- rterm:end -->"

// writeClaudeNote keeps a short section in ~/.claude/CLAUDE.md describing the
// paired machines, so Claude knows how to use them.
func writeClaudeNote(hosts map[string]Host) error {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".claude", "CLAUDE.md")
	old, _ := os.ReadFile(path)
	text := string(old)
	if i := strings.Index(text, noteBegin); i >= 0 {
		if j := strings.Index(text[i:], noteEnd); j >= 0 {
			text = strings.TrimRight(text[:i], "\n") + "\n" + strings.TrimLeft(text[i+j+len(noteEnd):], "\n")
		}
	}
	text = strings.TrimSpace(text)
	if len(hosts) > 0 {
		names := make([]string, 0, len(hosts))
		for n := range hosts {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString(noteBegin + "\n## Remote machines (rterm)\n\n")
		b.WriteString("You can run commands on these machines with the `rterm` CLI, in persistent shells the user watches live. Run `rterm guide` once for the full manual (exit codes, long-running commands, prompts, keys).\n\n")
		b.WriteString(machinesText(hosts))
		b.WriteString("\nQuick reference: `rterm NAME run \"<cmd>\"` · `rterm NAME read` · `rterm NAME keys C-c` · `rterm NAME wait \"<regex>\"` · `rterm NAME:server run \"…\"` for a second terminal. Ask the user before destructive commands.\n")
		b.WriteString(noteEnd + "\n")
		if text != "" {
			text += "\n\n"
		}
		text += b.String()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimLeft(text, "\n")), 0o644)
}

// machinesText lists paired machines with their OS and shell, one per line.
func machinesText(hosts map[string]Host) string {
	names := make([]string, 0, len(hosts))
	for n := range hosts {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		h := hosts[n]
		shell := "bash"
		if h.OS == "windows" {
			shell = "PowerShell, use PowerShell syntax"
		}
		fmt.Fprintf(&b, "- `%s`: %s (%s). Example: `rterm %s run \"%s\"`\n", n, osLabel(h.OS), shell, n, helloCmd(h.OS))
	}
	return b.String()
}

// Guide prints the manual for AI agents, plus the machines paired here.
func Guide() int {
	fmt.Print(docs.Guide)
	if hosts, err := loadHosts(); err == nil && len(hosts) > 0 {
		fmt.Print("\n## Machines paired on this computer\n\n" + machinesText(hosts))
	} else {
		fmt.Print("\n## Machines paired on this computer\n\nNone yet. The user pairs one with `rterm host init` there, then `rterm add` here.\n")
	}
	return 0
}

// InstallSkill installs (or refreshes) the Claude Code skill.
func InstallSkill() int {
	hosts, _ := loadHosts()
	path, err := docs.InstallSkill(machinesText(hosts))
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ installing the Claude Code skill:", err)
		return 1
	}
	fmt.Println("✓ Claude Code skill installed at", path)
	return 0
}

func osLabel(goos string) string {
	switch goos {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	}
	return "unknown OS"
}

// ---------------------------------------------------------------- flags

type flagSet map[string]string

// takeFlags pulls --name value / --name=value / --switch flags out of args.
// With leadingOnly, parsing stops at the first positional argument, so a
// command like `run ls --color` keeps its own flags.
func takeFlags(args []string, leadingOnly ...bool) (flagSet, []string) {
	f := flagSet{}
	boolFlags := map[string]bool{"readonly": true, "local": true, "help": true}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if len(leadingOnly) > 0 && leadingOnly[0] && !strings.HasPrefix(a, "--") {
			rest = append(rest, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "--") && len(a) > 2 {
			k := a[2:]
			if eq := strings.IndexByte(k, '='); eq >= 0 {
				f[k[:eq]] = k[eq+1:]
				continue
			}
			if boolFlags[k] {
				f[k] = "true"
				continue
			}
			if i+1 < len(args) {
				f[k] = args[i+1]
				i++
				continue
			}
		}
		rest = append(rest, a)
	}
	return f, rest
}

func (f flagSet) str(k, def string) string {
	if v, ok := f[k]; ok {
		return v
	}
	return def
}

func (f flagSet) int(k string, def int) int {
	if v, err := strconv.Atoi(f[k]); err == nil {
		return v
	}
	return def
}

func (f flagSet) seconds(k string, def int) int {
	if d, err := time.ParseDuration(f[k]); err == nil {
		return int(d.Seconds())
	}
	return f.int(k, def)
}

func (f flagSet) bool(k string) bool { return f[k] == "true" }
