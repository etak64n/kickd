---
title: kickd
description: "kickd runs commands when something happens: a schedule comes due, an HTTP request arrives, files change in a directory, or someone fires the command from the command line."
# The tab shows the name alone, instead of the page and the site: kickd | kickd.
head:
  - tag: title
    content: kickd
---

kickd runs commands when something happens: a schedule comes due, an HTTP request arrives, files change in a directory, or someone fires the command from the command line.
It is a single executable for macOS, Linux and Windows.
Its config is YAML and has the same format on every OS.

![Four triggers fire events: a cron schedule at 3\:00, a POST request to /hooks/deploy, changes in ~/app/src, and the command kickd event notify. The kickd agent runs the command of each event: rsync for backup, deploy.sh for deploy, make build for build, and notify.sh for notify.](../../assets/overview.svg)

The source, the releases and the examples are on [GitHub](https://github.com/etak64n/kickd).

## Getting started

- [Quick start](/kickd/getting-started/quickstart/): a first event, run in a terminal and fired by hand
- [Installing on macOS](/kickd/getting-started/install-macos/): the executable, the config, a LaunchAgent or a LaunchDaemon, and what commands can reach under launchd
- [Installing on Linux](/kickd/getting-started/install-linux/): the executable, the config, a system-wide or a per-user systemd unit, and the limit on watched directories
- [Installing on Windows](/kickd/getting-started/install-windows/): the executable, the config, a Windows service, and the environment of the SYSTEM account

## Guides

- [How kickd works](/kickd/guides/how-kickd-works/): step by step, how a trigger fires an event, how the firing becomes a run, and how the agent runs its command
- [Configuration](/kickd/guides/configuration/): where the config lives, defining events, events in several files, applying changes, examples
- [Runs](/kickd/guides/runs/): how firings become runs, run statuses, interrupted runs, the database
- [Running commands](/kickd/guides/commands/): the environment of a command under each kind of service, the working directory and `PATH`, Python and Node.js
- [User accounts](/kickd/guides/users/): which user runs the agent and its commands, who can fire events, and the files that kickd creates
- [Webhooks](/kickd/guides/webhooks/): authentication, request IDs, parameters, responses
- [Examples](https://github.com/etak64n/kickd/tree/main/examples): complete config files for common tasks, such as deploys from GitHub, rebuilds, backups and certificate renewal

## Settings

- [Configuration reference](/kickd/settings/config-keys/): every key of the config, its default, and when a change takes effect
- [Log settings](/kickd/settings/log/): the log file, levels, formats and rotation
- [Webhook server settings](/kickd/settings/webhook/): turning the HTTP server on and off, its address and the body limit
- [Database settings](/kickd/settings/database/): the SQLite file and how long ended runs stay
- [Event settings](/kickd/settings/events/): commands, working directories, environment variables, time limits, overlap and interrupted runs
- [Parameter settings](/kickd/settings/params/): the values that a firing passes to the command
- [Trigger settings](/kickd/settings/triggers/): manual, cron, webhook, file, after, startup and wake triggers

## Reference

- [Command-line reference](/kickd/reference/cli/): every subcommand, firing events with parameters, exit codes
- [What a command receives](/kickd/reference/payload/): environment variables and the payload JSON
- [Logging](/kickd/reference/logging/): formats, levels, keys, messages, rotation

## Internals

- [Architecture](/kickd/internals/architecture/): the parts of the agent, the path of a firing, reloading and stopping
- [Database internals](/kickd/internals/database/): the SQLite database, locking between processes, consumption of the queue, recovery at startup
- [Delivery guarantees and idempotency](/kickd/internals/idempotency/): when a firing can be lost or repeated, and how to write commands that tolerate repeats
- [How commands run](/kickd/internals/execution/): processes, environment, output, stopping, exit status
- [Platform differences](/kickd/internals/platforms/): processes, signals, file watching, services and permissions on macOS, Linux and Windows

## Project

- [Known limitations](/kickd/project/limitations/): what kickd does not do yet, and what has not been tested
- [Building kickd](/kickd/project/build/): building from source, cross builds for other platforms, release builds, versions
- [Development](/kickd/project/development/): tests, the documentation site, source layout
