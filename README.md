# rterm

A shared, watchable terminal on another machine, for Claude (or any agent) and you at the same time.

Claude runs on machine 1 and calls `rterm m2 run "…"` from its normal Bash tool. The command is typed into a real shell on machine 2, and Claude gets the output and exit code back as if it ran locally. You watch the same shell live with `rterm watch m2`, and you can type into it too.

- One binary for macOS, Linux and Windows, used on both machines.
- Runs over plain SSH, so any network works: LAN, office VPN, Tailscale.
- Not an MCP server, and nothing new listens on the network. Machine 2 only needs its SSH server.
- On Windows, sessions are real PowerShell through ConPTY. No WSL or tmux needed.

## Install

Download the binary for each machine from the [latest release](https://github.com/OWNER/REPO/releases/latest).

**Linux / macOS:**

```sh
curl -fL -o rterm https://github.com/OWNER/REPO/releases/latest/download/rterm-linux-amd64   # or -linux-arm64, -darwin-arm64, -darwin-amd64
chmod +x rterm && mkdir -p ~/.local/bin && mv rterm ~/.local/bin/
```

**Windows (PowerShell):**

```powershell
Invoke-WebRequest -Uri https://github.com/OWNER/REPO/releases/latest/download/rterm-windows-amd64.exe -OutFile $env:USERPROFILE\Downloads\rterm.exe
```

## Set up: machine 2 first, then machine 1

**1. On machine 2** (the one Claude will control). On Windows, use an Administrator PowerShell so it can turn on the OpenSSH server:

```powershell
& $env:USERPROFILE\Downloads\rterm.exe host init
```

This installs rterm to `~/.rterm/bin` and turns on the SSH server (installing it on Windows if needed). It then starts the session agent, checks that the shell works, and prints a pairing line like:

```
rterm add mypc 'CORP\ninad@10.20.4.15' --remote-bin 'C:\Users\ninad\.rterm\bin\rterm.exe'
```

**2. On machine 1** (where Claude runs), paste that line. Enter machine 2's password once. rterm then sets up a key, an `~/.ssh/config` entry with connection reuse, and a note in `~/.claude/CLAUDE.md` so Claude knows how to use it.

**3. Watch** in a terminal split while Claude works:

```sh
rterm watch mypc            # Ctrl-] detaches; --readonly to watch without typing
```

## Commands (machine 1)

| Command | What it does |
| --- | --- |
| `rterm NAME run [--timeout 120] "cmd"` | Types the command into the shared session, waits, prints output, exits with its exit code. If still running at the timeout, returns 124 and the command keeps going. |
| `rterm NAME read [--lines 60]` | Shows the current screen. |
| `rterm NAME keys C-c` · `keys y Enter` · `keys Up` | Sends keystrokes. |
| `rterm NAME wait [--timeout 60] "regex"` | Waits for output from the latest command to match. |
| `rterm NAME:server run "npm run dev"` | Uses a second named terminal. |
| `rterm NAME status` · `kill` | Lists sessions · restarts this session's shell. |
| `rterm watch NAME[:SESSION]` | Watches live. |
| `rterm ls` · `doctor NAME` · `remove NAME` | Lists machines · checks the link · unpairs. |

On machine 2: `rterm host status`, `rterm host stop` / `start`, and `rterm host attach` to watch a session locally.

## Teaching Claude to use it

The manual for AI agents is built into the binary, so it always matches the installed version:

- `rterm guide` prints it, with the machines paired on this computer: persistent sessions, exit codes, long-running commands, prompts, keys, and PowerShell notes.
- `rterm add` installs it as a Claude Code skill at `~/.claude/skills/rterm/SKILL.md`, so Claude loads it whenever you ask for work on another machine. Run `rterm skill` to reinstall it.
- `rterm add` also adds a short section to `~/.claude/CLAUDE.md` naming each machine and its shell, and pointing to `rterm guide`.
- `rterm --help` and `rterm NAME --help` give the short versions.

## How it works

Each client command is one SSH call that runs `rterm host rpc` on machine 2 with a JSON request on stdin. That talks to a small agent on machine 2 (localhost only, token-protected) which owns the shells: a PTY on macOS/Linux, ConPTY on Windows. A hook in the shell's prompt records each command's exit code, so the agent knows exactly when a command has finished. Every request is logged to `~/.rterm/log/<session>.jsonl` on machine 2.

## Notes for v0.1

- The Windows host side compiles and its output parsing is unit-tested, but this release has not been run on a real Windows machine yet. If something fails, `rterm doctor NAME` on machine 1 and `~/.rterm/agent.log` on machine 2 show what happened.
- Windows sessions use PowerShell (pwsh if installed); macOS/Linux sessions use bash.
- One `run` at a time per session; use `NAME:other` for parallel work.
- Unsigned binaries: corporate antivirus may need to allow `rterm.exe`.
- If you're an administrator on Windows and `rterm add` still asks for a password, run the `rterm host authorize --key "…"` line it prints in an Administrator PowerShell on machine 2.

## Build

```sh
go build -o rterm ./cmd/rterm   # Go 1.24+; golang.org/x/sys and x/term are vendored in third_party/
```
