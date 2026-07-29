#!/bin/sh
# Build an (unsigned) macOS .pkg from a released tarball and upload it to the
# GitHub release. Runs on a macOS runner after GoReleaser has published the tag.
#   Usage: TAG=v0.1.0 scripts/build-pkg.sh
# Requires: pkgbuild (ships with macOS), gh (authenticated), tar, curl.
set -eu

REPO="jammutkarsh/wandersort"
BIN="wandersort"
IDENT="com.jammutkarsh.wandersort"
TAG="${TAG:?set TAG=vX.Y.Z}"
num="${TAG#v}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for arch in amd64 arm64; do
  asset="${BIN}_${num}_darwin_${arch}.tar.gz"
  url="https://github.com/$REPO/releases/download/$TAG/$asset"
  echo "== $arch: fetching $asset"
  curl -fsSL "$url" -o "$work/$asset"

  root="$work/root-$arch"
  mkdir -p "$root/usr/local/bin"
  tar -xzf "$work/$asset" -C "$root/usr/local/bin" "$BIN"
  chmod +x "$root/usr/local/bin/$BIN"

  pkg="${BIN}-${num}-${arch}.pkg"
  pkgbuild \
    --root "$root" \
    --identifier "$IDENT" \
    --version "$num" \
    --install-location "/" \
    "$work/$pkg"

  echo "== uploading $pkg to $TAG"
  gh release upload "$TAG" "$work/$pkg" --clobber --repo "$REPO"
done

echo "done."
