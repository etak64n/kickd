# The queue

[Documentation index](../README.md#documentation)

In kickd, a named command in the config file is an **event**, and asking kickd to run an event is **firing** it.
Every firing is recorded as a **run** in a SQLite database called the **queue**.
The long-running kickd process, the **agent**, takes runs from the queue and starts the event's command for each one.

## From firing to run

When an event fires, kickd writes a run with the status `queued` to the queue, and the agent starts it.
Cron, webhook and file triggers run inside the agent and wake it at once, so their runs start within milliseconds.
`kickd event` writes to the queue from a process of its own, so it works even while the agent is not running; the agent notices its run within 0.2 seconds.

Runs of different events never wait for each other.
The event's `concurrency` setting decides what happens to a run when the same event is already running:

- **skip** (the default): the run is recorded as `skipped`, and its command does not start.
- **queue**: the run waits until the running one finishes. Waiting runs start one at a time, in the order they were queued. Each event can have up to 1000 waiting runs.
- **parallel**: the run starts at once, alongside the running one.

## Run statuses

A run is always in one of these statuses:

| Status | Meaning |
|---|---|
| `queued` | Waiting to start |
| `running` | The command is running |
| `succeeded` | The command exited with code 0 |
| `failed` | The command failed: a non-zero exit code, a timeout, or a failure to start |
| `canceled` | Canceled with `kickd cancel` |
| `skipped` | Not started, because the event was running and its `concurrency` is `skip` |
| `dropped` | Discarded, because the event already had the maximum number of waiting runs, or the config no longer defines the event |
| `interrupted` | Stopped when the agent stopped; handled by `on_interrupt` when the agent starts again |
| `retried` | Interrupted, and replaced by a new run of the same firing |
| `abandoned` | Interrupted, and not run again |

## Run history

Finished runs stay in the queue for `queue.retention`, 7 days by default.
The history holds the status and exit code of each run, and the first 64 KB of its output when the event's `log_output` is `true`, the default.
`kickd runs` lists the history, and `kickd show` displays one run.

By default, the queue is `kickd.db` in the directory of the config file.
The agent and the other kickd commands must run as users that can read and write this database.

## Interrupted runs

When kickd or the machine stops, the commands that were running stop too.
A run cut off this way is called an **interrupted run**.
The event's `on_interrupt` setting decides what the agent does with interrupted runs when it starts again:

- **abandon** (the default): the run is recorded as `abandoned` and does not run again.
- **rerun**: the same firing runs again from the start, as a new run.

`max_attempts` limits how many times one firing runs under `rerun`, counting the first run.
The default is 3.
When a firing reaches the limit, its run is recorded as `abandoned` and the agent logs an ERROR.

The agent tells two kinds of stop apart:

- **Clean stop**: the agent stops the running commands and records their runs as `interrupted`. A command that exits with code 0 during the stop is recorded as interrupted as well.
- **Crash**: when the agent is killed or the machine loses power, the run stays `running` in the queue. When the agent starts again, it treats such runs as interrupted.

In the log, the `reason` of an interrupted run is `agent_stopped` after a clean stop and `agent_crashed` after a crash.

Every firing has a **request ID**, which kickd's log records and the command's environment carry as `requestId` and `KICKD_REQUEST_ID`.
A rerun keeps the request ID of the original run, and its attempt number is one higher.
The command receives the attempt number in `KICKD_ATTEMPT`, so it can skip work that an earlier attempt finished.
The original run is recorded as `retried`, and `kickd show` displays which run each rerun repeats.

`rerun` suits commands that give the same result when they run twice.
For work that must not happen twice, such as charging a card or sending an email, keep the default `abandon`.
