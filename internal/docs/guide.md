# rterm — guide for AI agents

rterm gives you a persistent, shared terminal on another machine. You call it
from your normal shell tool; the command is typed into a long-lived shell on
the remote machine, and you get its output and exit code back. The user
watches the same shell live and may type into it too.

## Mental model

- Each machine has named sessions. The default session is `agent`; address
  others as `NAME:SESSION` (e.g. `m2:server`).
- A session is ONE persistent shell. State carries over between calls:
  current directory, environment variables, activated virtualenvs,
  background jobs, history. `cd` in one call still applies in the next.
- Windows machines run PowerShell (use PowerShell syntax). macOS and Linux
  machines run bash.
- Only one `run` at a time per session. Use a second session for parallel
  work (a dev server in `NAME:server`, tests in `NAME`).
- The user can see everything you type. Work as if someone is watching.

## Commands

    rterm ls                                   list paired machines and their OS
    rterm NAME run "command"                   run, wait, print output, exit with its code
    rterm NAME run --timeout 600 "command"     wait up to 600 s (default 120)
    rterm NAME read [--lines 60]               show the current screen
    rterm NAME keys C-c                        send keystrokes (see Keys)
    rterm NAME wait [--timeout 60] "regex"     wait until the latest command prints a match
    rterm NAME status                          sessions, shells, busy/idle
    rterm NAME kill                            restart this session's shell (loses its state)
    rterm doctor NAME                          diagnose the connection

Quote the whole command as one argument. Multi-line commands are fine; they
are saved to a script on the remote machine and run in the same shell.

## Results and exit codes

`run` prints the command's output on stdout and a status line on stderr,
e.g. `[rterm: exit 0, 2.3s]`. Its own exit code is:

| code | meaning |
| --- | --- |
| the command's code | the command finished (codes outside 0–255 become 1; the real code is in the status line) |
| 124 | still running at the timeout (`run`), or pattern not seen (`wait`) |
| 125 | the session is busy with an unfinished command |
| 255 | could not reach the machine (network, SSH, rterm missing) |
| 2 | bad usage |

Output longer than about 400 lines is trimmed to the first 100 and last 300.

## Patterns

Long-running commands and servers:

    rterm m2:server run --timeout 5 "npm run dev"    # returns 124: still running, that's expected
    rterm m2:server wait --timeout 90 "ready|listening|Local:"
    rterm m2 run "curl -s localhost:3000/health"    # Linux; on Windows: Invoke-WebRequest

A command still running from an earlier `run` makes the next `run` in that
session return 125. Then either wait for it (`wait`), look at it (`read`),
or stop it (`keys C-c`).

Interactive prompts:

    rterm m2 run --timeout 10 "npm init"     # returns 124 at the first question
    rterm m2 read                            # see the question
    rterm m2 keys "my-app" Enter             # answer it

Stuck or confusing state: `rterm NAME read` first. `keys C-c` interrupts.
`kill` is the last resort; it discards the session's directory, variables
and jobs.

## Keys

Names: `Enter Tab Esc BSpace Space Up Down Left Right Home End PgUp PgDn
Delete`, and `C-a` … `C-z` for Ctrl combinations (`C-c` interrupt,
`C-d` end of input). Any other argument is typed literally. Keys are sent
in order: `rterm m2 keys "y" Enter`.

## Windows (PowerShell) notes

- Use PowerShell syntax: `Get-ChildItem`, `$env:PATH`, `;` to chain
  (in PowerShell 7 `&&` also works).
- Exit codes: native programs report their real code; failed PowerShell
  cmdlets report 1.
- Paths use backslashes; quote paths with spaces.

## Etiquette

- Ask the user before destructive commands (deleting files, force pushes,
  resetting databases) or anything needing administrator rights.
- Don't print secrets; everything is visible on the user's screen and kept
  in the session log on the remote machine.
- Prefer one clear command per `run` so the user can follow along.
