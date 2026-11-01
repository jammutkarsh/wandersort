#!/bin/sh
# wandersort installer for macOS and Linux.
#   curl -fsSL https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.sh | sh
# Downloads the bare binary from the GitHub release and refuses to install it
# unless it matches the release's checksums.txt.
# Env overrides:
#   VERSION=v0.1.0   install a specific tag (default: latest release)
#   BINDIR=~/bin     install location (default: /usr/local/bin, fallback ~/.local/bin)
set -eu

REPO="jammutkarsh/wandersort"
BIN="wandersort"

err() { echo "install: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- detect os/arch -----------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) err "unsupported OS '$os' (use scripts/install.ps1 on Windows)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) err "unsupported architecture '$arch'" ;;
esac

if have curl; then dl() { curl -fsSL "$1"; }; dlo() { curl -fsSL "$1" -o "$2"; }
elif have wget; then dl() { wget -qO- "$1"; }; dlo() { wget -qO "$2" "$1"; }
else err "need curl or wget"; fi

if have sha256sum; then sha256() { sha256sum "$1" | awk '{print $1}'; }
elif have shasum; then sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else err "need sha256sum or shasum to verify the download"; fi

# --- resolve version ----------------------------------------------------------
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION=$(dl "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d'"' -f4)
  [ -n "$VERSION" ] || err "could not resolve latest version"
fi
num="${VERSION#v}" # strip leading v for asset names

asset="${BIN}_${num}_${os}_${arch}"
base="https://github.com/$REPO/releases/download/$VERSION"

# --- download + verify checksum -----------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $asset ($VERSION)..."
dlo "$base/$asset" "$tmp/$BIN" || err "download failed: $base/$asset"
dlo "$base/checksums.txt" "$tmp/checksums.txt" || err "download failed: $base/checksums.txt"

want=$(awk -v a="$asset" '$2 == a {print $1}' "$tmp/checksums.txt")
[ -n "$want" ] || err "$asset is not listed in checksums.txt"
[ "$(sha256 "$tmp/$BIN")" = "$want" ] || err "checksum mismatch for $asset"
chmod +x "$tmp/$BIN"

# --- install ------------------------------------------------------------------
BINDIR="${BINDIR:-/usr/local/bin}"
install_to() {
  if [ -w "$1" ] || mkdir -p "$1" 2>/dev/null && [ -w "$1" ]; then
    mv "$tmp/$BIN" "$1/$BIN"; echo "$1/$BIN"; return 0
  fi
  return 1
}
if dest=$(install_to "$BINDIR"); then :
elif have sudo && [ "$BINDIR" = "/usr/local/bin" ]; then
  echo "Installing to $BINDIR (requires sudo)..."
  sudo mv "$tmp/$BIN" "$BINDIR/$BIN"; dest="$BINDIR/$BIN"
else
  BINDIR="$HOME/.local/bin"; mkdir -p "$BINDIR"
  mv "$tmp/$BIN" "$BINDIR/$BIN"; dest="$BINDIR/$BIN"
fi

echo "Installed $BIN -> $dest"
case ":$PATH:" in
  *":$(dirname "$dest"):"*) ;;
  *) echo "Note: $(dirname "$dest") is not on your PATH. Add it to use '$BIN' directly." ;;
esac

# smoke check
"$dest" --version || err "installed binary failed to run"
