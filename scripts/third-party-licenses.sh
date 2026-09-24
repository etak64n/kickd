#!/bin/sh
# Writes internal/licenses/licenses.txt, the text that "kickd licenses"
# prints: the license of kickd, of the Go standard library, and of every
# Go module that any release platform links into kickd. Run it after a
# change to the dependencies in go.mod.
#
# With --check, it writes nothing and fails when the file is out of date.
set -eu
cd "$(dirname "$0")/.."
out=internal/licenses/licenses.txt
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

modules=$(
  for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
      go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./cmd/kickd
  done | sort -u
)

rule='================================================================================'

# section NAME FILE prints one license file under a heading.
section() {
  printf '%s\n%s\n%s\n\n' "$rule" "$1" "$rule"
  cat "$2"
  printf '\n'
}

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
    section "$1: $base" "$f"
    found=1
  done
  if [ "$found" = 0 ]; then
    echo "third-party-licenses.sh: no license file in $2 ($1)" >&2
    exit 1
  fi
}

{
  echo "Licenses of kickd"
  echo
  echo "kickd is released under the MIT License. Its executables also include the"
  echo "Go standard library and the Go modules below, under their own licenses."
  echo
  echo "- Go standard library"
  for m in $modules; do
    echo "- ${m%@*} ${m#*@}"
  done
  echo
  section "kickd: LICENSE" LICENSE
  # A copy of the LICENSE file of the official Go distributions; CI checks
  # that it still matches.
  section "Go standard library: LICENSE" internal/licenses/go-stdlib-LICENSE
  for m in $modules; do
    licenses "${m%@*} ${m#*@}" "$(go list -m -f '{{.Dir}}' "${m%@*}")"
  done
} > "$tmp"

if [ "${1:-}" = --check ]; then
  if ! cmp -s "$tmp" "$out"; then
    echo "third-party-licenses.sh: $out is out of date; run scripts/third-party-licenses.sh" >&2
    exit 1
  fi
  exit 0
fi
mv "$tmp" "$out"
