#!/bin/sh
# wandersort installer for macOS and Linux.
#   curl -fsSL https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.sh | sh
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

have tar || err "tar is required"
if have curl; then dl() { curl -fsSL "$1"; }; dlo() { curl -fsSL "$1" -o "$2"; }
elif have wget; then dl() { wget -qO- "$1"; }; dlo() { wget -qO "$2" "$1"; }
else err "need curl or wget"; fi

# --- resolve version ----------------------------------------------------------
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION=$(dl "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d'"' -f4)
  [ -n "$VERSION" ] || err "could not resolve latest version"
fi
num="${VERSION#v}" # strip leading v for asset names

asset="${BIN}_${num}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

# --- download + verify checksum -----------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $asset ($VERSION)..."
dlo "$base/$asset" "$tmp/$asset" || err "download failed: $base/$asset"

if dlo "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  want=$(grep " $asset\$" "$tmp/checksums.txt" | awk '{print $1}')
  if [ -n "$want" ]; then
    if have sha256sum; then got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    elif have shasum; then got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    else got=""; fi
    [ -z "$got" ] || [ "$got" = "$want" ] || err "checksum mismatch for $asset"
  fi
fi

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/$BIN" ] || err "binary '$BIN' not found in archive"
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
