#!/bin/sh
# Prints the licenses of the third-party software in the kickd executables:
# the Go standard library, and every Go module that any release platform
# links into kickd. The release workflow attaches the output to each
# release as THIRD_PARTY_LICENSES.txt.
#
# The Go license is read from $(go env GOROOT)/LICENSE, which the official
# Go distributions include.
set -eu
cd "$(dirname "$0")/.."

goroot=$(go env GOROOT)
gover=$(go env GOVERSION)
if [ ! -f "$goroot/LICENSE" ]; then
  echo "third-party-licenses.sh: $goroot/LICENSE not found; use an official Go distribution" >&2
  exit 1
fi

modules=$(
  for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
      go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./cmd/kickd
  done | sort -u
)

rule='================================================================================'

# licenses NAME DIR prints every license, notice and patent file in DIR.
licenses() {
  found=0
  for f in "$2"/*; do
    [ -f "$f" ] || continue
    base=$(basename "$f")
    case $(printf '%s' "$base" | tr 'A-Z' 'a-z') in
      licen[cs]e* | copying* | notice* | patents*) ;;
      *) continue ;;
    esac
    printf '%s\n%s: %s\n%s\n\n' "$rule" "$1" "$base" "$rule"
    cat "$f"
    printf '\n'
    found=1
  done
  if [ "$found" = 0 ]; then
    echo "third-party-licenses.sh: no license file in $2 ($1)" >&2
    exit 1
  fi
}

echo "Third-party software in kickd"
echo
echo "The kickd executables include the Go standard library and the Go modules"
echo "below. Their license files follow, as each project ships them."
echo
echo "- Go standard library $gover"
for m in $modules; do
  echo "- ${m%@*} ${m#*@}"
done
echo

licenses "Go standard library $gover" "$goroot"
for m in $modules; do
  licenses "${m%@*} ${m#*@}" "$(go list -m -f '{{.Dir}}' "${m%@*}")"
done
