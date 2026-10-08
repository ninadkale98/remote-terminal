// Command rterm gives Claude (or you) a shared, watchable terminal on another
// machine over plain SSH.
package main

import (
	"fmt"
	"os"

	"github.com/ninadkale98/remote-terminal/internal/client"
	"github.com/ninadkale98/remote-terminal/internal/host"
	"github.com/ninadkale98/remote-terminal/internal/proto"
)

const usage = `rterm %s — a persistent, shared terminal on another machine, over SSH

AI agents: run 'rterm guide' for the full manual (exit codes, long-running
commands, prompts, keys, Windows notes) and the list of paired machines.

On the machine Claude controls (machine 2):
  rterm host init                     set it up and print the pairing line

On the machine where Claude runs (machine 1):
  rterm add NAME USER@ADDRESS --remote-bin PATH    pair (paste the line from host init)
  rterm watch NAME[:SESSION] [--readonly]          watch the session live (Ctrl-] detaches)
  rterm NAME run [--timeout 120] "command"         run a command, get output + exit code
  rterm NAME read [--lines 60]                     show the current screen
  rterm NAME keys C-c | Enter | Up | "text" …      send keystrokes
  rterm NAME wait [--timeout 60] "regex"           wait for output to appear
  rterm NAME status | kill                         list sessions | restart this session
  rterm ls | doctor NAME | remove NAME             list | diagnose | unpair machines
  rterm guide                                      manual for AI agents
  rterm skill                                      (re)install the Claude Code skill
  rterm version

NAME:SESSION addresses a second terminal, e.g. rterm m2:server run "npm run dev".
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Printf(usage, proto.Version)
		os.Exit(0)
	}
	var code int
	switch args[0] {
	case "host":
		code = host.Main(args[1:])
	case "add":
		code = client.Add(args[1:])
	case "watch":
		code = client.Watch(args[1:])
	case "ls", "list":
		code = client.List()
	case "remove", "rm":
		code = client.Remove(args[1:])
	case "doctor":
		code = client.Doctor(args[1:])
	case "guide", "docs", "manual":
		code = client.Guide()
	case "skill":
		code = client.InstallSkill()
	case "version", "--version":
		fmt.Println("rterm", proto.Version)
	default:
		code = client.Session(args[0], args[1:])
	}
	os.Exit(code)
}
