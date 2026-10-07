package agent

import (
	"strings"
	"testing"
)

func TestRenderConPTYStyle(t *testing.T) {
	// ConPTY compresses runs of spaces with cursor-forward and clears with
	// erase-characters; PSReadLine redraws the input line with colors.
	raw := "\x1b[?25l\x1b[93mGet-Date\x1b[0m\x1b[?25h\r\n" +
		"\x1b[?25l\r\nTuesday,\x1b[1CJune\x1b[1C10\x1b[K\r\n\r\n" +
		"\x1b]133;D;0\x07PS C:\\Users\\ninad> \x1b[K"
	lines, partial := Render([]byte(raw))
	got := strings.Join(lines, "|")
	if want := "Get-Date||Tuesday, June 10||PS C:\\Users\\ninad>"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if !partial {
		t.Fatal("prompt line should be partial")
	}
}

func TestCleanRunDropsEchoAndPrompt(t *testing.T) {
	raw := "npm run build\r\n> app@1.0 build\r\nok\r\n"
	out, _ := cleanRun([]byte(raw), "npm run build", true)
	if out != "> app@1.0 build\nok" {
		t.Fatalf("got %q", out)
	}
	// Without a marker the trailing prompt is dropped.
	raw2 := "dir\r\nfile.txt\r\nPS C:\\> "
	out2, _ := cleanRun([]byte(raw2), "dir", false)
	if out2 != "file.txt" {
		t.Fatalf("got %q", out2)
	}
}

func TestCarriageReturnProgress(t *testing.T) {
	raw := "downloading 10%\rdownloading 55%\rdownloading 100%\r\ndone\r\n"
	lines, _ := Render([]byte(raw))
	if strings.Join(lines, "|") != "downloading 100%|done" {
		t.Fatalf("got %q", lines)
	}
}

func TestKeyBytes(t *testing.T) {
	got := KeyBytes([]string{"C-c", "y", "Enter", "Up", "c-d"})
	want := "\x03y\r\x1b[A\x04"
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPowerShellEncoding(t *testing.T) {
	// "ab" in UTF-16LE is 61 00 62 00 → base64 "YQBiAA=="
	if got := encodePowerShell("ab"); got != "YQBiAA==" {
		t.Fatalf("got %q", got)
	}
}
