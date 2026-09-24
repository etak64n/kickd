# User accounts

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, each firing of an event becomes a **run**, and the long-running kickd process is the **agent**.
The agent starts the command of every run as a child process.

## The user of the agent

The way the agent starts decides which user it runs as:

| How the agent starts | OS | User of the agent |
|---|---|---|
| `kickd run` in a terminal | macOS, Linux | The user of the terminal, or root with `sudo` |
| `kickd run` in PowerShell | Windows | The user of PowerShell, with administrator rights when PowerShell was opened as administrator |
| `kickd service install --user` | macOS | The user who installed the service. launchd starts the agent when that user logs in. |
| `sudo kickd service install` | macOS | root |
| `kickd service install --user` | Linux | The user who installed the service, in that user's own instance of systemd |
| `sudo kickd service install` | Linux | root |
| `kickd service install` in an administrator PowerShell | Windows | SYSTEM |

The service definitions that kickd writes name no user, so each service manager applies its default.
The default is root for a system-wide definition of launchd or systemd, the owner for a per-user definition, and the SYSTEM account for a Windows service.
`kickd service install` has no option to choose another user.

## The user of the commands

kickd never switches users.
A command starts as a child process of the agent and runs as the same user: with the same groups on macOS and Linux, and with the same access token on Windows.
The command also inherits the environment of the agent, so `HOME` or `USERPROFILE` points to the home directory of that user, when the user has one.

The user who fires an event does not matter.
`kickd event` only writes a run into the database, and the agent runs the command later as its own user.
A run records who fired it in `source`, which the command reads as `KICKD_SOURCE`: the user and host of the `kickd event` process.
Under `sudo`, that user is root.

For example, with kickd installed as a system-wide unit on Linux, `sudo kickd event deploy` makes root run the deploy command, and so does a webhook request from another machine.

## Who can fire an event

Every trigger except cron lets someone other than the user of the agent make the agent run a command as that user:

- **`kickd event`**: anyone who can read the config file and read and write the database. The database belongs to the user whose kickd process created it, and on macOS and Linux only that user and root can open it.
- **Webhook**: anyone who can reach the webhook server and knows the `token` or `secret` of the trigger. When the trigger has neither, anyone who can reach the server.
- **File**: anyone who can create, change, remove or rename files in the watched directory.
- **Cron**: nobody, because the schedule fires by itself.

Values that these people control reach the command as environment variables: parameters as `KICKD_DATA_<NAME>`, and file names as `KICKD_FILE_PATH`.
A command should treat them as untrusted input.
In sh, quote them, as in `"$KICKD_FILE_PATH"`, because an unquoted file name with spaces splits into several arguments.
On Windows, cmd expands `%VAR%` before it parses the line, so a value that contains `&` can start another command; a PowerShell script that reads `$env:KICKD_FILE_PATH` avoids this.

## Files that kickd creates

| File | Owner | Permissions on macOS and Linux |
|---|---|---|
| Config file written by `kickd init` | The user who ran `kickd init` | 0600 |
| Database, with its `-wal` and `-shm` files | The user whose kickd process created it | 0600 |
| Log file | The user of the agent | Allowed by the umask, usually 0644 |
| Payload file of a run | The user of the agent | 0600 |
| Files that a command writes | The user of the agent | Chosen by the command |

A command of an agent that runs as root writes files that belong to root, even in the home directory of another user.

## Choosing the user

- **A per-user service**, a LaunchAgent on macOS or a per-user unit on Linux, suits events that work on the files of one user. The commands have the permissions of that user, and no more.
- **A system-wide service**, as root on macOS and Linux or as SYSTEM on Windows, suits tasks for the whole machine. Every command in the config file then runs with full rights, so only root or administrators should be able to change the config file.
- **Windows** has only the service that runs as SYSTEM.

The service managers themselves can run a service as another user.
kickd does not set this up, and these settings have not been tested with kickd:

- **systemd**: a `User=` line in the `[Service]` section of the unit file.
- **launchd**: a `UserName` key in the definition file of a LaunchDaemon.
- **Windows**: the Log On tab of the service in the Services list.

The user then needs read access to the config file, and read and write access to the database and its directory.
Uninstalling the service and installing it again with kickd removes such a change.

A command of an agent that runs as root can also switch users by itself, for example with `sudo -u alice ./task.sh` on macOS or `runuser -u alice -- ./task.sh` on Linux.
