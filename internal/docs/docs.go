// Package docs embeds rterm's manual for AI agents, printed by `rterm guide`
// and installed as a Claude Code skill by `rterm add`.
package docs

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed guide.md
var Guide string

// skillHeader is the frontmatter Claude Code reads to decide when the skill applies.
const skillHeader = `---
name: rterm
description: Run commands in a persistent, shared terminal on another machine (Windows, macOS or Linux) with the rterm CLI. Use whenever the user asks you to do something on a remote or second machine, or mentions rterm or a paired machine name.
---

`

// InstallSkill writes the guide as ~/.claude/skills/rterm/SKILL.md, with the
// list of paired machines appended, and returns the path.
func InstallSkill(machines string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".claude", "skills", "rterm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	body := skillHeader + Guide
	if strings.TrimSpace(machines) != "" {
		body += "\n## Machines paired on this computer\n\n" + machines + "\nRun `rterm ls` for the current list.\n"
	}
	path := filepath.Join(dir, "SKILL.md")
	return path, os.WriteFile(path, []byte(body), 0o644)
}
