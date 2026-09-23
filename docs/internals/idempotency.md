# Delivery guarantees and idempotency

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event becomes a **run** in a SQLite database called the **queue**.
The long-running kickd process, the **agent**, starts the command of each run.

A command is **idempotent** when running it twice with the same input leaves the same result as running it once.
Copying a folder with rsync is idempotent, and so is setting a value.
Sending an email and appending a line to a file are not.
Whether a command needs to be idempotent depends on how many times kickd can start it for one firing.

Two terms describe how many times a system performs a piece of work:

- **At most once**: the work happens once or not at all. It is never repeated, but it can be lost.
- **At least once**: the work happens one or more times. It is not lost, but it can be repeated.

## When a firing is recorded

A firing can be lost before it reaches the queue.
Once it is in the queue, it stays there until the agent settles it.

| Trigger | Recorded | Firings while the agent is stopped |
|---|---|---|
| `kickd event` | Before `kickd event` returns | Recorded, and run when the agent starts |
| Webhook | Before the 202 response, or before the wait starts with `wait: true` | Refused, because no server is listening. The caller has to retry. |
| Cron | When the schedule comes due | Not recorded. kickd does not make up schedules that passed while it was stopped. |
| File | When the debounce time has passed after the last change | Not recorded. Changes made while the agent is stopped are not detected. |

A file trigger collects changes in memory until its debounce time passes.
Changes that it has collected when the agent stops, or when the config reloads, are discarded.
During a config reload, the webhook server also closes its listener and opens a new one, and a request that arrives in between is refused.

The queue runs SQLite in WAL mode with `synchronous=NORMAL`.
A recorded firing survives a crash of kickd, and a crash of the process that recorded it.
A crash of the operating system or a power loss can undo the commits of the last moments before it, including a firing that `kickd event` already reported as queued.

## How many times a run starts

kickd starts the command of a firing once, unless the run is interrupted.
An **interrupted run** is a run whose command was cut off because the agent stopped or crashed.
When the agent starts again, the event's `on_interrupt` setting decides what happens to each interrupted run:

- **abandon** (the default): the run is recorded as `abandoned` and does not start again. The command runs **at most once** for each firing.
- **rerun**: the firing runs again as a new run, until a run finishes or `max_attempts` runs have been made. The command runs **at least once** for each firing, unless every attempt is interrupted, and it can run more than once.

kickd does not rerun a run that failed.
A command that exited with a non-zero code, or that ran past its `timeout`, leaves a `failed` run, and the run is final.

## Reruns of finished work

With `rerun`, a command can repeat work that it already finished.

The agent records the outcome of a run only after the command exits.
If the agent crashes after the command has done its work, but before the outcome is recorded, the row stays `running`.
At the next start, the agent treats the row as interrupted, and `rerun` starts the command again.

A clean stop has a similar gap.
When the agent stops, it sends SIGTERM to the running commands on macOS and Linux.
A program that handles SIGTERM can exit with code 0 before it finishes its work, so the exit code cannot tell a finished command from a stopped one.
kickd therefore records every run that ends during a stop as `interrupted`, even with exit code 0, and `rerun` starts it again.

## Overlap after a crash of the agent

When the agent crashes or is killed, it cannot stop the commands it started.
Every command runs in a process group of its own, and whether the commands keep running depends on what started the agent:

- **macOS**: launchd stops only the processes in the process group of kickd itself, so the commands keep running.
- **Windows**: child processes outlive their parent, so the commands keep running.
- **A terminal**: nothing stops the commands of a kickd that was killed.
- **Linux with systemd**: systemd stops the processes left in the unit, including the commands, when the process of kickd exits.

When the agent starts again under `on_interrupt: rerun`, the rerun can start while the old command still runs.
The two then run at the same time, and both can finish.
A command that must not overlap with itself needs its own lock, for example a lock file taken with `flock` on Linux.

## Repeated firings

kickd does not merge or deduplicate firings.
Each call of `kickd event`, each webhook request and each schedule adds its own run, even when two firings carry the same data.

A webhook caller that retries a request after a timeout or an error fires the event again.
When the caller sends the same `X-Request-ID` header in every attempt, kickd gives the runs the same request ID, so the command can tell that it has seen the request before.
Callers that send a delivery ID in another header can be handled the same way, because the command finds every header in the payload, under `webhook.headers`.

## Skipped firings

With `concurrency: skip`, the default, a firing that arrives while the event is running is recorded as `skipped` and never runs.
For a file trigger, this can leave a file unprocessed.
A file that arrives while the command runs fires the event, the firing is skipped, and the event does not fire again until the next change.

Two settings avoid this:

- **`concurrency: queue`**: the firing waits and runs after the current run, so a run always starts after the last change.
- **A command that processes everything it finds**: a command that handles every file in the folder, rather than only the files in its payload, picks up the file at its next run.

## Writing idempotent commands

These patterns make a command safe to run twice:

- **Converge on a state**: describe the result instead of the change. `rsync -a src/ dst/` and `mkdir -p` reach the same state from any starting point.
- **Record completion under the request ID**: `KICKD_REQUEST_ID` stays the same in every attempt of one firing. A command can write a marker named after it when it finishes, and exit early when the marker already exists.
- **Write atomically**: write the result to a temporary file in the same directory, then rename it into place. On macOS and Linux, a rename within one file system replaces the target in one step, so an interrupted run leaves either the old file or the new one.
- **Move what is done**: a command that processes the files of a folder can move each file away when it is done, so a rerun sees only the files that remain.
- **Check the attempt**: `KICKD_ATTEMPT` is greater than 1 in a rerun, so a command can look for partial work before it starts over.

This script records completion under the request ID:

```sh
#!/bin/sh
set -eu
done_dir="$HOME/.local/state/nightly-report"
mkdir -p "$done_dir"
if [ -e "$done_dir/$KICKD_REQUEST_ID" ]; then
  echo "request $KICKD_REQUEST_ID is already done"
  exit 0
fi
./make-report > report.tmp
mv report.tmp report.html
touch "$done_dir/$KICKD_REQUEST_ID"
```

A crash between `mv` and `touch` makes the rerun build the report again, which is harmless, because the rerun builds the same report.
The markers can be deleted once their runs are older than `queue.retention`, because kickd no longer reruns them.

For work that must never happen twice, such as charging a card or sending an email, keep `on_interrupt: abandon`.
Another way is to make the receiving system reject repeats, for example with an idempotency key made from `KICKD_REQUEST_ID`.
