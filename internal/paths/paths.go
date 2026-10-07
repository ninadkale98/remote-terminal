// Package paths locates rterm's files under ~/.rterm on every OS.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Dir is ~/.rterm (or %USERPROFILE%\.rterm on Windows).
func Dir() string {
	if d := os.Getenv("RTERM_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".rterm")
}

// AgentFile holds the running agent's address and token.
func AgentFile() string { return filepath.Join(Dir(), "agent.json") }

// AgentLog is where the agent writes its own diagnostics.
func AgentLog() string { return filepath.Join(Dir(), "agent.log") }

// StateDir holds per-session completion state written by the shell hook.
func StateDir() string { return filepath.Join(Dir(), "state") }

// LogDir holds per-session audit logs.
func LogDir() string { return filepath.Join(Dir(), "log") }

// TmpDir holds multi-line command scripts.
func TmpDir() string { return filepath.Join(Dir(), "tmp") }

// BinDir is where `host init` installs the binary.
func BinDir() string { return filepath.Join(Dir(), "bin") }

// HostsFile is the client's list of paired machines.
func HostsFile() string { return filepath.Join(Dir(), "hosts.json") }

// ExeName is the binary's file name on this OS.
func ExeName() string {
	if runtime.GOOS == "windows" {
		return "rterm.exe"
	}
	return "rterm"
}

// Ensure creates the rterm directories.
func Ensure() error {
	for _, d := range []string{Dir(), StateDir(), LogDir(), TmpDir(), BinDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
