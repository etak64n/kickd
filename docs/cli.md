# Command-line reference

[Documentation index](../README.md#documentation)

kickd is one executable with subcommands.
`kickd run` starts the long-running kickd process, called the **agent**.
The other subcommands prepare the setup, or work with the runs.

In kickd, a named command in the config file is an **event**.
Each firing of an event is recorded as a **run** in the **database**, a SQLite file, and the agent starts the command of each run.
A run that has not started yet waits in the **queue**.

Every subcommand reads the config file.
The file is the first of these that applies: `-c` or `--config`, the environment variable `KICKD_CONFIG`, `kickd/config.yaml` in the user's config directory, and `kickd.yaml` in the current directory.
The subcommands that work with runs read and write the database of the config file directly, the same database that the agent uses.

## Subcommands

| Subcommand | What it does |
|---|---|
| `kickd run` | Runs the agent in the foreground. A service manager starts the agent with this subcommand too. |
| `kickd check` | Validates the config file, prints every error, and lists the events and their triggers. |
| `kickd init` | Writes an example config file. It refuses to overwrite an existing file. |
| `kickd service ACTION` | Manages the service. `ACTION` is `install`, `uninstall`, `start`, `stop`, `restart` or `status`. |
| `kickd event NAME` | Fires an event. |
| `kickd events` | Lists the events and the triggers that fire each one. |
| `kickd queue` | Shows queued, running and interrupted runs. |
| `kickd runs` | Shows the run history, newest first. `--event`, `--status` and `--limit` filter it. |
| `kickd show RUN_ID` | Shows one run: its details, parameters and command output. |
| `kickd cancel RUN_ID` | Cancels a queued run, or stops a running one. |
| `kickd status` | Shows whether the agent is running, and how many runs are waiting. |
| `kickd licenses` | Prints the licenses of kickd and of the third-party software that its executables include. |
| `kickd version` | Prints the version. |

`kickd service` takes two more options:

- **--user**: installs a per-user service, which is a LaunchAgent on macOS and a per-user systemd unit on Linux. Windows ignores it.
- **--name NAME**: sets the service name, `kickd` by default.

Every action on one service takes the same `--user` and `--name` as its `install`.

The run ID is the number in the RUN column of `kickd queue` and `kickd runs`, and the `runId` in the agent's log.
`--json` makes `event`, `events`, `queue`, `runs`, `show` and `status` print JSON.

## Firing an event

`kickd event` fires an event.
Parameters follow the event name as `KEY=VALUE`:

```sh
kickd event deploy ref=v1.2
```

`--data` passes parameters as one JSON object of strings.
`KEY=VALUE` arguments override its entries.

```sh
kickd event deploy --data '{"ref":"v1.2"}'
```

`kickd event` writes the run to the database and prints its run ID.
A run fired while the agent is stopped stays in the queue and starts when the agent starts.

With `--wait`, `kickd event` waits for the run to finish and prints a table of the result.
If the run is interrupted and rerun during the wait, `kickd event` waits for the rerun.
`--timeout` limits how long it waits.

| Exit code | Meaning |
|---|---|
| 0 | The run succeeded. Without `--wait`, the run was queued. |
| 1 | The run did not succeed: it failed, or was skipped, dropped, canceled or abandoned. Errors of kickd itself also exit with 1. |
| 2 | The arguments were wrong, such as an undefined event or an undeclared parameter. |
| 124 | The time given with `--timeout` passed. |

## Parameters

An event declares its parameters under `params`.
An event with `params` accepts only the declared parameters.
A parameter with `required: true` must be given, and a parameter with a `default` takes that value when omitted.
An event without `params` accepts parameters of any name made of letters, digits and `_`.

The command receives each parameter as an environment variable: `ref` arrives as `KICKD_DATA_REF`.
`KICKD_EVENT_DATA` holds all parameters as one JSON object.
Cron and file triggers cannot pass parameters, so an event with a required parameter cannot have those triggers.
