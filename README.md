<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/logo-dark.svg">
  <img alt="kickd" src="docs/images/logo-light.svg" width="300">
</picture>

[![Go Reference](https://pkg.go.dev/badge/github.com/etak64n/kickd.svg)](https://pkg.go.dev/github.com/etak64n/kickd)
[![CI](https://github.com/etak64n/kickd/actions/workflows/ci.yml/badge.svg)](https://github.com/etak64n/kickd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/etak64n/kickd)](https://github.com/etak64n/kickd/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/etak64n/kickd)](go.mod)
[![License](https://img.shields.io/github/license/etak64n/kickd)](LICENSE)

</div>

kickd runs commands when something happens: a schedule comes due, an HTTP request arrives, files change in a directory, or someone fires the command from the command line.
It is a single executable for macOS, Linux and Windows.
Its config file is YAML and has the same format on every OS.

![Four triggers fire events: a cron schedule at 3:00, a POST request to /hooks/deploy, changes in ~/app/src, and the command kickd event notify. kickd runs the command of each event: rsync for backup, deploy.sh for deploy, make build for build, and notify.sh for notify.](docs/images/overview.svg)

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

Each firing is first recorded as a **run** in the **database**, a SQLite file.
The long-running kickd process, called the **agent**, starts the command of each run.
A run that has not started yet, such as one that waits for the previous run of its event, waits in the **queue**.
The database is a file, so waiting runs survive a restart of the agent or of the machine.

## The config file

A config file is YAML with four sections: `log`, `webhook`, `database` and `events`.
Only `events` is required, and every other key has a default.
Commands, shells and paths differ between operating systems, so a config file is written for one OS.
`kickd init` writes these files, the one for the OS that it runs on.
The paths of the log and the database depend on where the config file is: these files have them for a config file in the home directory on macOS and Linux, and in `C:\ProgramData\kickd` on Windows.
Each file defines one event for each kind of trigger:

<details open>
<summary>macOS</summary>

```yaml
log:
  path: '~/Library/Logs/kickd/kickd.log'   # without a path, kickd logs to standard error
  level: info          # trace | debug | info | warn | error | fatal (LOG_LEVEL overrides it)
  format: auto         # auto | json | text: auto writes text to a terminal and JSON elsewhere (LOG_FORMAT overrides it)
  max_size_mb: 10      # past this size, kickd renames the file with .1 appended and starts a new one
  max_backups: 5       # how many renamed files to keep

webhook:
  enabled: true              # false keeps the HTTP server off, so webhook triggers do not fire
  listen: '127.0.0.1:8787'   # the HTTP server starts only when an event has a webhook trigger
  max_body_bytes: 1048576    # the largest request body accepted

database:
  path: '~/Library/Application Support/kickd/kickd.db'   # the SQLite file that records every run
  retention: 168h         # how long finished runs stay in the history

events:
  # Cron: every night at 3:00, Tokyo time.
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    workdir: '~'
    timeout: 1h              # the longest time the command may run
    concurrency: skip        # a firing while the backup runs is skipped
    on_interrupt: rerun      # run again when a stop or a crash cut the run off
    triggers:
      - type: cron
        schedule: '0 3 * * *'   # minute hour day month weekday
        timezone: Asia/Tokyo    # without it, local time
        missed: run             # after sleep or downtime, run once for the missed times

  # Webhook: POST /hooks/deploy with the header Authorization: Bearer <token>.
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
    timeout: 10m
    concurrency: queue       # deploys wait for each other and run in order
    on_interrupt: abandon    # a deploy that a stop or a crash cut off does not run again
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        methods: [POST]
        token: 'replace-with-a-long-random-string'

  # File changes: 2 seconds after the last change in ~/app/src or below.
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    timeout: 10m
    concurrency: queue       # changes during a build are built after it
    on_interrupt: abandon    # the next change starts a new build
    triggers:
      - type: file
        path: '~/app/src'
        recursive: true      # also watch subdirectories
        debounce: 2s

  # No triggers: runs only by hand, with kickd event notify.
  - name: notify
    command: ['./notify.sh']
    workdir: '~/app'
    timeout: 1m
    concurrency: parallel    # notifications do not wait for each other
    on_interrupt: abandon
```

</details>

<details>
<summary>Linux</summary>

```yaml
log:
  path: '~/.local/state/kickd/kickd.log'   # without a path, kickd logs to standard error
  level: info          # trace | debug | info | warn | error | fatal (LOG_LEVEL overrides it)
  format: auto         # auto | json | text: auto writes text to a terminal and JSON elsewhere (LOG_FORMAT overrides it)
  max_size_mb: 10      # past this size, kickd renames the file with .1 appended and starts a new one
  max_backups: 5       # how many renamed files to keep

webhook:
  enabled: true              # false keeps the HTTP server off, so webhook triggers do not fire
  listen: '127.0.0.1:8787'   # the HTTP server starts only when an event has a webhook trigger
  max_body_bytes: 1048576    # the largest request body accepted

database:
  path: '~/.local/state/kickd/kickd.db'   # the SQLite file that records every run
  retention: 168h         # how long finished runs stay in the history

events:
  # Cron: every night at 3:00, Tokyo time.
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    workdir: '~'
    timeout: 1h              # the longest time the command may run
    concurrency: skip        # a firing while the backup runs is skipped
    on_interrupt: rerun      # run again when a stop or a crash cut the run off
    triggers:
      - type: cron
        schedule: '0 3 * * *'   # minute hour day month weekday
        timezone: Asia/Tokyo    # without it, local time
        missed: run             # after sleep or downtime, run once for the missed times

  # Webhook: POST /hooks/deploy with the header Authorization: Bearer <token>.
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
    timeout: 10m
    concurrency: queue       # deploys wait for each other and run in order
    on_interrupt: abandon    # a deploy that a stop or a crash cut off does not run again
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        methods: [POST]
        token: 'replace-with-a-long-random-string'

  # File changes: 2 seconds after the last change in ~/app/src or below.
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    timeout: 10m
    concurrency: queue       # changes during a build are built after it
    on_interrupt: abandon    # the next change starts a new build
    triggers:
      - type: file
        path: '~/app/src'
        recursive: true      # also watch subdirectories
        debounce: 2s

  # No triggers: runs only by hand, with kickd event notify.
  - name: notify
    command: ['./notify.sh']
    workdir: '~/app'
    timeout: 1m
    concurrency: parallel    # notifications do not wait for each other
    on_interrupt: abandon
```

</details>

<details>
<summary>Windows</summary>

```yaml
log:
  path: 'C:\ProgramData\kickd\kickd.log'   # without a path, kickd logs to standard error
  level: info          # trace | debug | info | warn | error | fatal (LOG_LEVEL overrides it)
  format: auto         # auto | json | text: auto writes text to a terminal and JSON elsewhere (LOG_FORMAT overrides it)
  max_size_mb: 10      # past this size, kickd renames the file with .1 appended and starts a new one
  max_backups: 5       # how many renamed files to keep

webhook:
  enabled: true              # false keeps the HTTP server off, so webhook triggers do not fire
  listen: '127.0.0.1:8787'   # the HTTP server starts only when an event has a webhook trigger
  max_body_bytes: 1048576    # the largest request body accepted

database:
  path: 'C:\ProgramData\kickd\kickd.db'   # the SQLite file that records every run
  retention: 168h         # how long finished runs stay in the history

events:
  # Cron: every night at 3:00, Tokyo time.
  - name: backup
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'backup.ps1']
    workdir: 'C:\scripts'
    timeout: 1h              # the longest time the command may run
    concurrency: skip        # a firing while the backup runs is skipped
    on_interrupt: rerun      # run again when a stop or a crash cut the run off
    triggers:
      - type: cron
        schedule: '0 3 * * *'   # minute hour day month weekday
        timezone: Asia/Tokyo    # without it, local time
        missed: run             # after sleep or downtime, run once for the missed times

  # Webhook: POST /hooks/deploy with the header Authorization: Bearer <token>.
  - name: deploy
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'deploy.ps1']
    workdir: 'C:\app'
    timeout: 10m
    concurrency: queue       # deploys wait for each other and run in order
    on_interrupt: abandon    # a deploy that a stop or a crash cut off does not run again
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        methods: [POST]
        token: 'replace-with-a-long-random-string'

  # File changes: 2 seconds after the last change in C:\app\src or below.
  - name: build
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'build.ps1']
    workdir: 'C:\app'
    timeout: 10m
    concurrency: queue       # changes during a build are built after it
    on_interrupt: abandon    # the next change starts a new build
    triggers:
      - type: file
        path: 'C:\app\src'
        recursive: true      # also watch subfolders
        debounce: 2s

  # No triggers: runs only by hand, with kickd event notify.
  - name: notify
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'notify.ps1']
    workdir: 'C:\app'
    timeout: 1m
    concurrency: parallel    # notifications do not wait for each other
    on_interrupt: abandon
```

</details>

A `command` given as a string runs through the shell, `/bin/sh` on macOS and Linux and cmd on Windows, and a list starts its program directly; the Windows file starts PowerShell scripts with lists.
Relative paths start at the directory of the config file, and a leading `~` is the home directory.
Strings are in single quotes, which keep backslashes and double quotes as they are.

`kickd check` validates a config file, lists its events and triggers, and prints where the log, the database and each command run.
The [configuration reference](docs/config-keys.md) describes every key, and the [examples](examples/README.md) are complete configs for common tasks.

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

The releases page shows the SHA-256 digest of every file.
On Linux, these commands download kickd for x86-64, print its SHA-256 hash to compare with the digest on the page, and install it as `/usr/local/bin/kickd`:

```sh
curl -fLO https://github.com/etak64n/kickd/releases/latest/download/kickd-linux-amd64
sha256sum kickd-linux-amd64
sudo install -m 755 kickd-linux-amd64 /usr/local/bin/kickd
```

On a Mac, the same commands work with the file name for the Mac, and with `shasum -a 256` in place of `sha256sum`.

With Go 1.25 or later, `go install` builds kickd from source instead.
Go places the executable in `$(go env GOPATH)/bin`, which is `~/go/bin` unless Go is configured otherwise.

```sh
go install github.com/etak64n/kickd/cmd/kickd@latest
```

The installation guides for macOS, Linux and Windows cover each OS in detail, including running kickd as a service.

## Quick start

With `kickd` installed, these steps define an event, run the agent and fire the event.

1. Create a config file named `kickd.yaml` that defines one event, `hello`.
   Without `database.path`, kickd keeps its database, `kickd.db`, next to `kickd.yaml`.

   ```yaml
   events:
     - name: hello
       command: 'echo hello from kickd'
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
- [Runs](docs/runs.md): how firings become runs, run statuses, interrupted runs, the database
- [Command-line reference](docs/cli.md): every subcommand, firing events with parameters, exit codes
- [Webhooks](docs/webhook.md): authentication, request IDs, parameters, responses
- [Configuration reference](docs/config-keys.md): every key of the config file and its default
- [Examples](examples/README.md): complete config files for common tasks, such as OCR of scans, deploys from GitHub, backups and certificate renewal
- [What a command receives](docs/payload.md): environment variables and the payload JSON
- [Running commands](docs/commands.md): the environment of a command under each kind of service, the working directory and `PATH`, Python and Node.js
- [Logging](docs/logging.md): formats, levels, keys, messages, rotation
- [Known limitations](docs/limitations.md): what kickd does not do yet, and what has not been tested
- [Development](docs/development.md): tests, builds, source layout

### Internals

- [Architecture](docs/internals/architecture.md): the parts of the agent, the path of a firing, reloading and stopping
- [Database internals](docs/internals/database.md): the SQLite database, locking between processes, consumption of the queue, recovery at startup
- [Delivery guarantees and idempotency](docs/internals/idempotency.md): when a firing can be lost or repeated, and how to write commands that tolerate repeats
- [How commands run](docs/internals/execution.md): processes, environment, output, stopping, exit status
- [Platform differences](docs/internals/platforms.md): processes, signals, file watching, services and permissions on macOS, Linux and Windows
- [User accounts](docs/internals/users.md): which user runs the agent and its commands, who can fire events, and the files that kickd creates

## License

kickd is released under the [MIT License](LICENSE).
`kickd licenses` prints the licenses of kickd and of the third-party software that its executables include.
