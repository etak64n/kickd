---
title: Event settings
description: Commands, working directories, environment variables, time limits, overlap and interrupted runs, in the keys of each event.
---

kickd reads its **config** from the config file, `~/.kickd/config.yaml` by default, and from the other YAML files next to it that have an `events` section.
In kickd, a named command in the config is an **event**.
An event lists **triggers**, the ways in which it fires, such as a cron schedule or `kickd event NAME` on the command line.
Each firing of an event is recorded as a **run** in the **database**, a SQLite file, and the long-running kickd process, called the **agent**, starts the command of each run.

The `events` section is a list of events, in the config file or in another YAML file next to it, and the config needs at least one event in all.
Each event is a mapping of keys, of which `name`, `command` and `triggers` are required:

```yaml
events:
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    triggers:
      - type: manual
```

The agent reloads its config when the config file, or another YAML file next to it, is saved, added or removed: it waits 0.5 seconds after the change, checks the files, and switches to the new config when they have no errors.
Runs that start after the switch use the new settings of their event, including runs that were waiting in the queue.
Commands that are running keep the settings with which they started.
A few keys take effect at other moments: `description` and `params` at once for the subcommands that read the config, and `on_interrupt` and `max_attempts` at the next start of the agent.

## `name`

`name` identifies the event.

- **Type**: a string of 1 to 64 characters: letters, digits, `.`, `_`, `:` and `-`, starting with a letter or a digit.
- **Default**: none. `name` is required, and no two events of the config can have the same name, in the same file or in two files.
- **Takes effect**: when the file that defines the event is saved.

The name appears wherever kickd refers to the event: in `kickd event NAME`, in the `event` key of the log records, in the variable `KICKD_EVENT` of the command, and in the history that `kickd runs --event NAME` shows.

Renaming an event is the same as removing the old event and adding a new one.
Runs of the old name that wait in the queue are recorded as `dropped`, and a run of the old name that a stop or a crash cut off is recorded as `abandoned` at the next start of the agent.
The history keeps the old name.

```yaml
events:
  - name: backup:photos
    command: 'rsync -a ~/Pictures/ /mnt/backup/Pictures/'
    triggers:
      - type: manual
```

## `description`

`description` is a text about the event, for people.

- **Type**: a string.
- **Default**: none.
- **Takes effect**: at once for `kickd events`, which reads the config each time it runs.

`kickd events` shows the description in its DESCRIPTION column, and `kickd events --json` in its `description` field.
The agent does not use it.

```yaml
events:
  - name: backup
    description: 'Copies ~/work to ~/backup/work every night'
    command: 'rsync -a ~/work/ ~/backup/work/'
    triggers:
      - type: manual
```

## `command`

`command` is what a run of the event executes.

- **Type**: a string, or a list of strings.
- **Default**: none. `command` is required.
- **Takes effect**: for runs that start after the file that defines the event is saved.

The two forms run in different ways:

- **A string** runs through the shell of the OS: `/bin/sh -c` on macOS and Linux, and `cmd /S /C` on Windows. Pipes, redirections, `&&` and the variables of the shell work as they do in a terminal: `$VAR` on macOS and Linux, and `%VAR%` on Windows.
- **A list** is a program and its arguments, which kickd starts directly, without a shell. kickd passes each argument as it is, so `~`, `*` and `$VAR` in the arguments stay unexpanded, and no argument needs quotes for a shell.

kickd finds the program of a list in one of three ways:

- **A name without a path**, such as `python3`: kickd looks it up in the `PATH` that the command receives, which the `env` of the event can set. On Windows, kickd also tries the extensions that `PATHEXT` lists, such as `.exe`.
- **A relative path**, such as `./deploy.sh` or `.venv/bin/python`: the path starts at the working directory of the event.
- **An absolute path**, such as `/usr/local/bin/backup`: kickd starts that file.

For a string, the shell finds the programs, in the same `PATH`.
On Windows, a batch file such as `npm.cmd` runs through cmd, so give it as a string, and start a PowerShell script with `['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'script.ps1']`.

The exit code of the command decides the status of the run: 0 records it as `succeeded`, and any other code as `failed`.
`kickd check` prints the command of each event: a string as it is, and a list in the form `['./deploy.sh', 'staging']`.

```yaml
events:
  - name: archive
    command: 'tar czf "$HOME/archive/notes-$(date +%Y%m%d).tgz" -C "$HOME" notes'
    triggers:
      - type: manual
  - name: report
    command: ['python3', 'report.py', '--daily']
    workdir: '~/reports'
    triggers:
      - type: manual
```

## `workdir`

`workdir` is the directory in which the command runs.

- **Type**: a path. A leading `~` is the home directory, `${VAR}` is the value of the environment variable `VAR`, and a relative path starts at the directory of the config file.
- **Default**: the directory of the config file.
- **Takes effect**: for runs that start after the file that defines the event is saved.

The directory must exist when the config is loaded, and a missing one is an error of the config.
The default is the same whether kickd runs in a terminal or as a service, so the directory from which the agent was started does not matter.
A program or file that the command gives with a relative path starts here.
`kickd check` prints the working directory of each event on its `workdir` line.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    triggers:
      - type: manual
```

## `env`

`env` sets environment variables for the command.

- **Type**: a mapping of variable names to strings.
- **Default**: none.
- **Takes effect**: for runs that start after the file that defines the event is saved.

A command receives four layers of environment variables, each on top of the one before:

1. **The environment of kickd**, which depends on how kickd was started: a terminal gives kickd the variables of its shell, and a service manager gives it far fewer. For an event with a [`user`](#user), `HOME`, `USER`, `LOGNAME` and `TMPDIR` are those of that user.
2. **The `env` of the config file**, which applies to every event (see [For every event](#for-every-event)).
3. **The `env` of the event**, which adds variables and replaces the variables of the layers below that have the same name.
4. **The variables of the run**, such as `KICKD_EVENT` and `KICKD_DATA_REF`, which replace the variables of the same name from the other layers.

In a value, `${NAME}` and `$NAME` are replaced by the variable `NAME` as the layers below give it, as it is when the run starts: in the `env` of an event, by the `env` of the config file or by kickd.
kickd has no way to escape `$`, so a value that must contain text such as `$word`, like a password with a `$` in it, belongs in a file that the command reads.
The config uses this form on Windows too, as in `${USERPROFILE}`; the `%USERPROFILE%` form is expanded only by cmd, inside a string command.

A `PATH` in `env` also decides where kickd finds the program of `command`, and `${PATH}` in its value is the `PATH` of kickd, so directories put in front of it are searched first.
`kickd check` prints the `PATH` that each event sets.
On Windows, the names of environment variables are not case-sensitive, so `PATH` and `Path` are the same variable.
A value is the text that the file gives, so `1` and `'1'` are the same value.
kickd does not write the values of `env` to its log.

```yaml
events:
  - name: report
    command: ['uv', 'run', 'report.py']
    workdir: '~/reports'
    env:
      PATH: '${HOME}/.local/bin:${PATH}'
      PYTHONUNBUFFERED: '1'
    triggers:
      - type: manual
```

### For every event

The `env` section of the config file, beside `log` and `database`, sets environment variables for the commands of every event.
The `env` of an event replaces a variable of the same name, and `${NAME}` in the `env` of an event refers to the value that the config file sets.
It suits variables that every command needs, such as a `PATH` with the directories of Homebrew, or the `LANG` that a service manager does not set.
The section belongs in the config file, and another file of events that has one is an error.

```yaml
env:
  PATH: '/opt/homebrew/bin:/usr/local/bin:${PATH}'
  LANG: 'en_US.UTF-8'
events:
  - name: report
    command: ['uv', 'run', 'report.py']
    workdir: '~/reports'
    env:
      PATH: '${HOME}/.local/bin:${PATH}'
    triggers:
      - type: manual
```

## `user`

`user` names the user whose rights the command runs with, on macOS and Linux, when kickd runs as root.

- **Type**: the name of a user of the machine, such as `alice`.
- **Default**: none, which runs the command as the user of the agent.
- **Takes effect**: for runs that start after the file that defines the event is saved.

Only root can start a program as another user, so `user` needs an agent that runs as root: a LaunchDaemon on macOS or a system-wide unit on Linux, which `sudo kickd service install` sets up.
In the config of any other user, and on Windows, `user` is an error of the config.

When a run starts, kickd looks up the user and starts the command with the user ID, the group and the other groups of that user.
A user that the machine does not have fails the run before the command starts, with the reason `start_failed`.
The release executables look up users without cgo: on macOS they ask the directory service of the system, and on Linux they read `/etc/passwd` and `/etc/group`, so on Linux they do not find a user that only a network directory knows.

The environment and the files of the run belong to the user:

- `HOME` is the home directory of the user, and `USER` and `LOGNAME` are its name. These replace the variables of the agent, so `${HOME}` in an [`env`](#env) value is the home directory of the user, and an `env` can still set them.
- `TMPDIR` is `/tmp`, because the temporary directory of the agent can be closed to other users.
- The payload file at `KICKD_PAYLOAD_FILE` is in `/tmp` and belongs to the user, so only the user and root can read it.

The [`stop_command`](#stop_command) runs as the same user.
The user needs access to the [`workdir`](#workdir), and a command that cannot enter it fails to start.
Paths in the config still start at the home directory of the agent, so `~` in a `workdir` is the home directory of root, and a directory of the user is written in full.
kickd itself writes the log, the [`output`](#output) file and the database as the user of the agent.
`kickd check` prints the user of an event on its `user` line.

```yaml
events:
  - name: report
    command: ['uv', 'run', 'report.py']
    workdir: '/home/alice/reports'
    env:
      PATH: '${HOME}/.local/bin:${PATH}'   # ${HOME} is /home/alice
    user: 'alice'
    triggers:
      - type: manual
```

## `timeout`

`timeout` is the longest time that the command may run.

- **Type**: a duration: a number with one of the units `ms`, `s`, `m` and `h`, such as `90s` or `1h30m`.
- **Default**: none, which sets no limit. The value `0s` also sets no limit.
- **Takes effect**: for runs that start after the file that defines the event is saved.

When the command runs longer, kickd stops it together with the processes that it started:

- **macOS and Linux**: kickd sends SIGTERM to the process group of the command, and SIGKILL to what still runs after [`stop_timeout`](#stop_timeout), 10 seconds by default.
- **Windows**: kickd ends the process tree of the command at once.

An event with a [`stop_command`](#stop_command) runs it before SIGTERM.
The run is recorded as `failed`, with the reason `timeout`.
A timeout is a failure and not an interruption, so `on_interrupt: rerun` does not run the firing again.
A negative duration is an error of the config.

```yaml
events:
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    timeout: 1h
    triggers:
      - type: manual
```

## `stop_command`

`stop_command` is what kickd runs to ask a running command to stop, before it sends SIGTERM.

- **Type**: a string or a list of strings, as for [`command`](#command): a string runs through the shell, and a list starts a program with its arguments.
- **Default**: none, which stops the command with SIGTERM alone.
- **Takes effect**: for runs that start after the file that defines the event is saved.

kickd stops a running command when its [`timeout`](#timeout) passes, when `kickd cancel` cancels the run, when a new firing replaces it under [`concurrency: replace`](#concurrency), and when kickd stops.
Some programs do not stop on SIGTERM.
For example, `container run` of Apple's `container` 1.3.0, which runs a container in the foreground, cannot pass SIGTERM on to the container, and the container keeps running after its client has been killed.
For such programs, `stop_command` asks the program itself to stop:

1. kickd runs `stop_command` in the working directory and with the environment of the run, and waits up to [`stop_timeout`](#stop_timeout) for it.
   `KICKD_STOP_REASON` tells it why the run stops: `timeout`, `canceled`, `replaced` or `shutdown`.
2. kickd sends SIGTERM to the process group of the command, on Windows ends its process tree, and waits up to `stop_timeout` again.
3. kickd kills what still runs, as it does for a timeout.

A stop command that fails, or runs past `stop_timeout`, is logged as `Stop command failed`, and kickd goes on with SIGTERM.
The run keeps the status of the reason that stopped it, such as `failed` with the reason `timeout`.

```yaml
events:
  - name: web
    command: ['container', 'run', '--name', 'web', '--rm', 'web:latest']
    stop_command: ['container', 'stop', 'web']
    triggers:
      - type: startup
```

## `stop_timeout`

`stop_timeout` is how long kickd waits for a stopped command to exit before it kills it.

- **Type**: a duration, such as `30s`.
- **Default**: `10s`.
- **Takes effect**: for runs that start after the file that defines the event is saved.

kickd waits up to `stop_timeout` for the [`stop_command`](#stop_command), when the event has one, and up to `stop_timeout` again after SIGTERM.
When kickd runs as a service, the service manager gives it a limited time to stop: 20 seconds by default for launchd and 90 seconds for systemd, after which they kill it.
On Windows, kickd stops waiting for its commands after 30 seconds.
Keep `stop_timeout`, twice over for an event with a `stop_command`, short enough that the commands stop within that time when kickd stops.
A negative duration is an error of the config.

```yaml
events:
  - name: db
    command: ['postgres', '-D', '/usr/local/var/postgres']
    stop_timeout: 30s
    triggers:
      - type: startup
```

## `concurrency`

`concurrency` decides what happens when the event fires while a run of it is running.

- **Type**: one of `skip`, `queue`, `parallel` and `replace`.
- **Default**: `skip`.
- **Takes effect**: for the firings that the agent handles after the file that defines the event is saved.

The setting applies among the runs of one event, and runs of different events never wait for each other:

- **skip**: the new firing is recorded as `skipped`, with the reason `already_running`, and its command does not start. The running run counts the firings skipped while it ran, and `kickd show` displays the count. Firings that waited while the agent was stopped are handled in the same way: the first one runs, and the others are skipped.
- **queue**: the new firing waits in the queue, and waiting runs start one at a time, in the order of their firings. Up to 1000 runs of one event can wait. A firing beyond them is recorded as `dropped`: `kickd event` then exits with code 1, and a webhook request gets 409.
- **parallel**: the new firing starts at once, alongside the running ones, with no limit on their number.
- **replace**: kickd stops the running run, as `kickd cancel` does and with the event's [`stop_command`](#stop_command), and starts the new firing when it has stopped. The stopped run is recorded as `canceled`, with the reason `replaced`. When several firings wait for the stop, only the newest starts, and the others are recorded as `skipped`, with the reason `replaced`.

`skip` suits work of which one run covers the firings that came during it, such as a sync.
`queue` suits work that must handle every firing in order, such as builds and deploys.
`parallel` suits firings that do not depend on each other, such as notifications.
`replace` suits a long-running program that a new firing restarts, such as a server after a deploy.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    concurrency: queue
    triggers:
      - type: manual
```

## `restart`

`restart` decides whether kickd starts the event again when a run of it ends.

- **Type**: one of `never`, `on-failure` and `always`.
- **Default**: `never`.
- **Takes effect**: for the runs that end after the file that defines the event is saved.

The values mean:

- **never**: only the triggers of the event start it.
- **on-failure**: a run that failed, with a non-zero exit code, a timeout or a failure to start, starts the event again.
- **always**: a run that succeeded or failed starts the event again.

A restart is a firing with the trigger `restart`, and its run waits in the queue for the restart delay before it starts.
The first restart waits [`restart_delay`](#restart_delay), and each restart after a run shorter than [`restart_max_delay`](#restart_max_delay) waits twice as long as the one before, up to `restart_max_delay`, so that a command that fails at once does not run in a tight loop.
A run that lasts `restart_max_delay` or longer starts the waits again from `restart_delay`.
`kickd history` shows when a waiting restart starts, and `kickd cancel` cancels it like any queued run.

kickd does not restart a run that `kickd cancel` canceled, that a new firing replaced under [`concurrency: replace`](#concurrency), or that kickd stopping cut off: the [`on_interrupt`](#on_interrupt) setting handles that one, and a startup trigger starts the event when kickd starts again.
A restart that waits when its event no longer restarts, because the config changed, is recorded as `dropped`, with the reason `restart_disabled`.

`restart: always` with a startup trigger keeps a long-running program, such as a server, running while kickd runs:

```yaml
events:
  - name: web
    command: ['container', 'run', '--name', 'web', '--rm', 'web:latest']
    stop_command: ['container', 'stop', 'web']
    restart: always
    triggers:
      - type: startup
```

## `restart_delay`

`restart_delay` is how long the first restart of an event waits before it starts.

- **Type**: a duration, such as `500ms` or `5s`.
- **Default**: `1s`. The value `0s` also gives `1s`.
- **Takes effect**: for the runs that end after the file that defines the event is saved.

Each restart that follows a run shorter than [`restart_max_delay`](#restart_max_delay) waits twice as long as the one before.
A negative duration is an error of the config.

## `restart_max_delay`

`restart_max_delay` is the longest wait before a restart.

- **Type**: a duration, such as `5m`.
- **Default**: `1m`, or `restart_delay` when that is longer.
- **Takes effect**: for the runs that end after the file that defines the event is saved.

It is also how long a run must last for the next restart to wait only [`restart_delay`](#restart_delay) again.
A value shorter than `restart_delay` is an error of the config.

## `on_interrupt`

`on_interrupt` decides what happens to a run that a stop or a crash cut off.

- **Type**: one of `abandon` and `rerun`.
- **Default**: `abandon`.
- **Takes effect**: at the next start of the agent, which handles the runs that were cut off when it starts, with the value that the config has then.

A run is cut off, or **interrupted**, when its command is running as the agent stops or crashes, or as the machine shuts down or loses power.
At a clean stop, the agent stops the commands as a timeout does, and records their runs as `interrupted`.
After a crash, the runs stay `running` in the database, and the next start of the agent treats them as interrupted.

- **abandon**: the run is recorded as `abandoned`, with the reason `agent_stopped` after a clean stop and `agent_crashed` after a crash, and it does not run again.
- **rerun**: the same firing runs again from the start, as a new run with the same request ID, the same parameters, and a `KICKD_ATTEMPT` one higher. The interrupted run is recorded as `retried`.

`rerun` suits commands that give the same result when they run twice, such as rsync or make.
For work that must not happen twice, such as sending an email, keep `abandon`.
An interrupted run of an event that the config no longer defines is recorded as `abandoned`, with the reason `event_removed`.

```yaml
events:
  - name: sync
    command: 'rsync -a ~/work/ /mnt/backup/work/'
    on_interrupt: rerun
    triggers:
      - type: manual
```

## `max_attempts`

`max_attempts` is how many times one firing may run under `on_interrupt: rerun`, counting the first run.

- **Type**: a whole number, 1 or more, written without quotes.
- **Default**: `3`.
- **Takes effect**: at the next start of the agent, which handles the runs that were cut off when it starts, with the value that the config has then.

When a run that is already the last allowed attempt is cut off, the agent records it as `abandoned`, with the reason `max_attempts_reached`, and logs an ERROR record.
With `on_interrupt: abandon`, an interrupted run never runs again, and `max_attempts` has no effect.
A number below 1 is an error of the config.

```yaml
events:
  - name: sync
    command: 'rsync -a ~/work/ /mnt/backup/work/'
    on_interrupt: rerun
    max_attempts: 5
    triggers:
      - type: manual
```

## `stdin`

`stdin` decides what the command reads on standard input.

- **Type**: one of `none` and `payload`.
- **Default**: `none`.
- **Takes effect**: for runs that start after the file that defines the event is saved.

Every run has a **payload**, a JSON object that describes the run: the event, the trigger, the parameters, and what fired it, such as the changed files or the webhook request.
The command can always read the payload from the file named by the variable `KICKD_PAYLOAD_FILE`.

- **none**: standard input is empty, so a read gets the end of the input at once.
- **payload**: standard input carries the payload JSON, the same text as the file.

`payload` suits programs that read JSON on standard input, such as jq.
[What a command receives](/kickd/reference/payload/) lists the fields of the payload.

```yaml
events:
  - name: show-ref
    command: ['jq', '-r', '.data.ref']
    stdin: payload
    params:
      - name: ref
        default: main
    triggers:
      - type: manual
```

## `log_output`

`log_output` decides whether kickd keeps the output of the command.

- **Type**: `true` or `false`, written without quotes.
- **Default**: `true`.
- **Takes effect**: for runs that start after the file that defines the event is saved.

With `true`, kickd keeps the output in three places:

- **The log**: each line of output as a `Run output` record at the DEBUG level, which the default level `info` leaves out.
- **The record of a failed run**: the last lines of standard error, up to 20 lines and 4 KB, in the `stderrTail` key.
- **The database**: the first 64 KB of the output, stored with the run, which `kickd show` displays.

With `false`, kickd keeps the output in none of them, and a webhook trigger with `wait: true` still returns the output in its response.
In the log, kickd replaces values that look like passwords or tokens with `***masked***`, but it cannot recognize every form of secret, and the output stored in the database stays as the command printed it.
So `false` suits commands that print secrets, such as temporary credentials:

```yaml
events:
  - name: session-token
    command: ['aws', 'sts', 'get-session-token']
    log_output: false
    triggers:
      - type: manual
```

## `output`

`output` is a file to which kickd appends the output of the command.

- **Type**: a mapping with `path`, and optionally `max_size_mb` and `max_backups`.
- **Default**: none, which keeps the output only as [`log_output`](#log_output) says.
- **Takes effect**: for runs that start after the file that defines the event is saved.

[`log_output`](#log_output) logs each line of output at the DEBUG level and stores the first 64 KB with the run, which suits short commands.
The output of a long-running program, such as the access log of a server, needs a file of its own: kickd appends each line of standard output and standard error to `path`, whole lines at a time, whether `log_output` is `true` or `false`.

- **`path`**: the file. A relative path starts at the directory of the config file, and kickd creates the missing directories.
- **`max_size_mb`**: the size in MB past which kickd renames the file with `.1` appended and starts a new one, as it does for its log file. The default is `10`.
- **`max_backups`**: how many renamed files to keep. The default is `5`.

Runs that write to the same file at the same time share it, so their lines do not mix within a line.
kickd creates the file readable and writable by the user of the agent alone, as it creates the database, which also keeps output, and it takes these permissions from an existing file that has more.
A file that kickd cannot open is logged as `Output file unavailable`, and the command runs without it.

```yaml
events:
  - name: web
    command: ['./server']
    workdir: '~/web'
    output:
      path: ~/Library/Logs/web.log
      max_size_mb: 20
    triggers:
      - type: startup
```

## `params`

`params` declares the **parameters** of the event: named values that a firing passes to the command.

- **Type**: a list of parameters, each with a `name` and, optionally, `required`, `default` and `description`.
- **Default**: none.
- **Takes effect**: at once for `kickd event`, which reads the config each time it runs, and for webhook requests when the file that defines the event is saved.

The command receives each parameter in a variable named after it, `KICKD_DATA_` and the name in capitals, so `ref` arrives in `KICKD_DATA_REF`.
`kickd event` passes parameters as `KEY=VALUE` arguments, and a webhook request as query values.
Without `params`, `kickd event` accepts any names made of letters, digits and `_`, and webhook requests pass no parameters.
[Parameter settings](/kickd/settings/params/) describes each key of a parameter.

```yaml
events:
  - name: deploy
    command: 'git -C ~/app fetch && git -C ~/app checkout "$KICKD_DATA_REF"'
    params:
      - name: ref
        default: main
    triggers:
      - type: manual
```

## `triggers`

`triggers` lists the ways in which the event fires.

- **Type**: a list of triggers, each a mapping with a `type`.
- **Default**: none. `triggers` is required, with at least one trigger.
- **Takes effect**: when the file that defines the event is saved, because the agent then stops its triggers and starts them again from the new config.

An event fires only through the triggers that it lists, and it can list several of them.
A trigger has one of seven types, `manual`, `cron`, `webhook`, `file`, `after`, `startup` and `wake`, and takes only the keys of its type.
`- type: manual` lets `kickd event NAME` fire the event, and an event without it cannot be fired by hand.
An event lists at most one manual, one startup and one wake trigger.
[Trigger settings](/kickd/settings/triggers/) describes each type and its keys.

```yaml
events:
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    triggers:
      - type: cron
        schedule: '0 3 * * *'
        timezone: Asia/Tokyo
      - type: manual
```
