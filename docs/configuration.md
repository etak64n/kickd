# Configuration

[Documentation index](../README.md#documentation)

kickd reads one YAML config file.
The file defines **events**, which are named commands.
An event can list **triggers**, which fire the event automatically: a cron schedule, a webhook, or changes in a directory.
Every event can also be fired from the command line with `kickd event NAME`.
Each firing is recorded as a **run** in the **database**, a SQLite file, and the long-running kickd process, the **agent**, starts the event's command for each run.

## Where kickd looks for the config file

kickd uses the first of these that applies:

1. The file given with `-c` or `--config`
2. The file named by the environment variable `KICKD_CONFIG`
3. `kickd/config.yaml` in the user's config directory, if it exists
4. `kickd.yaml` in the current directory, if it exists

The user's config directory depends on the OS.
Without `-c`, `kickd init` writes an example config there:

| OS | Location |
|---|---|
| macOS | `~/Library/Application Support/kickd/config.yaml` |
| Linux | `~/.config/kickd/config.yaml`, or `$XDG_CONFIG_HOME/kickd/config.yaml` when `XDG_CONFIG_HOME` is set |
| Windows | `%AppData%\kickd\config.yaml` |

A kickd installed as a service reads the config file whose absolute path was recorded when the service was installed.
After moving the config file, uninstall the service and install it again.

## Where the log and the database go

kickd writes two files of its own: the log, and the **database**, a SQLite file that records every run.
The `base_dir` section gives a directory for each OS, and a relative `log.path` or `database.path` starts at the directory for the OS that kickd runs on:

```yaml
base_dir:
  macos: '~/Library/Application Support/kickd'
  linux: '~/.local/state/kickd'
  windows: '~\AppData\Local\kickd'
log:
  path: 'kickd.log'
database:
  path: 'kickd.db'
events:
  - name: hello
    shell: 'echo hello from kickd'
```

On a Mac, this file puts the log at `~/Library/Application Support/kickd/kickd.log` and the database next to it.
Without a `base_dir` entry for the OS, the two files start at the directory of the config file, as every other relative path in the file does.
Without `log.path`, kickd writes its log to standard error, and without `database.path`, the database is `kickd.db`.

`kickd init` writes the `base_dir` entry of the OS that it runs on, with the usual place of that OS.
A config file inside the home directory is taken for a user's kickd, and one outside it, such as `/etc/kickd/config.yaml`, for a service of the whole system:

| OS | For a user | For the whole system |
|---|---|---|
| macOS | `~/Library/Application Support/kickd` | `/Library/Application Support/kickd` |
| Linux | `~/.local/state/kickd` | `/var/lib/kickd` |
| Windows | `~\AppData\Local\kickd` | `C:\ProgramData\kickd` |

`kickd check` prints the resolved paths of the log and the database.

## One config file for each OS

The events of a config file are written for one OS.
The shell differs, `/bin/sh` on macOS and Linux and cmd on Windows, and so do the programs that commands call and the absolute paths of folders.
kickd does not translate commands between operating systems, so each OS gets a config file of its own.
`kickd init` writes an example for the OS that it runs on: with shell commands on macOS and Linux, and with PowerShell scripts on Windows.

To keep the files for several machines in one place, such as a repository of dotfiles, name them after their OS and give the one for the machine with `-c` or `KICKD_CONFIG`:

```sh
kickd run -c ~/dotfiles/kickd/kickd.macos.yaml
```

A few things already work the same on every OS:

- A leading `~` is the home directory, and `/` separates folders on Windows too.
- `${VAR}` in a path expands to the environment variable `VAR`.
- `command` starts a program without a shell, so its arguments need no quoting for sh or cmd.

A `base_dir` entry for another OS has no effect, so a file copied to another OS keeps its log and database next to the config file instead of in a folder of the wrong OS.

## Defining events

Events are listed under `events`.
This config defines two events:

```yaml
events:
  - name: hello
    shell: 'echo hello from kickd'

  - name: archive
    shell: 'tar czf "$HOME/backup/notes-$(date +%Y%m%d).tgz" -C "$HOME" notes'
    triggers:
      - type: cron
        schedule: '30 3 * * *'
        timezone: Asia/Tokyo
```

`hello` has no triggers, so it runs only when fired with `kickd event hello`.
`archive` fires every day at 3:30 Tokyo time from its cron trigger, and also with `kickd event archive`.

An event gives its command in one of two ways:

- **shell**: a string run by the system shell, `/bin/sh -c` on macOS and Linux and `cmd /S /C` on Windows. Pipes, redirections and variables work as in the shell.
- **command**: a list of the program and its arguments, started directly without a shell. kickd passes every element as it is, so `~`, `*` and `$VAR` stay unexpanded.

Without `log.path`, kickd writes its log to standard error: text on a terminal, JSON when standard error goes to a pipe or a file.
Each run logs a start record and a completion record at INFO.
The output of the command is logged at DEBUG, so `LOG_LEVEL=debug kickd run` shows it.

## Writing values

- **Durations**: values such as `30s`, `5m` and `1h`. A bare number such as `30` is an error.
- **Paths**: a leading `~` expands to the home directory, and `${VAR}` expands to the value of the environment variable `VAR` of kickd. A relative `log.path` or `database.path` starts at the `base_dir` of the OS, and every other relative path, such as a `workdir` or the `path` of a file trigger, starts at the directory of the config file.
- **Quotes**: the examples put strings in single quotes, which keep backslashes and double quotes as they are, so Windows paths and shell commands need no escapes. Double quotes appear only inside shell commands, where the shell reads them. Inside single quotes, a single quote is written twice, as in `'it''s'`.
- **Environment variables on Windows**: the config file uses the `${USERPROFILE}` form on Windows too. The `%USERPROFILE%` form is expanded only by cmd, inside a `shell` string.
- **Key names**: an unknown key is an error, so `kickd check` finds misspelled keys.

## Where commands run

Each event sets where its command runs and where its programs are found:

```yaml
events:
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
  - name: convert
    command: ['ffmpeg', '-version']
    env:
      PATH: '/opt/homebrew/bin:${PATH}'
```

- **Working directory**: `workdir` is the directory in which the command runs. Without it, the command runs in the directory of the config file, whether kickd runs as a service or in a terminal. A program or script given with a relative path, such as `./deploy.sh`, starts at the working directory.
- **Programs**: a program name without a path, such as `ffmpeg`, is looked up in the `PATH` of the command's environment, for `command` as for `shell`. `env` sets that `PATH` for each event, and `${PATH}` in the value is the `PATH` of kickd.

A service starts kickd with a shorter `PATH` than a terminal has.
Under launchd on macOS, it is `/usr/bin:/bin:/usr/sbin:/sbin`.
systemd gives units `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin`, and a Windows service sees only the system `PATH`.
`kickd check` prints the working directory of each event, and the `PATH` that an event sets.

## Applying changes

The agent watches the directory of its config file.
When the config file is saved, the agent waits 0.5 seconds and reloads it.
On macOS and Linux, SIGHUP also makes the agent reload.

When the new config is valid, the agent rebuilds its triggers.
Commands that are running continue to the end.
Runs waiting in the queue stay there and run with the new config.
Waiting runs of events that the new config no longer defines are recorded as `dropped`.

When the new config has errors, the agent logs them and keeps running with the previous config.
It switches to the new config when a valid version is saved.
A change to where the database goes, through `database.path` or `base_dir`, takes effect only when the agent restarts.

## Differences between operating systems

| Item | macOS and Linux | Windows |
|---|---|---|
| Shell that runs `shell` | `/bin/sh -c` | `cmd /S /C` |
| Environment variables inside `shell` | `$KICKD_DATA_REF` | `%KICKD_DATA_REF%` |
| Separator in `KICKD_FILE_PATHS`, the list of changed files | `:` | `;` |
| Example paths | `~/project`, `/srv/data` | `'C:\Data\Import'`, `C:/Data/Import` |

To run PowerShell on Windows, start `powershell` with `command`.

## Example: rebuild when source files change

This event runs `make build` in `~/project` when files in `~/project/src` or its subdirectories change.
It is written for a kickd that runs as the user: a LaunchAgent on macOS, or a per-user systemd unit on Linux.

```yaml
events:
  - name: rebuild
    command: [make, build]
    workdir: ~/project
    concurrency: queue
    triggers:
      - type: file
        path: ~/project/src
        recursive: true
        exclude: ['*.swp', '*~']
        debounce: 2s
```

- `recursive` also watches the subdirectories of `path`, including ones created later.
- `exclude` leaves out paths that match a pattern, here the temporary files that editors such as Vim and Emacs write.
- `debounce` waits until no new change has arrived for the given time, then fires once.

Saving several files, or switching branches with Git, causes a burst of changes, so this trigger fires once, 2 seconds after the burst ends.
All changes during the wait are combined into that one firing.

With `concurrency: queue`, a firing that arrives during a build waits and runs after it, so the last change is always built.
The trigger watches only `src`, so the files that the build writes outside it do not fire the event again.

## Example: a nightly backup that reruns after an interruption

This event backs up a folder every day at 3:30 Tokyo time.
If kickd or the machine stops during the backup, the backup starts over when kickd starts again.

```yaml
events:
  - name: nightly-backup
    shell: 'rsync -a "$HOME/notes/" "$HOME/backup/notes/"'
    timeout: 30m
    on_interrupt: rerun
    max_attempts: 3
    triggers:
      - type: cron
        schedule: '30 3 * * *'
        timezone: Asia/Tokyo
```

- `schedule` is a cron expression with five fields: minute, hour, day of month, month and day of week.
- `timezone` makes kickd read the cron expression in that time zone.
- `timeout` stops the command when it runs longer than 30 minutes.
- `on_interrupt: rerun` starts a run again when a stop or crash cut it off, and `max_attempts` limits how many times one firing runs, counting the first run.

If the machine sleeps, or kickd is stopped, at 3:30, the backup runs once as soon as the machine wakes or kickd starts, because the `missed` key of cron triggers defaults to `run`.
With `missed: skip`, the backup waits for the next night instead.

rsync gives the same result when it runs twice, which makes it safe to rerun.

## Example: a deploy fired by a webhook or by hand

This event fires on a POST to `/hooks/deploy` and on `kickd event deploy`.

```yaml
webhook:
  listen: '127.0.0.1:8787'
events:
  - name: deploy
    command: ['/home/me/app/deploy.sh']
    workdir: /home/me/app
    concurrency: queue
    params:
      - name: ref
        default: main
    triggers:
      - type: webhook
        path: /hooks/deploy
        methods: [POST]
        token: 'replace-with-a-long-random-string'
        wait: true
```

`params` declares the values that a firing can pass, called **parameters**.
The command receives `ref` in the environment variable `KICKD_DATA_REF`.
From the command line, the parameter is passed as `kickd event deploy ref=v1.2`.
Through the webhook, it is passed in the query:

```sh
curl -X POST -H "Authorization: Bearer <token>" "http://127.0.0.1:8787/hooks/deploy?ref=v1.2"
```

A command such as `openssl rand -hex 32` generates a suitable token.
With `wait: true`, the webhook responds after the run finishes, with the exit code and the output as JSON.
With `concurrency: queue`, firings that arrive while a deploy is running wait, and run one at a time in the order they arrived.

## Example: a PowerShell script on Windows

This event runs a PowerShell script when a CSV file arrives in `C:\Data\Import`.

```yaml
log:
  path: 'C:\ProgramData\kickd\kickd.log'
events:
  - name: import-csv
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'C:\Scripts\import.ps1']
    workdir: 'C:\Data'
    triggers:
      - type: file
        path: 'C:\Data\Import'
        include: ['*.csv']
        changes: [create, write]
        debounce: 3s
```

With `changes: [create, write]` and `debounce: 3s`, the event fires once, 3 seconds after the file stops being written.
The script reads the path of the changed file from `$env:KICKD_FILE_PATH`.
`-ExecutionPolicy Bypass` allows the unsigned script for this one invocation only.
