# Queue internals

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event is recorded as a **run**.
The runs are kept in a SQLite database called the **queue**.
The long-running kickd process, the **agent**, consumes the queue.
The other kickd subcommands read and write it from their own processes.

## Why a database

kickd keeps runs in a database file, rather than in the memory of the agent, for three reasons:

- **Restarts**: a waiting run survives a stop, a crash and a reboot, because it is on disk.
- **Sharing between processes**: `kickd event`, `kickd cancel` and the other subcommands work on the same records as the agent, without a network connection to it.
- **History**: finished runs stay available to `kickd runs` and `kickd show`.

SQLite keeps a whole database in one file and needs no server process.
kickd uses modernc.org/sqlite, a port of SQLite to Go generated from its C source, so kickd builds for every platform without a C compiler.

## Database settings

kickd opens the database with these settings:

| Setting | Value | Effect |
|---|---|---|
| `journal_mode` | `WAL` | Readers and a writer do not block each other |
| `synchronous` | `NORMAL` | A commit survives a crash of any process, but a crash of the OS or a power loss can undo the last commits |
| `busy_timeout` | 5000 ms | A connection that finds the database locked waits up to 5 seconds before it fails |
| Transactions | `IMMEDIATE` | Every transaction takes the write lock when it begins |
| Connections | One per process | The statements of one process run one at a time |

**WAL**, or write-ahead logging, is a SQLite mode in which a commit appends the changes to a second file, `kickd.db-wal`.
SQLite later copies the changes into the database file.
Readers keep reading the last committed state while a writer appends, so `kickd runs` does not wait for the agent.

Immediate transactions prevent a deadlock between processes.
In the default mode, a SQLite transaction takes the write lock only at its first write.
When two processes each read inside a transaction and then both try to write, neither can wait for the other, so SQLite fails one of them at once with `SQLITE_BUSY`.
A transaction that takes the write lock at its beginning turns this into an ordinary wait, bounded by the busy timeout.

Two processes that create a new database at the same moment can still collide while they switch it to WAL mode.
kickd retries the setup for up to 30 seconds in that case.

On macOS and Linux, kickd creates a new database file with the permission 0600, readable and writable only by its owner, before SQLite opens it, because runs hold payloads and command output.
SQLite gives the `-wal` and `-shm` files that it creates next to the database the permissions of the database file, so they are private as well.
The version of the table layout is stored in SQLite's `user_version` field, and a kickd that finds a newer version than it knows refuses to open the database.

## Tables

The `runs` table holds one row per run:

| Columns | Contents |
|---|---|
| `id` | The run ID, assigned in order of insertion |
| `request_id`, `event`, `trigger_kind`, `trigger_id`, `source` | What fired the run |
| `payload` | The JSON that describes the firing, which the command receives |
| `status`, `reason`, `detail` | The state of the run, and why it is in that state |
| `attempt`, `retry_of` | The attempt number, and the run that this rerun repeats |
| `exit_code`, `signal`, `output`, `output_truncated` | The outcome of the command |
| `skipped` | How many firings were skipped while this run was active |
| `cancel_requested` | Set by `kickd cancel` for a running run |
| `created_at`, `started_at`, `finished_at`, `duration_ms` | Times, in milliseconds since 1970 |

The `agent` table holds a single row about the agent: its process ID, version, host, start time, last heartbeat and stop time.

## Adding a run

A firing adds a row with the status `queued` in one transaction.
For an event with `concurrency: queue`, the same transaction first counts the queued runs of the event.
When there are already 1000, the row is inserted with the status `dropped` instead.
Counting and inserting in one immediate transaction keeps the limit exact, even when several processes add runs at the same moment.

The dispatcher is the part of the agent that consumes the queue.
Triggers insert their runs through the dispatcher and wake it at once.
`kickd event` inserts from its own process.

## Noticing writes from other processes

SQLite keeps a counter, `PRAGMA data_version`, that changes when another connection commits to the database file.
The agent has one connection, so a change of the counter means that another process wrote.
The agent reads the counter every 0.2 seconds and scans the queue when it changes.
It also scans every 5 seconds without a change, as a safeguard.
Reading the counter is a single statement, so the polling costs little.

## Consuming the queue

A scan reads up to 500 queued runs, oldest first, and handles each one by the `concurrency` of its event:

- **skip**: when the event is running, the row becomes `skipped`, and the `skipped` count of the active run grows by one.
- **queue**: when the event is running, the row stays `queued` for a later scan.
- **parallel**, or an event that is not running: the run starts.

Starting a run is a conditional update from `queued` to `running`.
When the update changes no row, another process changed the run first, for example with `kickd cancel`, and the run does not start.
This check keeps the agent and the subcommands consistent without holding a lock while a command runs.

A queued run of an event that the config no longer defines becomes `dropped`, with the reason `event_removed`.

The scan leaves out the runs of events that already run under `concurrency: queue`, because those runs cannot start yet.
A long backlog of one event therefore never hides the runs of other events behind it, however many runs wait.
When a scan reads all 500 places and starts or settles at least one run, the agent scans again at once, so the runs after the first 500 start without delay.

## Canceling a run

`kickd cancel` only changes the database:

- A queued run becomes `canceled` at once.
- A running run gets `cancel_requested` set. The agent sees the write within 0.2 seconds, stops the command, and records the run as `canceled`.

## Heartbeat

The agent writes the current time into the `agent` row every 5 seconds.
`kickd status` reports the agent as running when the last heartbeat is less than 15 seconds old and no stop is recorded.
A clean stop records the stop time, so `kickd status` reports the stop at once.
After a crash, the last heartbeat keeps the agent looking alive for up to 15 seconds.

## Recovery at startup

Before it consumes the queue, the agent looks for runs that the previous agent process left unfinished:

- A row with the status `interrupted` was stopped during a clean stop.
- A row still `running` was cut off by a crash, because only a live agent runs commands.

For each such run, the event's `on_interrupt` and `max_attempts` decide:

| Case | Result |
|---|---|
| The config no longer defines the event | `abandoned`, with the reason `event_removed` |
| `on_interrupt: abandon` | `abandoned`, with the reason `agent_stopped` or `agent_crashed` |
| `on_interrupt: rerun`, and `attempt` has reached `max_attempts` | `abandoned`, with the reason `max_attempts_reached` |
| `on_interrupt: rerun` | `retried`, and a new queued run is inserted |

The new run copies the payload with the attempt number increased by one, keeps the request ID, and points to the old run with `retry_of`.
The insert of the new run and the update of the old one happen in one transaction.
A crash during recovery therefore leaves either both changes or neither.

## Retention

When the agent starts, and every hour after that, the agent deletes finished runs whose finish time is older than `queue.retention`.
Queued, running and interrupted runs are never deleted.

## Output storage

The runner, the part of the agent that runs commands, collects standard output and standard error in one buffer, and keeps the first 64 KB.
The dispatcher stores that buffer in `output` when the event's `log_output` is `true`, the default.
With `log_output: false`, the output is not stored.

## Database errors

When a database operation fails, the agent logs `Queue operation failed`, with the operation in `detail`.
It logs each operation at most once a minute, so a broken disk does not flood the log, and it tries again at the next scan.
