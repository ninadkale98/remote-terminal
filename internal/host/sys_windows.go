//go:build windows

package host

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/ninadkale98/remote-terminal/internal/paths"
)

func startDetached(exe string, args ...string) error {
	logf, err := os.OpenFile(paths.AgentLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	base := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	// Try to leave the SSH session's job object so the agent outlives the
	// connection; fall back if the job doesn't allow breakaway.
	for _, flags := range []uint32{base | windows.CREATE_BREAKAWAY_FROM_JOB, base} {
		cmd := exec.Command(exe, args...)
		cmd.Stdout = logf
		cmd.Stderr = logf
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		if err = cmd.Start(); err == nil {
			return cmd.Process.Release()
		}
	}
	return err
}

func enableVT() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}

func loginName() string {
	u, d, c := os.Getenv("USERNAME"), os.Getenv("USERDOMAIN"), os.Getenv("COMPUTERNAME")
	if d != "" && !strings.EqualFold(d, c) {
		return d + `\` + u
	}
	return u
}

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func powershell(script string) ([]byte, error) {
	return exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script).CombinedOutput()
}

// installKey adds the key to the user's authorized_keys and, when this
// account can write it, to administrators_authorized_keys — which is the
// file Windows' sshd actually reads for members of Administrators.
func installKey(key string) ([]string, error) {
	var where []string
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	userFile := filepath.Join(home, ".ssh", "authorized_keys")
	if err := appendKey(userFile, key, 0o600); err != nil {
		return nil, err
	}
	where = append(where, userFile)

	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	adminFile := filepath.Join(pd, "ssh", "administrators_authorized_keys")
	if err := appendKey(adminFile, key, 0o600); err == nil {
		// sshd ignores this file unless only Administrators and SYSTEM can access it.
		out, aerr := exec.Command("icacls.exe", adminFile, "/inheritance:r",
			"/grant", "*S-1-5-32-544:F", "/grant", "*S-1-5-18:F").CombinedOutput()
		if aerr != nil {
			return where, fmt.Errorf("fixing permissions on %s: %v: %s", adminFile, aerr, out)
		}
		where = append(where, adminFile)
	}
	return where, nil
}

func osInit(installed string) bool {
	admin := isElevated()
	binDir := filepath.Dir(installed)

	// PATH (user scope), so 'rterm' works by name in new terminals.
	ps := `$d='` + strings.ReplaceAll(binDir, "'", "''") + `'; $p=[Environment]::GetEnvironmentVariable('Path','User'); ` +
		`if (-not (($p -split ';') -contains $d)) { [Environment]::SetEnvironmentVariable('Path', (($p.TrimEnd(';') + ';' + $d).TrimStart(';')), 'User') }`
	if out, err := powershell(ps); err != nil {
		fmt.Printf("✗ adding %s to PATH: %v %s\n", binDir, err, out)
	} else {
		fmt.Println("✓ added", binDir, "to your PATH (new terminals)")
	}

	// Autostart the agent at logon (no admin needed).
	runVal := `"` + installed + `" host start`
	if out, err := exec.Command("reg.exe", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", "rterm-agent", "/t", "REG_SZ", "/d", runVal, "/f").CombinedOutput(); err != nil {
		fmt.Printf("✗ registering autostart: %v %s\n", err, out)
	} else {
		fmt.Println("✓ agent will start when you log in")
	}

	// OpenSSH Server.
	q, _ := exec.Command("sc.exe", "query", "sshd").CombinedOutput()
	installedSSH := strings.Contains(string(q), "SERVICE_NAME")
	running := strings.Contains(string(q), "RUNNING")
	const fw = `if (-not (Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue)) { ` +
		`New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH Server (sshd)' -Enabled True ` +
		`-Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 | Out-Null }`
	const enable = `Set-Service -Name sshd -StartupType Automatic; Start-Service sshd; `
	const install = `Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0 | Out-Null; `

	switch {
	case installedSSH && running:
		fmt.Println("✓ OpenSSH Server is running")
		if admin {
			_, _ = powershell(fw)
		}
	case !admin:
		fmt.Println("✗ OpenSSH Server is not set up, and this window isn't elevated.")
		fmt.Println("  Re-run 'rterm host init' in an Administrator PowerShell, or run these there:")
		if !installedSSH {
			fmt.Println("    " + install)
		}
		fmt.Println("    " + enable)
		fmt.Println("    " + fw)
		return false
	default:
		script := enable + fw
		if !installedSSH {
			fmt.Println("… installing OpenSSH Server (this can take a few minutes)")
			script = install + script
		}
		if out, err := powershell(script); err != nil {
			fmt.Printf("✗ setting up OpenSSH Server failed: %v\n%s\n", err, out)
			fmt.Println("  If Windows Update is blocked on this machine, install OpenSSH from")
			fmt.Println("  https://github.com/PowerShell/Win32-OpenSSH/releases (the .msi) and re-run init.")
			return false
		}
		fmt.Println("✓ OpenSSH Server installed and running (starts automatically)")
	}
	if admin {
		fmt.Println("  note: this window is elevated, so the session started now runs as administrator.")
		fmt.Println("  For a normal-rights session later: rterm host stop, then rterm host start in a normal window.")
	}
	return true
}
