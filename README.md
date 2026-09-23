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

## Quick start

The quick start needs Go 1.25 or later.

1. Install kickd.
   Go places the executable in `$(go env GOPATH)/bin`, which is `~/go/bin` unless Go is configured otherwise.
   If the shell cannot find `kickd`, add that directory to `PATH`.

   ```sh
   go install github.com/etak64n/kickd/cmd/kickd@latest
   ```

2. Create a config file named `kickd.yaml` that defines one event, `hello`.

   ```yaml
   events:
     - name: hello
       shell: "echo hello from kickd"
   ```

3. Start the agent in the foreground.
   Ctrl+C stops it.

   ```sh
   kickd run -c kickd.yaml
   ```

4. In another terminal, fire the event.
   With `--wait`, `kickd event` waits for the run to finish and prints the result.

   ```sh
   kickd event hello --wait -c kickd.yaml
   ```

   ```text
   RUN  EVENT  ATTEMPT  STATUS     EXIT  DURATION  DETAIL
   1    hello  1        succeeded  0     12ms      -
   ```

5. Show the run, including the output of its command.

   ```sh
   kickd show 1 -c kickd.yaml
   ```

To keep kickd running in the background, install it as a service by following the installation guide for the OS.

## Documentation

- [Building kickd](docs/build.md): `go install`, building from source, and executables for machines without Go
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
