# kickd

kickd runs commands when something happens: a schedule comes due, an HTTP request arrives, files change in a directory, or someone fires the command from the command line.
It is a single executable for macOS, Linux and Windows.
Its config is YAML and has the same format on every OS.

![Four triggers fire events: a cron schedule at 3:00, a POST request to /hooks/deploy, changes in ~/app/src, and the command kickd event notify. The kickd agent runs the command of each event: rsync for backup, deploy.sh for deploy, make build for build, and notify.sh for notify.](images/overview.svg)

The source, the releases and the examples are on [GitHub](https://github.com/etak64n/kickd).

## Getting started

- [Quick start](quickstart.md): a first event, run in a terminal and fired by hand
- [Installing on macOS](install-macos.md): the executable, the config, a LaunchAgent or a LaunchDaemon, and what commands can reach under launchd
- [Installing on Linux](install-linux.md): the executable, the config, a system-wide or a per-user systemd unit, and the limit on watched directories
- [Installing on Windows](install-windows.md): the executable, the config, a Windows service, and the environment of the SYSTEM account

## Guides

- [Configuration](configuration.md): where the config lives, defining events, events in several files, applying changes, examples
- [Runs](runs.md): how firings become runs, run statuses, interrupted runs, the database
- [Running commands](commands.md): the environment of a command under each kind of service, the working directory and `PATH`, Python and Node.js
- [Webhooks](webhook.md): authentication, request IDs, parameters, responses
- [Examples](https://github.com/etak64n/kickd/tree/main/examples): complete config files for common tasks, such as deploys from GitHub, rebuilds, backups and certificate renewal

## Settings

- [Configuration reference](config-keys.md): every key of the config, its default, and when a change takes effect
- [Log settings](settings/log.md): the log file, levels, formats and rotation
- [Webhook server settings](settings/webhook.md): turning the HTTP server on and off, its address and the body limit
- [Database settings](settings/database.md): the SQLite file and how long ended runs stay
- [Event settings](settings/events.md): commands, working directories, environment variables, time limits, overlap and interrupted runs
- [Parameter settings](settings/params.md): the values that a firing passes to the command
- [Trigger settings](settings/triggers.md): manual, cron, webhook, file, after, startup and wake triggers

## Reference

- [Command-line reference](cli.md): every subcommand, firing events with parameters, exit codes
- [What a command receives](payload.md): environment variables and the payload JSON
- [Logging](logging.md): formats, levels, keys, messages, rotation

## Internals

- [Architecture](internals/architecture.md): the parts of the agent, the path of a firing, reloading and stopping
- [Database internals](internals/database.md): the SQLite database, locking between processes, consumption of the queue, recovery at startup
- [Delivery guarantees and idempotency](internals/idempotency.md): when a firing can be lost or repeated, and how to write commands that tolerate repeats
- [How commands run](internals/execution.md): processes, environment, output, stopping, exit status
- [Platform differences](internals/platforms.md): processes, signals, file watching, services and permissions on macOS, Linux and Windows
- [User accounts](internals/users.md): which user runs the agent and its commands, who can fire events, and the files that kickd creates

## Project

- [Known limitations](limitations.md): what kickd does not do yet, and what has not been tested
- [Building kickd](build.md): building from source, cross builds for other platforms, release builds, versions
- [Development](development.md): tests, the documentation site, source layout
