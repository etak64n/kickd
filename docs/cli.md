# Command-line reference

[Documentation index](index.md)

kickd is one executable with subcommands.
`kickd run` starts the long-running kickd process, called the **agent**.
The other subcommands prepare the setup, or work with the runs.

In kickd, a named command in the config file is an **event**.
Each firing of an event is recorded as a **run** in the **database**, a SQLite file, and the agent starts the command of each run.
A run that has not started yet waits in the **queue**.

Every subcommand reads the config file of the user who runs it, and takes no option that names another file:

| Who runs kickd | Config file |
|---|---|
| A user | `~/.kickd/config.yaml` |
| root on Linux, as with `sudo` | `/etc/kickd/config.yaml` |
| root on macOS, as with `sudo` | `/Library/Application Support/kickd/config.yaml` |
| An administrator on Windows, as in a PowerShell opened as administrator | `C:\ProgramData\kickd\config.yaml` |

The config file of root and of administrators is the config for the whole machine, which kickd for the whole machine reads when it runs as a service.
Every subcommand also reads the events of the other `.yaml` and `.yml` files next to the config file that have an `events` section.
The subcommands that work with runs read and write the database of the config file directly, the same database that the agent uses.

## Subcommands

| Subcommand | What it does |
|---|---|
| `kickd run` | Runs the agent in the foreground. A service manager starts the agent with this subcommand too. |
| `kickd check` | Validates the config, prints every error, and lists the files whose events it reads, and the events with their triggers. |
| `kickd init` | Writes an example config file and an example events file, `event.example.yaml`, next to it, for the OS that it runs on: the files that the README shows for that OS. It refuses to overwrite an existing file. |
| `kickd service ACTION` | Manages the service. `ACTION` is `install`, `uninstall`, `start`, `stop`, `restart` or `status`. |
| `kickd event NAME` | Fires an event that has a manual trigger. |
| `kickd events` | Lists the events, the triggers that fire each one, and the file that defines it. |
| `kickd history` | Shows the run history, newest first, so the runs that run or wait come first. `--event`, `--status` and `--limit` filter it, and `--status queued` shows only the runs that wait. |
| `kickd show RUN_ID` | Shows one run: its details, parameters and command output. |
| `kickd cancel RUN_ID` | Cancels a queued run, or stops a running one. |
| `kickd status` | Shows whether the agent is running, and how many runs are waiting. |
| `kickd licenses` | Prints the licenses of kickd and of the third-party software that its executables include. |
| `kickd version` | Prints the version. |
| `kickd help [COMMAND]` | Lists the subcommands, or shows the help of one: its command line, what it does, and its flags. |

`kickd service` works on the service that runs the agent as the same user, with the same config file:

| Who runs kickd service | Service |
|---|---|
| A user on macOS | A LaunchAgent, which launchd starts when the user logs in |
| A user on Linux | A unit of the systemd of the user |
| root on macOS, as with `sudo` | A LaunchDaemon, which launchd starts when the Mac starts |
| root on Linux, as with `sudo` | A system-wide unit of systemd |
| An administrator on Windows | A Windows service, which runs as SYSTEM |

The service is named `kickd`.
On Windows, `kickd service` needs a PowerShell opened as administrator.

`--help` after a subcommand shows the same help as `kickd help` with the name of that subcommand, as in `kickd event --help`.

The run ID is the number in the RUN column of `kickd history`, and the `runId` in the agent's log.
`--json` makes `event`, `events`, `history`, `show` and `status` print JSON.

## Firing an event

`kickd event` fires an event whose triggers include `- type: manual`.
For an event without a manual trigger, it exits with code 2 and names the line to add.
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
| 2 | The arguments were wrong, such as an undefined event, an event without a manual trigger, or an undeclared parameter. |
| 124 | The time given with `--timeout` passed. |

## Parameters

An event declares its parameters under `params`.
An event with `params` accepts only the declared parameters.
A parameter with `required: true` must be given, and a parameter with a `default` takes that value when omitted.
An event without `params` accepts parameters of any name made of letters, digits and `_`.

The command receives each parameter as an environment variable: `ref` arrives as `KICKD_DATA_REF`.
A declared parameter that is left out and has no default arrives empty, so the command gets every declared parameter.
`KICKD_DATA` holds all parameters as one JSON object.
Cron and file triggers cannot pass parameters, so an event with a required parameter cannot have those triggers.
