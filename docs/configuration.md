# Configuration

[Documentation index](../README.md#documentation)

kickd reads one YAML config file.
The file defines **events**, which are named commands.
An event can list **triggers**, which fire the event automatically: a cron schedule, a webhook, or changes in a directory.
Every event can also be fired from the command line with `kickd event NAME`.
Each firing is recorded as a **run** in the **queue**, a SQLite database, and the long-running kickd process, the **agent**, starts the event's command for each run.

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

## Defining events

Events are listed under `events`.
This config defines two events:

```yaml
events:
  - name: hello
    shell: "echo hello from kickd"

  - name: archive
    shell: 'tar czf "$HOME/backup/notes-$(date +%Y%m%d).tgz" -C "$HOME" notes'
    triggers:
      - type: cron
        schedule: "30 3 * * *"
        timezone: Asia/Tokyo
```

`hello` has no triggers, so it runs only when fired with `kickd event hello`.
`archive` fires every day at 3:30 Tokyo time from its cron trigger, and also with `kickd event archive`.

An event gives its command in one of two ways:

- **shell**: a string run by the system shell, `/bin/sh -c` on macOS and Linux and `cmd /S /C` on Windows. Pipes, redirections and variables work as in the shell.
- **command**: a list of the program and its arguments, started directly without a shell. kickd passes every element as it is, so `~`, `*` and `$VAR` stay unexpanded.

Without a `log` section, kickd writes its log to standard error: text on a terminal, JSON when standard error goes to a pipe or a file.
Each run logs a start record and a completion record at INFO.
The output of the command is logged at DEBUG, so `LOG_LEVEL=debug kickd run` shows it.

## Writing values

- **Durations**: strings such as `"30s"`, `"5m"` and `"1h"`. A bare number such as `30` is an error.
- **Paths**: in `log.file`, `queue.path`, `workdir` and the `path` of file triggers, a leading `~` expands to the home directory, and `${VAR}` expands to the value of the environment variable `VAR` of kickd. Relative paths are relative to the directory of the config file.
- **Environment variables on Windows**: the config file uses the `${USERPROFILE}` form on Windows too. The `%USERPROFILE%` form is expanded only by cmd, inside a `shell` string.
- **Key names**: an unknown key is an error, so `kickd check` finds misspelled keys.

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
A change to `queue.path` takes effect only when the agent restarts.

## Differences between operating systems

| Item | macOS and Linux | Windows |
|---|---|---|
| Shell that runs `shell` | `/bin/sh -c` | `cmd /S /C` |
| Environment variables inside `shell` | `$KICKD_DATA_REF` | `%KICKD_DATA_REF%` |
| Separator in `KICKD_FILE_PATHS`, the list of changed files | `:` | `;` |
| Example paths | `~/Inbox`, `/srv/data` | `'C:\Data\Inbox'`, `C:/Data/Inbox` |

To run PowerShell on Windows, start `powershell` with `command`.

## Example: move PDFs that arrive in a folder

This event moves PDFs that arrive in `~/Inbox` to `~/Archive`.
It is written for a kickd that runs as the user: a LaunchAgent on macOS, or a per-user systemd unit on Linux.

```yaml
events:
  - name: archive-pdf
    shell: 'for f in "$HOME"/Inbox/*.pdf; do [ -e "$f" ] || continue; mv "$f" "$HOME"/Archive/; done'
    triggers:
      - type: file
        path: ~/Inbox
        include: ["*.pdf"]
        changes: [create, write]
        debounce: 3s
```

- `include` limits the trigger to files whose names match a pattern, here PDFs.
- `changes` lists the kinds of change that fire the event.
- `debounce` waits until no new change has arrived for the given time, then fires once.

While a file is being copied, `write` changes keep arriving, so this trigger fires once, 3 seconds after the copy finishes.
Files that arrive during the wait are combined into that one firing.
For this reason, the script handles every PDF in the folder, whatever the firing lists.

Moving a file out of `~/Inbox` causes a `rename` or `remove` change there.
`changes` lists only `create` and `write`, so these changes do not fire the event.

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
        schedule: "30 3 * * *"
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
  listen: "127.0.0.1:8787"
events:
  - name: deploy
    command: ["/home/me/app/deploy.sh"]
    workdir: /home/me/app
    concurrency: queue
    params:
      - name: ref
        default: main
    triggers:
      - type: webhook
        path: /hooks/deploy
        methods: [POST]
        token: "replace-with-a-long-random-string"
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

This event runs a PowerShell script when a CSV file arrives in `C:\Data\Inbox`.

```yaml
log:
  file: 'C:\ProgramData\kickd\kickd.log'
events:
  - name: import-csv
    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'C:\Scripts\import.ps1']
    workdir: 'C:\Data'
    triggers:
      - type: file
        path: 'C:\Data\Inbox'
        include: ['*.csv']
        changes: [create, write]
        debounce: 3s
```

With `changes: [create, write]` and `debounce: 3s`, the event fires once, 3 seconds after the file stops being written.
The script reads the path of the changed file from `$env:KICKD_FILE_PATH`.
`-ExecutionPolicy Bypass` allows the unsigned script for this one invocation only.
