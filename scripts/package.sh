#!/usr/bin/env bash
# Builds the Windows release folders and zips: for amd64 and arm64,
#
#   dist/claimward-windows-<arch>/
#     claimward-app.exe      the app (GUI subsystem, no console window)
#     claimward-helper.exe   the privileged helper service
#     wintun.dll             WireGuard LLC's signed Wintun, downloaded and
#     wintun-LICENSE.txt     checked against a pinned SHA-256
#     install.ps1 uninstall.ps1 LICENSE README.md
#   dist/claimward-windows-<arch>-<version>.zip
#
# wintun.dll is not in the repository: it is fetched from wintun.net here,
# and refused unless its archive has the hash below. Its licence allows it
# to be distributed alongside software that uses it only through its API,
# which is what this package does; the licence travels with it.
#
# Runs anywhere Go cross-compiles (Linux, macOS, Windows with bash):
#   ./scripts/package.sh            VERSION=v0.1.0 ./scripts/package.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
WINTUN_VERSION="0.14.1"
WINTUN_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
ARCHES="${ARCHES:-amd64 arm64}"

dist="$root/dist"
cache="$dist/cache"
mkdir -p "$cache"

sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

zip="$cache/wintun-$WINTUN_VERSION.zip"
if [ ! -f "$zip" ] || [ "$(sha256 "$zip")" != "$WINTUN_SHA256" ]; then
  curl -fsSL -o "$zip.part" "https://www.wintun.net/builds/wintun-$WINTUN_VERSION.zip"
  mv "$zip.part" "$zip"
fi
got="$(sha256 "$zip")"
if [ "$got" != "$WINTUN_SHA256" ]; then
  echo "wintun-$WINTUN_VERSION.zip has SHA-256 $got, expected $WINTUN_SHA256: refusing it" >&2
  rm -f "$zip"
  exit 1
fi
rm -rf "$cache/wintun"
unzip -q -o "$zip" -d "$cache"

for arch in $ARCHES; do
  out="$dist/claimward-windows-$arch"
  rm -rf "$out"
  mkdir -p "$out"
  flags="-s -w -X main.version=$VERSION"
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" GOWORK=off \
    go build -trimpath -ldflags "$flags -H windowsgui" -o "$out/claimward-app.exe" ./cmd/claimward-app
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" GOWORK=off \
    go build -trimpath -ldflags "$flags" -o "$out/claimward-helper.exe" ./cmd/claimward-helper
  cp "$cache/wintun/bin/$arch/wintun.dll" "$out/"
  cp "$cache/wintun/LICENSE.txt" "$out/wintun-LICENSE.txt"
  cp scripts/install.ps1 scripts/uninstall.ps1 LICENSE README.md "$out/"
  (cd "$dist" && rm -f "claimward-windows-$arch-$VERSION.zip" && zip -qr "claimward-windows-$arch-$VERSION.zip" "claimward-windows-$arch")
  echo "$(sha256 "$dist/claimward-windows-$arch-$VERSION.zip")  claimward-windows-$arch-$VERSION.zip"
done
