//go:build !windows

package host

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/rterm/rterm/internal/paths"
)

func startDetached(exe string, args ...string) error {
	logf, err := os.OpenFile(paths.AgentLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func enableVT() {}

func loginName() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

func installKey(key string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".ssh")
	path := filepath.Join(dir, "authorized_keys")
	if err := appendKey(path, key, 0o600); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Chmod(path, 0o600)
	return []string{path}, nil
}

func osInit(installed string) bool {
	ok := sshListening()
	if ok {
		fmt.Println("✓ SSH server is listening on port 22")
	} else if runtime.GOOS == "darwin" {
		fmt.Println("✗ SSH server is off. Turn on System Settings › General › Sharing › Remote Login,")
		fmt.Println("  or run: sudo systemsetup -setremotelogin on")
	} else {
		fmt.Println("✗ No SSH server on port 22. Install and start one, e.g.:")
		fmt.Println("  sudo apt install openssh-server   (Debian/Ubuntu)")
		fmt.Println("  sudo dnf install openssh-server && sudo systemctl enable --now sshd   (Fedora/RHEL)")
	}
	if !strings.Contains(os.Getenv("PATH"), paths.BinDir()) {
		fmt.Printf("  (optional) add %s to your PATH to run rterm by name\n", paths.BinDir())
	}
	fmt.Println("✓ the agent starts on demand over SSH; no autostart needed")
	return ok
}
