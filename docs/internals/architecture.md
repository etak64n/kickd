# Architecture

[Documentation index](../../README.md#documentation)

kickd is one Go executable.
`kickd run` starts the long-running process, called the **agent**.
The other subcommands, such as `kickd event` and `kickd runs`, are short-lived processes that read and write the same database as the agent.

In kickd, a named command in the config file is an **event**, and a **trigger** fires an event.
Each firing becomes a **run**, one execution of the event's command, recorded in the **database**, a SQLite file.
The runs that have not started yet form the **queue**.

## Parts of the agent

The agent is built from these parts, each in a Go package under `internal/`:

| Part | Package | Role |
|---|---|---|
| Config | `config` | Loads the YAML file, validates it, and fills in defaults |
| Triggers | `trigger` | Turn cron schedules, HTTP requests and file changes into firings |
| Database | `queue` | Stores every run in the SQLite database |
| Dispatcher | `runner` | Takes queued runs in order, applies each event's concurrency policy, and starts runs |
| Runner | `runner` | Starts the command of a run as a process, collects its output, and stops it when needed |

The `agent` package connects the parts and reloads the config, and the `logging` package writes the log records of all of them.

## Path of a firing

![Four triggers fire events: a cron schedule at 3:00, a POST request to /hooks/deploy, changes in ~/app/src, and the command kickd event notify. kickd runs the command of each event: rsync for backup, deploy.sh for deploy, make build for build, and notify.sh for notify.](../images/overview.svg)

Every firing takes the same path, whatever fired it:

1. A trigger, or `kickd event`, builds the **payload**: the event name, the parameters, and the details of the trigger.
2. The payload is inserted into the database as a run with the status `queued`. From this point, the firing survives a restart of the agent.
3. The dispatcher reads the queued runs, oldest first. For each run, the event's `concurrency` setting decides whether the run starts, is skipped, or keeps waiting.
4. The runner starts the command in a new process and waits for it to exit.
5. The dispatcher writes the outcome to the database: the status, the exit code, the duration and the start of the output.

Triggers run inside the agent, so a trigger inserts its run through the dispatcher and wakes it at once.
`kickd event` runs in a process of its own and inserts the run into the database directly.
The agent notices writes from other processes by checking the database every 0.2 seconds.

A queue in a database, rather than in the memory of the agent, is what lets `kickd event` work while the agent is stopped, and lets waiting runs survive a crash.

## Concurrency inside the agent

The agent runs its parts in separate goroutines, the lightweight threads of Go:

- **Dispatcher loop**: one goroutine consumes the queue. It also writes a heartbeat to the database every 5 seconds, and deletes old runs every hour.
- **Triggers**: one goroutine runs every cron schedule, one runs the webhook server with one more goroutine per HTTP request, and each file trigger has a goroutine of its own.
- **Runs**: each running command has a goroutine that waits for it.
- **Config watcher**: one goroutine watches the directory of the config file.

The dispatcher keeps the number of running commands of each event in memory, and uses it to apply `concurrency`.
With `parallel`, kickd sets no limit on the number of commands running at once.

## Reloading the config

When the config file changes, the agent loads the new file and validates all of it before using any part.
When the new config is valid, the agent stops every trigger, gives the dispatcher the new event definitions, and starts the triggers of the new config.
Runs that are running keep the definition they started with.
Runs that are waiting take the new definition when they start.
When the new config is invalid, the agent logs the errors and keeps the triggers and definitions of the previous config.

Restarting the triggers has three effects:

- The webhook server closes its listener and opens a new one, so a request that arrives in between is refused.
- Each file trigger discards the changes it has collected but not yet fired.
- Each cron trigger continues from the time it was last handled, which the database keeps, so a scheduled time that falls in the reload still runs.

## Stopping

The agent stops on SIGINT or SIGTERM, on Ctrl+C in a terminal, and on a stop request from the service manager.
Stopping cancels the triggers and the running commands at the same moment, and no new run starts after that.
The agent then waits for the commands to exit, records their runs as `interrupted`, and marks itself as stopped in the database.
Runs that were still waiting stay `queued`.

When the agent starts, it first settles the runs that the previous agent process left unfinished, and only then consumes the queue.
The event's `on_interrupt` setting decides whether each unfinished run starts again.
