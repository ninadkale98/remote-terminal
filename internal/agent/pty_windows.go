//go:build windows

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procUpdateProcThreadAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")

// conPTY runs a process under a Windows pseudo console (ConPTY).
type conPTY struct {
	hpc       windows.Handle
	in        *os.File // we write keystrokes here
	out       *os.File // we read rendered output here
	proc      windows.Handle
	pid       int
	closeOnce sync.Once
}

func (p *conPTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *conPTY) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *conPTY) Pid() int                    { return p.pid }

func (p *conPTY) Resize(cols, rows int) error {
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (p *conPTY) Wait() error {
	_, err := windows.WaitForSingleObject(p.proc, windows.INFINITE)
	return err
}

func (p *conPTY) Close() error {
	p.closeOnce.Do(func() {
		_ = windows.TerminateProcess(p.proc, 1)
		// Closing the pseudo console ends the output stream, so the reader sees EOF.
		windows.ClosePseudoConsole(p.hpc)
		_ = p.in.Close()
		_ = windows.CloseHandle(p.proc)
	})
	return nil
}

// shellArgv picks PowerShell 7 if installed, else Windows PowerShell, and
// installs the completion hook through -EncodedCommand.
func shellArgv() ([]string, string, error) {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return []string{p, "-NoLogo", "-NoExit", "-EncodedCommand", encodePowerShell(psHook)},
				strings.TrimSuffix(name, ".exe"), nil
		}
	}
	return nil, "", errors.New("PowerShell not found on this machine")
}

func sessionEnv(name, state string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "RTERM_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "RTERM_STATE="+state, "RTERM_SESSION="+name)
}

func envBlock(env []string) *uint16 {
	var b []uint16
	for _, kv := range env {
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

func startPTY(argv, env []string, dir string, cols, rows int) (PTY, error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, fmt.Errorf("create output pipe: %w", err)
	}
	var hpc windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc)
	// The pseudo console keeps its own copies of these two ends.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, fmt.Errorf("create pseudo console (needs Windows 10 1809 or later): %w", err)
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// The pseudo console attribute takes the HPCON value itself (not a
	// pointer to it), so call the API directly rather than through
	// attrs.Update, which expects a real Go pointer.
	if r1, _, e1 := procUpdateProcThreadAttribute.Call(uintptr(unsafe.Pointer(attrs.List())), 0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(hpc), unsafe.Sizeof(hpc), 0, 0); r1 == 0 {
		return nil, fmt.Errorf("attach pseudo console: %w", e1)
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// Without this, a child of a process whose own stdio is redirected can
	// inherit those handles instead of the pseudo console's.
	si.Flags |= windows.STARTF_USESTDHANDLES

	app, err := windows.UTF16PtrFromString(argv[0])
	if err != nil {
		return nil, err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return nil, err
	}
	dir16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(app, cmdLine, nil, nil, false, flags, envBlock(env), dir16, &si.StartupInfo, &pi); err != nil {
		windows.ClosePseudoConsole(hpc)
		windows.CloseHandle(inW)
		windows.CloseHandle(outR)
		return nil, fmt.Errorf("start %s: %w", filepath.Base(argv[0]), err)
	}
	windows.CloseHandle(pi.Thread)
	return &conPTY{
		hpc:  hpc,
		in:   os.NewFile(uintptr(inW), "conpty-in"),
		out:  os.NewFile(uintptr(outR), "conpty-out"),
		proc: pi.Process,
		pid:  int(pi.ProcessId),
	}, nil
}

// scriptFile returns the file name and the one-line command that runs a
// multi-line command saved to that file. Invoke-Expression is used because
// running a .ps1 file directly is blocked by the default execution policy.
func scriptFile(dir, base string) (string, func(path string) string) {
	return filepath.Join(dir, base+".ps1"), func(path string) string {
		return "Invoke-Expression ([IO.File]::ReadAllText('" + strings.ReplaceAll(path, "'", "''") + "'))"
	}
}
