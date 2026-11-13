# User accounts

[Documentation index](index.md)

In kickd, a named command in the config file is an **event**, each firing of an event becomes a **run**, and the long-running kickd process is the **agent**.
The agent starts the command of every run as a child process.

## The user of the agent

The way the agent starts decides which user it runs as:

| How the agent starts | OS | User of the agent |
|---|---|---|
| `kickd run` in a terminal | macOS, Linux | The user of the terminal, or root with `sudo` |
| `kickd run` in PowerShell | Windows | The user of PowerShell, with administrator rights when PowerShell was opened as administrator |
| `kickd service install` without `sudo` | macOS | The user who installed the service. launchd starts the agent when that user logs in. |
| `sudo kickd service install` | macOS | root |
| `kickd service install` without `sudo` | Linux | The user who installed the service, in that user's own instance of systemd |
| `sudo kickd service install` | Linux | root |
| `kickd service install` in an administrator PowerShell | Windows | SYSTEM |

The service definitions that kickd writes name no user, so each service manager applies its default.
The default is root for a system-wide definition of launchd or systemd, the owner for a per-user definition, and the SYSTEM account for a Windows service.
`kickd service install` has no option to choose another user.

The user of the agent also decides which config file the agent reads:

- A user: `~/.kickd/config.yaml` in the home directory of that user.
- root on Linux: `/etc/kickd/config.yaml`.
- root on macOS: `/Library/Application Support/kickd/config.yaml`.
- An administrator or SYSTEM on Windows: `C:\ProgramData\kickd\config.yaml`.

The config file of root, administrators and SYSTEM is the config for the whole machine.
Only root and administrators can change the files in its directory, because its commands run as root or as SYSTEM.

On macOS and Linux, kickd checks this before it reads the config of root, whether as the agent or as a subcommand under `sudo`.
Root must own the config file, every other file of events, every directory on the way to each of them, and the file that each link on the way points to, and neither their group nor other users may write to them.
Otherwise, kickd refuses the config, and `kickd check` names the file or directory that another user can change, with the `sudo chown` or `sudo chmod` command that fixes it.
kickd cannot see the scripts and programs that the commands run, so these need the same protection: a script that another user can change runs as root too.
On Windows, kickd does not check the permissions of the config.

## The user of the commands

A command starts as a child process of the agent and runs as the same user: with the same groups on macOS and Linux, and with the same access token on Windows.
The command also inherits the environment of the agent, so `HOME` or `USERPROFILE` points to the home directory of that user, when the user has one.

On macOS and Linux, an agent that runs as root can run the command of an event as another user, whom the event names with [`user`](settings/events.md#user).
The command and the stop command then run with the user ID, the groups and the home directory of that user.
Windows has no such setting, and the config of any user other than root cannot have it.

The user who fires an event decides which kickd the event reaches, and the agent of that kickd runs the command as its own user, or as the `user` of the event.
`kickd event` reads the config file of the user who runs it, writes a run into the database of that config, and the agent runs the command later.
A run records who fired it in `source`, which the command reads as `KICKD_MANUAL_SOURCE`: the user and host of the `kickd event` process.
Under `sudo`, that user is root.

For example, with kickd installed as a system-wide unit on Linux, `sudo kickd event deploy` makes root run the deploy command, and so does a webhook request from another machine.
`kickd event deploy` without `sudo` reaches the kickd of the user instead, with the config file in the home directory of that user.

## Who can fire an event

Every trigger except cron lets someone other than the user of the agent make the agent run a command as that user:

- **Manual**: for an event with a manual trigger, anyone who can read the config file and read and write the database, with `kickd event`. The database belongs to the user whose kickd process created it, and on macOS and Linux only that user and root can open it.
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
| Config file and events file written by `kickd init` | The user who ran `kickd init` | 0600 |
| Database, with its `-wal` and `-shm` files | The user whose kickd process created it | 0600 |
| Log file | The user of the agent | Allowed by the umask, usually 0644 |
| Payload file of a run | The user of the agent, or the `user` of the event | 0600 |
| Files that a command writes | The user of the agent, or the `user` of the event | Chosen by the command |

A command of an agent that runs as root writes files that belong to root, even in the home directory of another user, unless its event has a `user`.

## Choosing the user

- **A per-user service**, a LaunchAgent on macOS or a per-user unit on Linux, suits events that work on the files of one user. The commands have the permissions of that user, and no more.
- **A system-wide service**, as root on macOS and Linux or as SYSTEM on Windows, suits tasks for the whole machine. Every command in the config file then runs with full rights, unless its event has a `user`, and the config file is the config for the whole machine, which only root and administrators can change.
- **A system-wide service with `user`**, on macOS and Linux, suits a machine where tasks of one user must run without that user logging in. The agent starts at boot as root, and each event with a [`user`](settings/events.md#user) runs with the permissions of that user, and no more.
- **Windows** has only the service that runs as SYSTEM.

The service managers themselves can run a service as another user.
kickd does not set this up, and these settings have not been tested with kickd:

- **systemd**: a `User=` line in the `[Service]` section of the unit file.
- **launchd**: a `UserName` key in the definition file of a LaunchDaemon.
- **Windows**: the Log On tab of the service in the Services list.

kickd then runs as that user, so it reads the config file of that user, `~/.kickd/config.yaml` in the home directory of the user.
The user needs read access to that file, and read and write access to the database and its directory.
Uninstalling the service and installing it again with kickd removes such a change.
