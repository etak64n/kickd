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

![Four triggers fire events: a cron schedule at 3:00, a POST request to /hooks/deploy, changes in ~/app/src, and the command kickd event notify. The kickd agent runs the command of each event: rsync for backup, deploy.sh for deploy, make build for build, and notify.sh for notify.](docs/images/overview.svg)

## How kickd works

A named command in the config file is called an **event**.
An event holds the command to run and a list of triggers.
Firing an event requests one run of its command.

A **trigger** fires an event.
kickd has seven kinds of trigger:

- **Manual**: `kickd event NAME` fires the event.
- **Cron**: the event fires at the times given by a cron expression.
- **Webhook**: the event fires when an HTTP request arrives at a path that kickd listens on.
- **File**: the event fires when files are created, written, removed or renamed in a directory.
- **After**: the event fires when a run of another event ends with one of the given statuses, such as a backup that failed.
- **Startup**: the event fires once when the agent starts.
- **Wake**: the event fires when the machine wakes from sleep.

An event fires only through the triggers that it lists, and it can list several of them.

Each firing is first recorded as a **run** in the **database**, a SQLite file.
The long-running kickd process, called the **agent**, starts the command of each run.
A run that has not started yet, such as one that waits for the previous run of its event, waits in the **queue**.
The database is a file, so waiting runs survive a restart of the agent or of the machine.

## Features

- **Recovery after a stop or crash**: each event chooses whether a run that was cut off starts again or is given up.
- **Overlap control**: each event chooses whether a firing that arrives while it is running is skipped, queued, or run at the same time.
- **Time limits**: commands that run past their limit are stopped.
- **Live reload**: saving the config file, or a file of events next to it, applies the change without a restart.
- **Events in several files**: each group of events can have a YAML file of its own next to the config file.
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

The installation guides for [macOS](docs/install-macos.md), [Linux](docs/install-linux.md) and [Windows](docs/install-windows.md) cover each OS in detail, including running kickd as a service.

## Quick start

With `kickd` installed, these steps define an event, run the agent and fire the event.

1. In an empty directory, create a config file named `kickd.yaml` that defines one event, `hello`.
   Without `database.path`, kickd keeps its database, `kickd.db`, next to `kickd.yaml`.

   ```yaml
   events:
     - name: hello
       command: 'echo hello from kickd'
       triggers:
         - type: manual   # kickd event hello fires it
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

## The config file

kickd reads its settings from `~/.kickd/config.yaml`, on every OS, and its events from the YAML files in `~/.kickd`.
`config.yaml` holds the three sections of settings, `log`, `webhook` and `database`, and every key of them has a default.
Events are listed under `events`, in `config.yaml` or in any other `.yaml` or `.yml` file of the directory: kickd reads every file there that has an `events` section, so each group of events, such as those of one app, can have a file of its own.

Commands, shells and paths differ between operating systems, so the files are written for one OS.
`kickd init` writes two files for the OS that it runs on: `config.yaml`, and `event.example.yaml` with one event for each of the manual, cron, webhook and file triggers.
Every event there has a manual trigger, and notify follows a backup that fails with an after trigger.
On Windows, the files are for a service of the whole system, in `C:\ProgramData\kickd`, because Windows has no service for one user:

<details open>
<summary>macOS and Linux</summary>

`~/.kickd/config.yaml`:

```yaml
# The settings of kickd. kickd also reads the events of every other .yaml
# and .yml file in this directory that has an events section, such as
# event.example.yaml. An events section in this file works too.

log:
  path: '~/.kickd/kickd.log'   # without a path, kickd logs to standard error
  level: info          # trace | debug | info | warn | error | fatal (LOG_LEVEL overrides it)
  format: auto         # auto | json | text: auto writes text to a terminal and JSON elsewhere (LOG_FORMAT overrides it)
  max_size_mb: 10      # past this size, kickd renames the file with .1 appended and starts a new one
  max_backups: 5       # how many renamed files to keep

webhook:
  enabled: true              # false keeps the HTTP server off, so webhook triggers do not fire
  listen: '127.0.0.1:8787'   # the HTTP server starts only when an event has a webhook trigger
  max_body_bytes: 1048576    # the largest request body accepted

database:
  path: '~/.kickd/kickd.db'   # the SQLite file that records every run
  retention: 168h         # how long finished runs stay in the history
```

`~/.kickd/event.example.yaml`:

```yaml
# Events of kickd. kickd reads the events section of every .yaml and .yml
# file next to its config file, so the events can be split into files.

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
      - type: manual            # kickd event backup also runs it

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
      - type: manual            # kickd event deploy also runs it

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
      - type: manual         # kickd event build also runs it

  # Manual and after: by hand with kickd event notify, and when a backup fails.
  - name: notify
    command: ['./notify.sh']
    workdir: '~/app'
    timeout: 1m
    concurrency: parallel    # notifications do not wait for each other
    on_interrupt: abandon
    triggers:
      - type: manual
      - type: after             # a backup that failed or was given up
        event: backup
        status: [failed, abandoned]
```

</details>

<details>
<summary>Windows</summary>

`C:\ProgramData\kickd\config.yaml`:

```yaml
# The settings of kickd. kickd also reads the events of every other .yaml
# and .yml file in this directory that has an events section, such as
# event.example.yaml. An events section in this file works too.

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
```

`C:\ProgramData\kickd\event.example.yaml`:

```yaml
# Events of kickd. kickd reads the events section of every .yaml and .yml
# file next to its config file, so the events can be split into files.

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
      - type: manual            # kickd event backup also runs it

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
      - type: manual            # kickd event deploy also runs it

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
      - type: manual         # kickd event build also runs it

  # Manual and after: by hand with kickd event notify, and when a backup fails.
  - name: notify
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'notify.ps1']
    workdir: 'C:\app'
    timeout: 1m
    concurrency: parallel    # notifications do not wait for each other
    on_interrupt: abandon
    triggers:
      - type: manual
      - type: after             # a backup that failed or was given up
        event: backup
        status: [failed, abandoned]
```

</details>

A `command` given as a string runs through the shell, `/bin/sh` on macOS and Linux and cmd on Windows, and a list starts its program directly; the Windows file starts PowerShell scripts with lists.
Relative paths start at the directory of the config file, and a leading `~` is the home directory.
Strings are in single quotes, which keep backslashes and double quotes as they are.

`kickd check` validates the config, lists the files whose events it reads, and prints the events with their triggers and where the log, the database and each command run.
The [configuration reference](docs/config-keys.md) describes every key, and the [examples](examples/README.md) are complete configs for common tasks.

## Documentation

The [documentation site](https://etak64n.github.io/kickd/) has the guides, the settings of every key, and the internals of kickd, as of the latest release.
The same pages are the Markdown files in `docs`, which [the documentation index](docs/index.md) lists.

## License

kickd is released under the [MIT License](LICENSE).
`kickd licenses` prints the licenses of kickd and of the third-party software that its executables include.
