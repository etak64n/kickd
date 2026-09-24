# Logging

[Documentation index](../README.md#documentation)

The long-running kickd process, called the **agent**, logs what it does: starting, loading its config, starting and finishing runs, and receiving webhooks.
In kickd, a named command in the config file is an **event**, and each firing of an event is recorded as a **run**.
The key names of the log records follow the JSON log format of AWS Lambda.

## Choosing a format

kickd writes logs in two formats:

- **JSON**: one JSON object per line, known as JSON Lines. It suits log files and programs that read the log.
- **Text**: five columns separated by tabs: timestamp, requestId, level, message, and the remaining keys as one JSON object, or `-` when there are none. It suits people reading a terminal.

The default `log.format`, `auto`, picks the format by destination: text for a terminal, JSON for files and pipes.
When `kickd run` runs in a terminal, it also shows every record there as text, even when `log.path` is set.

On a terminal, each level has a color: INFO is cyan, WARN yellow, ERROR and FATAL red, and DEBUG and TRACE gray.
The environment variable `NO_COLOR` turns colors off.

## Log levels

The six levels, from most to least severe, are FATAL, ERROR, WARN, INFO, DEBUG and TRACE.
The default level, `info`, writes records at INFO and above.
A FATAL record is the last record of a kickd that cannot start.

The environment variables `LOG_LEVEL` and `LOG_FORMAT` take precedence over `log.level` and `log.format` in the config file.
With them, a detailed log is available for one run of the agent, without editing the config file:

```sh
LOG_LEVEL=debug LOG_FORMAT=text kickd run
```

## Keys on every record

- **timestamp**: when the record was written, in UTC, in RFC 3339 format with milliseconds.
- **level**: one of the six level names.
- **message**: a fixed English sentence that says what happened. Values that vary go into other keys.
- **requestId**: the ID of one unit of work. kickd creates one for each firing of an event, and the records of the firing's runs, skips and reruns share it. For a webhook, kickd takes the caller's `X-Request-ID` header when it has one. Records that belong to no firing, such as startup, shutdown and config reloads, carry an ID that the agent creates each time its process starts.
- **service**: the name of the program, `kickd`. Only JSON records have this key.

JSON records look like this:

```json
{"timestamp":"2026-09-23T08:41:12.345Z","level":"INFO","message":"Run started","requestId":"gh-delivery-7f3a","service":"kickd","event":"deploy","trigger":"webhook","runId":42,"triggerId":"webhook:/hooks/deploy","method":"POST"}
{"timestamp":"2026-09-23T08:41:14.002Z","level":"ERROR","message":"Run failed","requestId":"gh-delivery-7f3a","service":"kickd","event":"deploy","trigger":"webhook","runId":42,"reason":"exit_code","exitCode":4,"durationMs":1657,"stderrTail":"disk quota exceeded","location":"runner.go:(*Runner).execute:311"}
```

The same two records in text format:

```text
2026-09-23T08:41:12.345Z	gh-delivery-7f3a	INFO	Run started	{"event":"deploy","trigger":"webhook","runId":42,"triggerId":"webhook:/hooks/deploy","method":"POST"}
2026-09-23T08:41:14.002Z	gh-delivery-7f3a	ERROR	Run failed	{"event":"deploy","trigger":"webhook","runId":42,"reason":"exit_code","exitCode":4,"durationMs":1657,"stderrTail":"disk quota exceeded","location":"runner.go:(*Runner).execute:311"}
```

## Main messages

| message | Level | When | Main keys |
|---|---|---|---|
| `Agent starting` | INFO | The agent started | `version`, `pid`, `file` |
| `Database opened` | INFO | The agent opened the database | `file`, `count` |
| `Interrupted run requeued` | WARN | An interrupted run will run again, as `on_interrupt: rerun` says | `runId`, `reason`, `attempt`, `maxAttempts` |
| `Interrupted run abandoned` | WARN | An interrupted run will not run again. ERROR when the run reached `max_attempts` | `runId`, `reason`, `attempt` |
| `Config loaded` | INFO | The agent loaded its config at startup | `file`, `eventCount`, `triggerCount` |
| `Config reloaded` | INFO | The agent reloaded its config | `file`, `eventCount`, `triggerCount` |
| `Config reload failed` | ERROR | The reloaded config had errors. The agent keeps the previous config | `file`, `errorType`, `errorMessage` |
| `Config warning` | WARN | The config has a problem that does not stop the agent | `event`, `reason`, `detail` |
| `File watch started` | INFO | A file trigger started watching a directory | `event`, `file`, `recursive`, `count` |
| `Cron schedule added` | INFO | A cron trigger was registered | `event`, `schedule`, `nextRunAt` |
| `Missed schedule caught up` | INFO | Scheduled times passed while the machine slept or kickd was stopped, and one run makes up for them, as `missed: run` says | `event`, `schedule`, `scheduledAt`, `count`, `detail` |
| `Missed schedule skipped` | INFO | Scheduled times passed while the machine slept or kickd was stopped, and kickd skips them, as `missed: skip` says | `event`, `schedule`, `scheduledAt`, `count`, `nextRunAt` |
| `Webhook server listening` | INFO | The webhook server started listening | `listen`, `count` |
| `Webhook triggers disabled` | INFO | `webhook.enabled` is `false`, so the webhook server does not start and the webhook triggers do not fire | `count` |
| `Run started` | INFO | The command of a run started | `event`, `trigger`, `triggerId`, `runId`, `attempt`, `source` |
| `Run completed` | INFO | The command exited with code 0 | `exitCode`, `durationMs`, `skipped` |
| `Run failed` | ERROR | The command failed | `reason`, `exitCode`, `signal`, `durationMs`, `stderrTail` |
| `Run interrupted` | WARN | The agent stopped a running command because the agent was stopping | `reason`, `signal`, `durationMs` |
| `Run canceled` | WARN | `kickd cancel` stopped a running command | `reason`, `signal`, `durationMs` |
| `Run cancel requested` | INFO | `kickd cancel` asked the agent to stop a running run | `runId` |
| `Run skipped, previous run still active` | WARN | A firing was skipped because the event was running and its `concurrency` is `skip` | `runId`, `activeRun`, `activeRequestId`, `activeForMs` |
| `Run dropped, queue full` | WARN | A firing was discarded because the event already had the maximum number of waiting runs | `runId`, `thresholdCount` |
| `Queued run dropped` | WARN | A waiting run was discarded because the config no longer defines its event | `runId`, `reason` |
| `Webhook received` | INFO | A webhook request passed authentication | `method`, `path`, `bytes`, `authMethod` |
| `Webhook authentication failed` | WARN | The token or signature of a webhook request did not match | `reason`, `authMethod`, `remoteAddr` |
| `Webhook rejected` | WARN | A webhook request had a method that is not allowed, a body over the limit, or a missing required parameter | `reason`, `detail` |
| `Request completed` | INFO | The webhook server sent a response | `method`, `path`, `status`, `durationMs` |
| `Database operation failed` | ERROR | Reading or writing the database failed | `detail`, `file`, `errorType`, `errorMessage` |
| `Database path change needs a restart` | WARN | A reloaded config moves the database, which takes effect only when the agent restarts | `file`, `detail` |
| `Agent stopping` | INFO | The agent began to stop | `signal` or `reason`, `inFlightJobs` |
| `Agent stopped` | INFO | The agent stopped | `uptimeSec`, `processed`, `succeeded`, `failed`, `skipped`, `canceled`, `interrupted` |
| `Config load failed` | FATAL | The config had errors at startup, so the agent could not start | `file`, `errorType`, `errorMessage`, `exitCode` |
| `Agent start failed` | FATAL | A trigger could not start, so the agent could not start | `errorType`, `errorMessage`, `exitCode` |

`Run skipped, previous run still active` is a WARN only for the first skip during a run, and a DEBUG for later ones.
The record that ends the running run counts the skips in `skipped`.

The `reason` of `Run failed` gives the kind of failure:

- **exit_code**: the command exited with a non-zero code.
- **timeout**: the command ran past `timeout`, so kickd stopped it. `thresholdMs` holds the limit.
- **start_failed**: the command could not start.
- **wait_failed**: the command exited with code 0, but reading its output failed.
- **panic**: an unexpected error (a Go panic) occurred inside kickd.

The `reason` of `Interrupted run requeued` and `Interrupted run abandoned` gives the cause of the interruption: `agent_crashed` when the agent had crashed, and `agent_stopped` when it had stopped cleanly.

At DEBUG, kickd also logs the command line it runs (`Running command`), each line of command output (`Run output`) and each run added to the queue (`Run queued`).
At TRACE, it also logs each file change it detects (`File change detected`).

## Keys on error records

ERROR and FATAL records carry keys that help find the cause:

- **errorType**: the kind of error: the name of the Go error type with its package, such as `fs.PathError`.
- **errorMessage**: the text of the error.
- **location**: where the record was written: file name, function name and line number, joined with colons.
- **stackTrace**: the stack trace, as an array. Go errors carry no stack trace, so only records of a recovered panic have this key.
- **stderrTail**: for a failed command, the last lines of its standard error, up to 20.

## Secrets

kickd does not write `token`, `secret` or the values of `env` from the config file to the log.
In command lines, command output and `stderrTail`, values that look like passwords or tokens are replaced with `***masked***`.
The forms it recognizes include `Authorization: Bearer ...`, `password=...`, `--token ...` and `user:password@` in URLs.
kickd cannot recognize secrets in other forms, so pass secrets to commands with `env` rather than as arguments.

On macOS and Linux, kickd creates the log file with the permissions that the umask allows, usually 0644, so other users of the machine can read it.
A failed run adds the end of its standard error to the log, and at the DEBUG level every line of output goes there too, so commands should not print secrets.

## Log file rotation

When the file set by `log.path` grows past `log.max_size_mb`, kickd renames it to `kickd.log.1`.
The previous `.1` becomes `.2`, `.2` becomes `.3`, and so on, and files beyond `log.max_backups` are deleted.
If renaming fails, kickd keeps writing to the same file and reports the failure on standard error.
