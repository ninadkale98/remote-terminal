# Install rterm on Windows (PowerShell 5.1 or 7):
#   irm https://raw.githubusercontent.com/ninadkale98/remote-terminal/main/install.ps1 | iex
# Options (environment variables):
#   RTERM_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   RTERM_INSTALL_DIR  where to put rterm.exe (default: %LOCALAPPDATA%\rterm\bin)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is very slow with the progress bar
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = 'ninadkale98/remote-terminal'
$dir = if ($env:RTERM_INSTALL_DIR) { $env:RTERM_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'rterm\bin' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "rterm: unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
}
$base = if ($env:RTERM_VERSION) { "https://github.com/$repo/releases/download/$($env:RTERM_VERSION)" }
        else { "https://github.com/$repo/releases/latest/download" }
$asset = "rterm-windows-$arch.exe"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("rterm-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading $asset ..."
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp 'rterm.exe')
  Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')

  $line = Get-Content (Join-Path $tmp 'SHA256SUMS') | Where-Object { $_ -match " $([regex]::Escape($asset))$" } | Select-Object -First 1
  if (-not $line) { throw "rterm: $asset is not listed in SHA256SUMS; not installing" }
  $want = ($line -split '\s+')[0].ToLower()
  $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp 'rterm.exe')).Hash.ToLower()
  if ($want -ne $got) { throw "rterm: checksum mismatch for $asset; not installing" }

  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  Move-Item -Force (Join-Path $tmp 'rterm.exe') (Join-Path $dir 'rterm.exe')
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $dir)) {
  [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';') + ';' + $dir).TrimStart(';')), 'User')
  Write-Host "  added $dir to your PATH (open a new terminal to use it)"
}
$env:Path = "$dir;$env:Path"
Write-Host "✓ installed $(& (Join-Path $dir 'rterm.exe') version) to $dir\rterm.exe"
