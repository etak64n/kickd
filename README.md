# kickd

kickd runs commands when something happens: a schedule comes due, an HTTP request arrives, files change in a directory, or someone fires the command from the command line.
It is a single executable for macOS, Linux and Windows.
Its config file is YAML and has the same format on every OS.

## How kickd works

A named command in the config file is called an **event**.
An event holds the command to run and, optionally, a list of triggers.
Firing an event requests one run of its command.

A **trigger** fires an event.
kickd has four kinds of trigger:

- **Manual**: `kickd event NAME` fires the event. Every event can be fired this way, whether or not it has other triggers.
- **Cron**: the event fires at the times given by a cron expression.
- **Webhook**: the event fires when an HTTP request arrives at a path that kickd listens on.
- **File**: the event fires when files are created, written, removed or renamed in a directory.

Each firing is first written as a **run** to a SQLite database called the **queue**.
The long-running kickd process, called the **agent**, takes runs from the queue in order and starts the event's command for each one.
The queue is a file, so runs that are still waiting survive a restart of the agent or of the machine.

## Features

- **Recovery after a stop or crash**: each event chooses whether a run that was cut off starts again or is given up.
- **Overlap control**: each event chooses whether a firing that arrives while it is running is skipped, queued, or run at the same time.
- **Time limits**: commands that run past their limit are stopped.
- **Live reload**: saving the config file applies it without a restart.
- **Queue commands**: the command line shows the queue and the run history, and cancels runs.
- **Service installation**: kickd installs itself as a launchd job on macOS, a systemd unit on Linux and a Windows service on Windows.
- **Structured logs**: JSON Lines in a log file, colored text on a terminal.

## Installation

Prebuilt executables are on the [releases page](https://github.com/etak64n/kickd/releases/latest).
Each file is kickd for one platform, and needs no other files:

| Machine | File |
|---|---|
| Mac with Apple silicon | `kickd-darwin-arm64` |
| Mac with an Intel CPU | `kickd-darwin-amd64` |
| Linux on x86-64 | `kickd-linux-amd64` |
| Linux on 64-bit ARM | `kickd-linux-arm64` |
| Windows on x64 | `kickd-windows-amd64.exe` |
| Windows on ARM | `kickd-windows-arm64.exe` |

`checksums.txt` on the same page lists the SHA-256 hash of every file.
On Linux, these commands download kickd for x86-64, check it against `checksums.txt`, and install it as `/usr/local/bin/kickd`:

```sh
curl -fLO https://github.com/etak64n/kickd/releases/latest/download/kickd-linux-amd64
curl -fLO https://github.com/etak64n/kickd/releases/latest/download/checksums.txt
sha256sum -c --ignore-missing checksums.txt
sudo install -m 755 kickd-linux-amd64 /usr/local/bin/kickd
```

On a Mac, the same commands work with the file name for the Mac and with `shasum -a 256 -c --ignore-missing checksums.txt` as the check.

With Go 1.25 or later, `go install` builds kickd from source instead.
Go places the executable in `$(go env GOPATH)/bin`, which is `~/go/bin` unless Go is configured otherwise.

```sh
go install github.com/etak64n/kickd/cmd/kickd@latest
```

The installation guides for macOS, Linux and Windows cover each OS in detail, including running kickd as a service.

## Quick start

With `kickd` installed, these steps define an event, run the agent and fire the event.

1. Create a config file named `kickd.yaml` that defines one event, `hello`.

   ```yaml
   events:
     - name: hello
       shell: "echo hello from kickd"
   ```

2. Start the agent in the foreground.
   Ctrl+C stops it.

   ```sh
   kickd run -c kickd.yaml
   ```

3. In another terminal, fire the event.
   With `--wait`, `kickd event` waits for the run to finish and prints the result.

   ```sh
   kickd event hello --wait -c kickd.yaml
   ```

   ```text
   RUN  EVENT  ATTEMPT  STATUS     EXIT  DURATION  DETAIL
   1    hello  1        succeeded  0     12ms      -
   ```

4. Show the run, including the output of its command.

   ```sh
   kickd show 1 -c kickd.yaml
   ```

To keep kickd running in the background, install it as a service by following the installation guide for the OS.

## Documentation

### Guides

- [Building kickd](docs/build.md): building from source, cross builds for other platforms, release builds, versions
- [Installing on macOS](docs/install-macos.md): the executable, the config file, running as a LaunchAgent, access to protected folders
- [Installing on Linux](docs/install-linux.md): the executable, the config file, running as a systemd unit, per-user units
- [Installing on Windows](docs/install-windows.md): the executable, the config file, running as a Windows service, the SYSTEM account
- [Configuration](docs/configuration.md): where the config file lives, defining events, applying changes, examples
- [The queue](docs/queue.md): how firings become runs, run statuses, interrupted runs
- [Command-line reference](docs/cli.md): every subcommand, firing events with parameters, exit codes
- [Webhooks](docs/webhook.md): authentication, request IDs, parameters, responses
- [Configuration reference](docs/config-keys.md): every key of the config file and its default
- [What a command receives](docs/payload.md): environment variables and the payload JSON
- [Logging](docs/logging.md): formats, levels, keys, messages, rotation
- [Known limitations](docs/limitations.md): what kickd does not do yet, and what has not been tested
- [Development](docs/development.md): tests, builds, source layout

### Internals

- [Architecture](docs/internals/architecture.md): the parts of the agent, the path of a firing, reloading and stopping
- [Queue internals](docs/internals/queue.md): the SQLite database, locking between processes, consumption, recovery at startup
- [Delivery guarantees and idempotency](docs/internals/idempotency.md): when a firing can be lost or repeated, and how to write commands that tolerate repeats
- [How commands run](docs/internals/execution.md): processes, environment, output, stopping, exit status
- [Platform differences](docs/internals/platforms.md): processes, signals, file watching, services and permissions on macOS, Linux and Windows
