//go:build !windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/creack/pty"

	"github.com/ninadkale98/remote-terminal/internal/paths"
)

type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *unixPTY) Pid() int                    { return p.cmd.Process.Pid }
func (p *unixPTY) Wait() error                 { return p.cmd.Wait() }
func (p *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
func (p *unixPTY) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.f.Close()
}

// shellArgv picks bash and installs the completion hook as its rc file.
func shellArgv() ([]string, string, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return nil, "", fmt.Errorf("bash not found; rterm needs bash on this machine")
	}
	hook := filepath.Join(paths.Dir(), "hook.bash")
	if err := os.WriteFile(hook, []byte(bashHook), 0o600); err != nil {
		return nil, "", err
	}
	return []string{bash, "--rcfile", hook, "-i"}, "bash", nil
}

func sessionEnv(name, state string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "RTERM_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color", "RTERM_STATE="+state, "RTERM_SESSION="+name)
}

func startPTY(argv, env []string, dir string, cols, rows int) (PTY, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

// scriptFile returns the file name and the one-line command that runs a
// multi-line command saved to that file.
func scriptFile(dir, base string) (string, func(path string) string) {
	return filepath.Join(dir, base+".sh"), func(path string) string {
		return "source '" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
}
