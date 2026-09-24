# Configuration reference

[Documentation index](../README.md#documentation)

The kickd config file is YAML with four top-level sections: `log`, `webhook`, `queue` and `events`.
In kickd, a named command is an **event**, and a **trigger** fires an event automatically.
Every key below is optional unless its description says otherwise.
kickd reports unknown keys as errors, and `kickd check` lists every error in the file.

## Top-level keys

| Key | Default | Description |
|---|---|---|
| `log.level` | `info` | The log level: `trace`, `debug`, `info`, `warn`, `error` or `fatal`. The environment variable `LOG_LEVEL` overrides it. |
| `log.format` | `auto` | The log format: `auto`, `json` or `text`. `auto` writes text to a terminal and JSON to files and pipes. The environment variable `LOG_FORMAT` overrides it. |
| `log.file` | none | The log file. Without it, kickd logs to standard error. |
| `log.max_size_mb` | `10` | When the log file grows past this size in MB, kickd renames it with `.1` appended and starts a new file. |
| `log.max_backups` | `5` | How many renamed log files to keep. `.1` is the newest, and files beyond this number are deleted. |
| `webhook.listen` | `127.0.0.1:8787` | The address of the HTTP server that all webhook triggers share. The server runs only when a webhook trigger exists. |
| `webhook.max_body_bytes` | `1048576` | The largest request body accepted, in bytes. |
| `queue.path` | `kickd.db` | The SQLite file of the queue, the database that records every run. |
| `queue.retention` | `168h` | How long finished runs stay in the history. |
| `events` | none | The list of events. Required, with at least one event. |

## Event keys

| Key | Description |
|---|---|
| `name` | The event name, required and unique in the file: up to 64 letters, digits, `.`, `_`, `:` and `-`, starting with a letter or digit. |
| `description` | A description, shown by `kickd events`. |
| `command` | The program and its arguments, as a list. kickd looks the program up in its own `PATH`. The elements are passed as they are, without a shell, so `~` and `*` are not expanded. |
| `shell` | A string run by the shell: `/bin/sh -c` on macOS and Linux, `cmd /S /C` on Windows. Each event has exactly one of `command` and `shell`. |
| `workdir` | The working directory of the command. It must exist when the config is loaded. Without it, the command runs in the working directory of kickd. |
| `env` | Environment variables added for the command. `${VAR}` in values expands to the environment variable of kickd. An entry replaces a variable of kickd with the same name. |
| `timeout` | The longest time the command may run. When it passes, kickd sends SIGTERM to the command's process group on macOS and Linux, and SIGKILL 10 seconds later if it is still running. On Windows, kickd ends the process tree at once. Without it, there is no limit. |
| `concurrency` | What happens when the event fires while it is running: `skip` (default), `queue` or `parallel`. |
| `on_interrupt` | What happens, when the agent starts again, to a run that a stop or crash cut off: `abandon` (default) or `rerun`. |
| `max_attempts` | With `rerun`, how many times one firing may run, counting the first run. Default `3`. |
| `stdin` | `payload` also passes the payload JSON, the information about the run, on standard input. Default `none`. |
| `log_output` | Whether kickd keeps the command's output. With the default `true`, each line of output is logged at DEBUG, the record of a failed run includes the end of standard error, and the first 64 KB are stored with the run. `false` does none of these; a webhook with `wait: true` still returns the output. |
| `params` | The parameters that a firing can pass. |
| `triggers` | The triggers. An event fired only from the command line needs none. |

A kickd running as a service has the directory of its config file as its working directory on macOS and Linux, and `C:\Windows\System32` on Windows.

## Parameter keys

| Key | Description |
|---|---|
| `name` | The parameter name, required: up to 64 letters, digits and `_`, not starting with a digit. |
| `required` | `true` makes the parameter mandatory. A required parameter cannot have a `default`, and its event cannot have cron or file triggers. |
| `default` | The value used when the parameter is omitted. |
| `description` | A description. |

## Cron trigger keys

| Key | Description |
|---|---|
| `type` | `cron`, required |
| `schedule` | A cron expression with five fields: minute, hour, day of month, month and day of week. A six-field form with a leading seconds field, and forms such as `@hourly`, `@daily` and `@every 10m`, also work. The day fields also take `L` for the last day of the month, `15W` for the weekday nearest to the 15th, `5L` for the last Friday and `fri#3` for the third Friday, and `7` is Sunday as `0` is. The [gocron README](https://github.com/etak64n/gocron#expressions) describes every form. Required. |
| `timezone` | A time zone name such as `Asia/Tokyo`. Without it, the local time of the machine applies. |
| `missed` | What happens to scheduled times that passed while the machine slept or kickd was stopped: `run` (default) runs once right after the machine wakes or kickd starts, however many times passed; `skip` waits for the next scheduled time. A scheduled time counts as missed when kickd notices it more than a minute late. |

## Webhook trigger keys

| Key | Description |
|---|---|
| `type` | `webhook`, required |
| `path` | The URL path, required. It starts with `/` and is unique among all triggers. `/healthz` is reserved for the health check. |
| `methods` | The HTTP methods accepted. Without it, every method is accepted. |
| `token` | A token that the caller must send as `Authorization: Bearer <token>`, in the `X-Kickd-Token` header, or as the query parameter `token`. |
| `secret` | The key for request signatures. The caller sends the HMAC-SHA256 of the body as `sha256=<hex>` in `X-Hub-Signature-256`, the format that GitHub uses, or in `X-Kickd-Signature`. |
| `wait` | `true` holds the response until the run finishes, and returns its exit code and output. |

## File trigger keys

| Key | Description |
|---|---|
| `type` | `file`, required |
| `path` | The directory to watch, required. It must exist when the config is loaded. |
| `recursive` | `true` also watches subdirectories, including ones created later. |
| `include` | Patterns of files to include. A pattern without a slash, such as `*.md`, matches the file name. A pattern with a slash, such as `docs/*.md`, matches the path relative to the watched directory. Without it, every file is included. |
| `exclude` | Patterns to exclude. They are matched against every component of the path, so `.git` excludes everything under a `.git` directory. |
| `changes` | The kinds of change that fire the event: `create`, `write`, `remove`, `rename` and `chmod`. The default is every kind except `chmod`. |
| `debounce` | The event fires once after no new change has arrived for this long. Default `1s`. All changes during the wait are combined into one firing. |

Patterns use the syntax of Go's `path.Match`: `*`, `?` and `[...]`.
`**` is not supported.

Each trigger accepts only the keys of its own type.
For example, `schedule` on a file trigger is an error.
