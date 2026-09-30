# Log settings

[Documentation index](../index.md)

The long-running kickd process, called the **agent**, writes a **log**: one record for each thing that it does, such as loading its config, starting a command, or receiving a webhook request.
The `log` section of the config file sets where the agent writes its records, which records it writes, and in what format:

```yaml
log:
  path: '~/.kickd/kickd.log'
  level: info
  format: auto
  max_size_mb: 10
  max_backups: 5
```

The agent opens its log once, when it starts, and keeps it until it stops.
A change to the `log` section therefore takes effect at the next start of the agent, and saving the config file while the agent runs leaves the log as it is.
[Logging](../logging.md) describes the records themselves: their keys, their messages and their two formats.

## `log.path`

`log.path` is the file that the agent writes its records to.

- **Type**: a path. A leading `~` is the home directory, `${VAR}` is the value of the environment variable `VAR`, and a relative path starts at the directory of the config file.
- **Default**: none. Without `log.path`, the agent writes its records to standard error.
- **Takes effect**: at the next start of the agent.

The agent creates the file, and the directories on its path, when they do not exist.
On macOS and Linux, the file gets the permissions that the umask allows, usually 0644, so the other users of the machine can read it.
When `kickd run` runs in a terminal, the agent writes each record to the file and also shows it on the screen as text.
`kickd check` prints the resolved path in its `log:` line.

Without `log.path`, the records go wherever the standard error of the agent goes:

- **A terminal**: `kickd run` shows them on the screen.
- **launchd on macOS**: a LaunchAgent writes them to `~/kickd.err.log`, and a LaunchDaemon to `/var/log/kickd.err.log`.
- **systemd on Linux**: journald collects them, and `journalctl -u kickd` reads them.
- **A Windows service**: a Windows service has no standard error, so without `log.path` its records are lost.

`kickd init` writes a `log.path` at the usual place of the OS.
A config file inside the home directory is for a user's kickd, and one outside it is for a service of the whole system:

| Config file | macOS | Linux | Windows |
|---|---|---|---|
| Inside the home directory | `~/.kickd/kickd.log` | `~/.kickd/kickd.log` | `~\.kickd\kickd.log` |
| Outside the home directory | `/Library/Logs/kickd/kickd.log` | `/var/log/kickd/kickd.log` | `C:\ProgramData\kickd\kickd.log` |

```yaml
log:
  path: 'C:\ProgramData\kickd\kickd.log'
```

## `log.level`

`log.level` sets the least severe level of the records that the agent writes.

- **Type**: one of `trace`, `debug`, `info`, `warn`, `error` and `fatal`, in upper or lower case.
- **Default**: `info`.
- **Takes effect**: at the next start of the agent.

Every record has one of six levels.
From the least severe to the most severe, they are:

| Level | Records at this level |
|---|---|
| `trace` | Each file change that a file trigger sees, and each firing of a cron schedule |
| `debug` | Each line of the output of commands, the command line of each run, and each run added to the queue |
| `info` | The start of each run and the end of each run that succeeds, config loads, the start of each trigger, and each webhook request |
| `warn` | Runs that were skipped, dropped, interrupted or canceled, failed webhook authentication, and problems that the agent works around |
| `error` | Runs that failed, config reloads that failed, and failures to read or write the database |
| `fatal` | The last record of an agent that cannot start |

A level writes its own records and the records of every more severe level, so `info` writes INFO, WARN, ERROR and FATAL records.

The environment variable `LOG_LEVEL` takes precedence over `log.level`.
`kickd check` then shows where the level came from, as in `level=debug (from LOG_LEVEL)`.
A value that is not one of the six levels is an error of the config file, whether it comes from `log.level` or from `LOG_LEVEL`.

```sh
LOG_LEVEL=debug kickd run
```

## `log.format`

`log.format` sets how the agent writes each record.

- **Type**: one of `auto`, `json` and `text`.
- **Default**: `auto`.
- **Takes effect**: at the next start of the agent.

kickd writes records in two formats:

- **JSON**: one JSON object per line. It suits log files and programs that read the log.
- **Text**: five columns separated by tabs: the time, the request ID, the level, the message, and the other keys as one JSON object, or `-` when the record has no other keys. It suits people who read a terminal.

`auto` picks the format by where the records go.
The log file of `log.path` gets JSON.
Standard error gets text when it is a terminal, and JSON when it is a pipe or a file, such as the journal of systemd.

When `kickd run` runs in a terminal with `log.path` set, the screen gets text whatever `log.format` says, and `log.format` applies to the file.
Text on a terminal has a color for each level, and the environment variable `NO_COLOR` turns the colors off.

The environment variable `LOG_FORMAT` takes precedence over `log.format`.

A systemd unit without `log.path` sends its records to journald as JSON.
`format: text` makes `journalctl -u kickd` show them as text:

```yaml
log:
  format: text
```

## `log.max_size_mb`

`log.max_size_mb` is the size at which the agent starts a new log file.

- **Type**: a whole number of megabytes, written without quotes, where a megabyte is 1,048,576 bytes.
- **Default**: `10`. The value `0` also means 10.
- **Takes effect**: at the next start of the agent.

When the file of `log.path` grows past this size, the agent renames it with `.1` appended and starts a new, empty file.
A file that already had `.1` becomes `.2`, `.2` becomes `.3`, and so on, so `.1` is always the newest of the renamed files.
If renaming fails, the agent keeps writing to the same file and reports the failure on standard error.
Without `log.path`, the agent renames nothing, and this key has no effect.
A negative number is an error of the config file.

```yaml
log:
  path: '/var/log/kickd/kickd.log'
  max_size_mb: 50
```

## `log.max_backups`

`log.max_backups` is how many renamed log files the agent keeps.

- **Type**: a whole number, written without quotes.
- **Default**: `5`. The value `0` also means 5.
- **Takes effect**: at the next start of the agent.

When a rename would make a file older than this number, the agent deletes that file.
With the defaults, the log takes at most about 60 MB: the current file and 5 renamed files of about 10 MB each.
A negative number is an error of the config file.

```yaml
log:
  path: '/var/log/kickd/kickd.log'
  max_size_mb: 50
  max_backups: 10
```
