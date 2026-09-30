# Configuration reference

[Documentation index](../README.md#documentation)

The kickd config file is YAML with four top-level sections: `log`, `webhook`, `database` and `events`.
In kickd, a named command is an **event**, and a **trigger** fires an event.
Each firing is recorded as a **run** in the **database**, a SQLite file, and the long-running kickd process, called the **agent**, starts the command of each run.

Six pages describe the keys in detail, and each key of the tables links to its description:

- [Log settings](settings/log.md): the `log` section
- [Webhook server settings](settings/webhook.md): the `webhook` section
- [Database settings](settings/database.md): the `database` section
- [Event settings](settings/events.md): the keys of each event
- [Parameter settings](settings/params.md): the keys of each parameter of an event
- [Trigger settings](settings/triggers.md): the keys of each type of trigger

Every key is optional unless its description says otherwise.
kickd reports unknown keys as errors, and `kickd check` lists every error in the file.

## Writing values

- **Strings**: the text as it is. Single quotes, as in `'C:\Data'`, keep backslashes and double quotes as they are, and a single quote inside them is written twice, as in `'it''s'`. A value without quotes, such as `1`, is the same string as `'1'`.
- **Whole numbers**, such as `max_attempts: 5`, and **`true` and `false`**: without quotes.
- **Durations**: a number with one of the units `ms`, `s`, `m` and `h`, such as `500ms`, `90s` or `1h30m`. Days are not a unit, and a bare number such as `30` is an error.
- **Paths**: a leading `~` is the home directory, `${VAR}` is the value of the environment variable `VAR`, and a relative path starts at the directory of the config file. `kickd check` prints the resolved paths.
- **Lists**: `[a, b]` on one line, or one `- ` item on each line.

Earlier versions of kickd called `log.path` `log.file` and the `database` section `queue`, took a string command as `shell`, and, in v0.2.0, had a `base_dir` section for the directory of the log and the database.
A config file with the old keys fails to load, with a message that gives the new key.

## Top-level keys

| Key | Default | Description |
|---|---|---|
| [`log.path`](settings/log.md#logpath) | none | The log file. Without it, kickd logs to standard error. |
| [`log.level`](settings/log.md#loglevel) | `info` | The least severe level that is logged: `trace`, `debug`, `info`, `warn`, `error` or `fatal`. `LOG_LEVEL` overrides it. |
| [`log.format`](settings/log.md#logformat) | `auto` | `auto`, `json` or `text`. `auto` writes text to a terminal and JSON to files and pipes. `LOG_FORMAT` overrides it. |
| [`log.max_size_mb`](settings/log.md#logmax_size_mb) | `10` | The size in MB past which kickd renames the log file with `.1` appended and starts a new one. |
| [`log.max_backups`](settings/log.md#logmax_backups) | `5` | How many renamed log files to keep. |
| [`webhook.enabled`](settings/webhook.md#webhookenabled) | `true` | `false` keeps the HTTP server off, so webhook triggers do not fire. |
| [`webhook.listen`](settings/webhook.md#webhooklisten) | `127.0.0.1:8787` | The address of the HTTP server that all webhook triggers share. |
| [`webhook.max_body_bytes`](settings/webhook.md#webhookmax_body_bytes) | `1048576` | The largest request body accepted, in bytes. |
| [`database.path`](settings/database.md#databasepath) | `kickd.db` | The SQLite file that records every run, next to the config file by default. |
| [`database.retention`](settings/database.md#databaseretention) | `168h` | How long ended runs stay in the database. |
| [`events`](settings/events.md) | none | The list of events. Required, with at least one event. |

## Event keys

| Key | Default | Description |
|---|---|---|
| [`name`](settings/events.md#name) | none | The event name. Required and unique: up to 64 letters, digits, `.`, `_`, `:` and `-`, starting with a letter or digit. |
| [`description`](settings/events.md#description) | none | A description, shown by `kickd events`. |
| [`command`](settings/events.md#command) | none | What the event runs. Required. A string runs through the shell, and a list starts a program with its arguments. |
| [`workdir`](settings/events.md#workdir) | the directory of the config file | The working directory of the command. It must exist. |
| [`env`](settings/events.md#env) | none | Environment variables for the command. A `PATH` here also decides where the program of `command` is found. |
| [`timeout`](settings/events.md#timeout) | none | The longest time the command may run. Without it, there is no limit. |
| [`concurrency`](settings/events.md#concurrency) | `skip` | What happens when the event fires while it runs: `skip`, `queue` or `parallel`. |
| [`on_interrupt`](settings/events.md#on_interrupt) | `abandon` | What happens to a run that a stop or a crash cut off: `abandon` or `rerun`. |
| [`max_attempts`](settings/events.md#max_attempts) | `3` | With `rerun`, how many times one firing may run, counting the first run. |
| [`stdin`](settings/events.md#stdin) | `none` | `payload` passes the payload JSON on standard input. |
| [`log_output`](settings/events.md#log_output) | `true` | Whether kickd logs the output of the command and stores it with the run. |
| [`params`](settings/events.md#params) | none | The parameters that a firing can pass. |
| [`triggers`](settings/events.md#triggers) | none | The triggers, one or more. Required. |

## Parameter keys

| Key | Default | Description |
|---|---|---|
| [`name`](settings/params.md#name) | none | The parameter name. Required: up to 64 letters, digits and `_`, not starting with a digit. |
| [`required`](settings/params.md#required) | `false` | `true` refuses a firing without the parameter. |
| [`default`](settings/params.md#default) | none | The value when a firing does not pass the parameter. Without it, the value is empty. |
| [`description`](settings/params.md#description) | none | A description, shown by `kickd events --json`. |

## Trigger keys

Each trigger takes `type` and only the keys of its type.

| Type | Keys | Fires the event |
|---|---|---|
| [`manual`](settings/triggers.md#manual-triggers) | none | When `kickd event NAME` names the event |
| [`cron`](settings/triggers.md#cron-triggers) | `schedule` (required), `timezone`, `missed` (default `run`) | At the times of a cron expression |
| [`webhook`](settings/triggers.md#webhook-triggers) | `path` (required), `methods`, `token`, `secret`, `wait` (default `false`) | When an HTTP request arrives at the path |
| [`file`](settings/triggers.md#file-triggers) | `path` (required), `recursive` (default `false`), `include`, `exclude`, `changes`, `debounce` (default `1s`) | When files change in the directory |
| [`after`](settings/triggers.md#after-triggers) | `event` (required), `status` (required) | When a run of the other event ends with one of the statuses |
| [`startup`](settings/triggers.md#startup-triggers) | none | When the agent starts |
| [`wake`](settings/triggers.md#wake-triggers) | none | When the machine wakes from sleep |

An event lists at most one manual, one startup and one wake trigger.

## When changes take effect

The agent reloads its config file when the file is saved, 0.5 seconds after the save, and on SIGHUP on macOS and Linux.
When the new file has errors, the agent logs them and keeps the previous config.
Most keys take effect at that reload, and some wait for the next start of the agent:

| Keys | Take effect |
|---|---|
| `log.*` | At the next start of the agent, which opens its log once |
| `webhook.*` | At the reload, which restarts the HTTP server |
| `database.*` | At the next start of the agent, which opens the database once |
| `name`, `command`, `workdir`, `env`, `timeout`, `concurrency`, `stdin`, `log_output` | At the reload, for runs that start after it. Running commands keep the settings with which they started. |
| `description` and `params` | At once for `kickd events` and `kickd event`, which read the config file each time they run, and at the reload for webhook requests |
| `on_interrupt` and `max_attempts` | At the next start of the agent, which handles the runs that were cut off when it starts |
| Manual triggers | At once for `kickd event`, which reads the config file each time it runs |
| Cron, webhook, file, after and wake triggers | At the reload, which stops these triggers and starts them again from the new file |
| Startup triggers | At the next start of the agent |
