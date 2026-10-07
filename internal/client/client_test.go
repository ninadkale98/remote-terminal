package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteCmd(t *testing.T) {
	h := Host{RemoteBin: `C:\Users\ninad\.rterm\bin\rterm.exe`}
	if got := h.remoteCmd("host", "rpc"); got != `C:\Users\ninad\.rterm\bin\rterm.exe host rpc` {
		t.Fatalf("got %q", got)
	}
	h.RemoteBin = `C:\Users\Ninad K\.rterm\bin\rterm.exe`
	if got := h.remoteCmd("host", "rpc"); got != `"C:\Users\Ninad K\.rterm\bin\rterm.exe" host rpc` {
		t.Fatalf("got %q", got)
	}
}

func TestSSHBlockIsReplacedAndKeepsUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".ssh", "config")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o700)
	_ = os.WriteFile(cfg, []byte("Host work\n  User me\n"), 0o600)

	if err := writeSSHBlock("m2", "Host m2\n  HostName 1.2.3.4\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeSSHBlock("m2", "Host m2\n  HostName 5.6.7.8\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	s := string(b)
	if strings.Count(s, "Host m2") != 1 || !strings.Contains(s, "5.6.7.8") || strings.Contains(s, "1.2.3.4") {
		t.Fatalf("bad config:\n%s", s)
	}
	if !strings.HasPrefix(s, "# >>> rterm m2 >>>") || !strings.Contains(s, "Host work\n  User me") {
		t.Fatalf("rterm block should be first and user config kept:\n%s", s)
	}
	_ = writeSSHBlock("m2", "")
	b, _ = os.ReadFile(cfg)
	if strings.Contains(string(b), "m2") {
		t.Fatalf("block not removed:\n%s", b)
	}
}

func TestTakeFlags(t *testing.T) {
	f, rest := takeFlags([]string{"--timeout", "30", "ls", "--color"}, true)
	if f.int("timeout", 0) != 30 || strings.Join(rest, " ") != "ls --color" {
		t.Fatalf("got %v %v", f, rest)
	}
	f, rest = takeFlags([]string{"t:server", "--readonly"})
	if !f.bool("readonly") || rest[0] != "t:server" {
		t.Fatalf("got %v %v", f, rest)
	}
}
