# Database settings

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event is recorded as a **run**.
kickd records every run in its **database**, a SQLite file: runs that wait, runs that are running, and runs that have ended.
The long-running kickd process, called the **agent**, starts the commands of the waiting runs.
The other subcommands, such as `kickd event`, `kickd runs` and `kickd cancel`, read and write the same file, which is why `kickd event` works while the agent is stopped.

The `database` section of the config file sets where the file is, and how long ended runs stay in it:

```yaml
database:
  path: '~/.local/state/kickd/kickd.db'
  retention: 168h
```

The agent opens the database once, when it starts, and keeps it until it stops.
A change to the `database` section therefore takes effect at the next start of the agent.

## `database.path`

`database.path` is the SQLite file of the database.

- **Type**: a path. A leading `~` is the home directory, `${VAR}` is the value of the environment variable `VAR`, and a relative path starts at the directory of the config file.
- **Default**: `kickd.db` in the directory of the config file.
- **Takes effect**: at the next start of the agent.

kickd creates the file, and the directories on its path, when they do not exist.
The file gets the permissions 0600 on macOS and Linux, so only its owner can read and write it.
SQLite keeps two more files next to it, with `-wal` and `-shm` appended to the name, and kickd gives them the same permissions.

The agent and every subcommand that uses the same config file use this database, so each of them must run as a user that can read and write it.
A database that a service of the whole system created belongs to root, or to administrators and SYSTEM on Windows, so the subcommands for that service run with sudo or in an administrator shell.

A change to `database.path` reaches the subcommands at once, because they read the config file each time they run, while the agent keeps the file that it opened.
Until the agent starts again, `kickd event` writes its runs to the new file and the agent reads the old one, so restart the agent right after the change.
When the agent reloads a config with another `database.path`, it logs the warning `Database path change needs a restart`.
The runs recorded so far stay in the old file.
To keep them, stop the agent, move the file to the new path together with its `-wal` and `-shm` files when they exist, change `database.path`, and start the agent.

`kickd check` prints the resolved path in its `database:` line.

`kickd init` writes a `database.path` at the usual place of the OS.
A config file inside the home directory is for a user's kickd, and one outside it is for a service of the whole system:

| Config file | macOS | Linux | Windows |
|---|---|---|---|
| Inside the home directory | `~/Library/Application Support/kickd/kickd.db` | `~/.local/state/kickd/kickd.db` | `~\AppData\Local\kickd\kickd.db` |
| Outside the home directory | `/Library/Application Support/kickd/kickd.db` | `/var/lib/kickd/kickd.db` | `C:\ProgramData\kickd\kickd.db` |

```yaml
database:
  path: '/var/lib/kickd/kickd.db'
```

## `database.retention`

`database.retention` is how long an ended run stays in the database.

- **Type**: a duration: a number with one of the units `ms`, `s`, `m` and `h`, such as `90m` or `720h`. Days are not a unit, so 30 days is `720h`.
- **Default**: `168h`, which is 7 days. The value `0s` also means 168 hours.
- **Takes effect**: at the next start of the agent.

The agent deletes the runs that ended longer ago than this duration: when it starts, and then once an hour.
Runs that wait, run, or were interrupted and wait for the next start of the agent stay, however old they are.
A deleted run no longer appears in `kickd runs`, and `kickd show` reports it as not found.
A negative duration is an error of the config file.

```yaml
database:
  retention: 720h
```
