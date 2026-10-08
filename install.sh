#!/bin/sh
# Install rterm on Linux or macOS:
#   curl -fsSL https://raw.githubusercontent.com/ninadkale98/remote-terminal/main/install.sh | sh
# Options (environment variables):
#   RTERM_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   RTERM_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
set -eu

repo="ninadkale98/remote-terminal"
dir="${RTERM_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "rterm: unsupported OS $(uname -s). On Windows use install.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "rterm: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

if [ -n "${RTERM_DOWNLOAD_BASE:-}" ]; then
  base="$RTERM_DOWNLOAD_BASE"           # a mirror or a local folder (file://…), for testing
elif [ -n "${RTERM_VERSION:-}" ]; then
  base="https://github.com/$repo/releases/download/$RTERM_VERSION"
else
  base="https://github.com/$repo/releases/latest/download"
fi

asset="rterm-$os-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset …"
curl -fsSL -o "$tmp/rterm" "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"

want="$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/rterm" | cut -d' ' -f1)"
else
  got="$(shasum -a 256 "$tmp/rterm" | cut -d' ' -f1)"
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "rterm: checksum mismatch for $asset; not installing" >&2
  exit 1
fi

mkdir -p "$dir"
chmod +x "$tmp/rterm"
mv "$tmp/rterm" "$dir/rterm"
echo "✓ installed $("$dir/rterm" version) to $dir/rterm"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "  $dir is not on your PATH. Add this to your shell profile:"
     echo "    export PATH=\"$dir:\$PATH\"" ;;
esac
