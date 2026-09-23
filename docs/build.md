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

## Executables for machines without Go

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

## Version

`kickd version` prints the version embedded at build time.
`scripts/build-all.sh` embeds the output of `git describe`.
Builds made with plain `go build` or `go install` print `dev`.
