#!/bin/sh
# Cross-compile kickd for every supported OS into dist/.
set -eu
cd "$(dirname "$0")/.."
version="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
mkdir -p dist
for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  out="dist/kickd-${os}-${arch}"
  [ "$os" = windows ] && out="${out}.exe"
  echo "building $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "$out" ./cmd/kickd
done
