# Event settings

[Documentation index](../../README.md#documentation)

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

A command receives three layers of environment variables, each on top of the one before:

1. **The environment of kickd**, which depends on how kickd was started: a terminal gives kickd the variables of its shell, and a service manager gives it far fewer.
2. **The `env` of the event**, which adds variables and replaces the variables of kickd that have the same name.
3. **The variables of the run**, such as `KICKD_EVENT` and `KICKD_DATA_REF`, which replace the variables of the same name from the other layers.

In a value, `${NAME}` and `$NAME` are replaced by the variable `NAME` of kickd, as it is when the run starts.
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

## `timeout`

`timeout` is the longest time that the command may run.

- **Type**: a duration: a number with one of the units `ms`, `s`, `m` and `h`, such as `90s` or `1h30m`.
- **Default**: none, which sets no limit. The value `0s` also sets no limit.
- **Takes effect**: for runs that start after the file that defines the event is saved.

When the command runs longer, kickd stops it together with the processes that it started:

- **macOS and Linux**: kickd sends SIGTERM to the process group of the command, and SIGKILL 10 seconds later to what still runs.
- **Windows**: kickd ends the process tree of the command at once.

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

## `concurrency`

`concurrency` decides what happens when the event fires while a run of it is running.

- **Type**: one of `skip`, `queue` and `parallel`.
- **Default**: `skip`.
- **Takes effect**: for the firings that the agent handles after the file that defines the event is saved.

The setting applies among the runs of one event, and runs of different events never wait for each other:

- **skip**: the new firing is recorded as `skipped`, with the reason `already_running`, and its command does not start. The running run counts the firings skipped while it ran, and `kickd show` displays the count. Firings that waited while the agent was stopped are handled in the same way: the first one runs, and the others are skipped.
- **queue**: the new firing waits in the queue, and waiting runs start one at a time, in the order of their firings. Up to 1000 runs of one event can wait. A firing beyond them is recorded as `dropped`: `kickd event` then exits with code 1, and a webhook request gets 409.
- **parallel**: the new firing starts at once, alongside the running ones, with no limit on their number.

`skip` suits work of which one run covers the firings that came during it, such as a sync.
`queue` suits work that must handle every firing in order, such as builds and deploys.
`parallel` suits firings that do not depend on each other, such as notifications.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    concurrency: queue
    triggers:
      - type: manual
```

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
[What a command receives](../payload.md) lists the fields of the payload.

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

## `params`

`params` declares the **parameters** of the event: named values that a firing passes to the command.

- **Type**: a list of parameters, each with a `name` and, optionally, `required`, `default` and `description`.
- **Default**: none.
- **Takes effect**: at once for `kickd event`, which reads the config each time it runs, and for webhook requests when the file that defines the event is saved.

The command receives each parameter in a variable named after it, `KICKD_DATA_` and the name in capitals, so `ref` arrives in `KICKD_DATA_REF`.
`kickd event` passes parameters as `KEY=VALUE` arguments, and a webhook request as query values.
Without `params`, `kickd event` accepts any names made of letters, digits and `_`, and webhook requests pass no parameters.
[Parameter settings](params.md) describes each key of a parameter.

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
[Trigger settings](triggers.md) describes each type and its keys.

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
