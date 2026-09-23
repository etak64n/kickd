# Building kickd

[Documentation index](../README.md#documentation)

kickd is written in Go and builds with Go 1.25 or later.
The long-running agent and every other command are subcommands of one executable, `kickd`.
The executable needs neither Go nor any other runtime on the machine where it runs.

## Installing with go install

`go install` downloads the source, builds it, and places `kickd` in `$(go env GOPATH)/bin`.

```sh
go install github.com/etak64n/kickd/cmd/kickd@latest
```

## Building from source

```sh
git clone https://github.com/etak64n/kickd.git
cd kickd
go build -o kickd ./cmd/kickd        # on Windows: go build -o kickd.exe ./cmd/kickd
```

## Cross builds for other platforms

Go can build executables for other operating systems and CPUs.
kickd is written entirely in Go, including its SQLite driver, so these builds need no C compiler.

`scripts/build-all.sh` builds kickd for six platforms into `dist/`.
The script runs on macOS and Linux.

```sh
./scripts/build-all.sh
```

| Machine | Executable |
|---|---|
| Mac with Apple silicon | `kickd-darwin-arm64` |
| Mac with an Intel CPU | `kickd-darwin-amd64` |
| Linux on x86-64 | `kickd-linux-amd64` |
| Linux on 64-bit ARM, such as a Raspberry Pi with a 64-bit OS | `kickd-linux-arm64` |
| Windows on x64 | `kickd-windows-amd64.exe` |
| Windows on ARM | `kickd-windows-arm64.exe` |

To build for a single platform, set `GOOS` and `GOARCH`:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o kickd-linux-arm64 ./cmd/kickd
```

`uname -m` on macOS and Linux, and `$env:PROCESSOR_ARCHITECTURE` in PowerShell on Windows, print the CPU of a machine.
`x86_64` and `AMD64` call for the amd64 executable.
`arm64`, `aarch64` and `ARM64` call for the arm64 executable.

## Release builds

Pushing a tag whose name starts with `v`, such as `v0.2.0`, starts the release workflow on GitHub Actions.
The workflow runs the tests, builds the six executables with `scripts/build-all.sh`, and writes their SHA-256 hashes to `checksums.txt`.
It then publishes the seven files as a release named after the tag.
The workflow builds with the latest Go release, so release executables include its security fixes.

```sh
git tag v0.2.0
git push origin v0.2.0
```

The file names carry no version, so `https://github.com/etak64n/kickd/releases/latest/download/<file>` always points to the file of the latest release.

## Version

`kickd version` prints the version of the executable.
Release executables and `scripts/build-all.sh` embed a version at build time: the tag, or the output of `git describe`.

Without an embedded version, kickd prints the version that the go command recorded in the executable.
`go install ...@latest` records the tag that it installed.
A build inside a git checkout records a pseudo-version made from the commit, with `+dirty` when the checkout has uncommitted changes.
When neither is available, kickd prints `dev`.
