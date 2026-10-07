package agent

import (
	"encoding/base64"
	"unicode/utf16"
)

// The shell hooks run before every prompt. They write "<count> <exit code>"
// to the session's state file (which is how the agent knows a command has
// finished) and print an invisible OSC 133;D marker that lets the agent cut
// the output exactly where the prompt starts.

const bashHook = `# rterm session hook (generated; safe to delete)
[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc
[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"
__rterm_n=0
__rterm_hook() {
  local c=$?
  __rterm_n=$((__rterm_n+1))
  printf '%s %s\n' "$__rterm_n" "$c" > "$RTERM_STATE.tmp" && mv -f "$RTERM_STATE.tmp" "$RTERM_STATE"
  printf '\033]133;D;%s\007' "$c"
  return $c
}
case "$PROMPT_COMMAND" in
  *__rterm_hook*) ;;
  *) PROMPT_COMMAND="__rterm_hook${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
esac
`

const psHook = `$global:__rterm_n = 0
function global:prompt {
  $ok = $?
  $code = 0
  if (-not $ok) { if ($global:LASTEXITCODE) { $code = $global:LASTEXITCODE } else { $code = 1 } }
  $global:LASTEXITCODE = 0
  $global:__rterm_n++
  try { [IO.File]::WriteAllText($env:RTERM_STATE, "$($global:__rterm_n) $code") } catch {}
  [Console]::Write("$([char]27)]133;D;$code$([char]7)")
  "PS $($executionContext.SessionState.Path.CurrentLocation)> "
}
`

// encodePowerShell encodes a script for powershell.exe -EncodedCommand
// (base64 of UTF-16LE). Encoded commands are not subject to execution policy.
func encodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
