# Platform differences

[Documentation index](../../README.md#documentation)

kickd runs the same code on macOS, Linux and Windows, except where the operating systems differ.
In kickd, a named command in the config file is an **event**, each firing of an event becomes a **run**, and the long-running kickd process is the **agent**.

## Commands and processes

| Item | macOS and Linux | Windows |
|---|---|---|
| Shell that runs `shell` | `/bin/sh -c` | `cmd /S /C` |
| Process group | Each command gets a process group of its own | Each command gets a process group of its own, which keeps the Ctrl+C of the console away from it |
| Stopping a command | SIGTERM to the group, then SIGKILL after 10 seconds | `taskkill /T /F` at once |
| Exit status | The exit code, or `-1` and the signal name | The exit code |
| Separator in `KICKD_FILE_PATHS` | `:` | `;` |
| Directory of payload files | `$TMPDIR`, or `/tmp` when it is not set | The temporary folder of the account, from `%TMP%` or `%TEMP%` |

## Signals

On macOS and Linux, the agent stops on SIGINT and SIGTERM, and reloads its config on SIGHUP.
Windows has no such signals.
A Windows service stops through the Service Control Manager, and a kickd in a console stops on Ctrl+C.
On every OS, the agent also reloads its config when the file is saved.

## File watching

kickd watches files with the fsnotify library, which uses a different interface of the kernel on each OS:

| OS | Interface | Unit of watching | Limit |
|---|---|---|---|
| Linux | inotify | Each directory | `fs.inotify.max_user_watches` watches per user, and `fs.inotify.max_queued_events` changes waiting to be read |
| macOS | kqueue | Each file and each directory | The limit on open files of the process |
| Windows | ReadDirectoryChangesW | Each directory | A fixed buffer of changes for each directory |

kickd asks fsnotify to watch one directory at a time.
With `recursive: true`, kickd walks the tree when it starts, and adds a watch for every directory in it.
When a directory is created later, kickd watches it as well, and reports the files already inside it as created, because they may have been written before the watch was in place.

kqueue needs an open file descriptor for every file that it watches, so a large tree on macOS uses many descriptors.
The Go runtime raises the soft limit on open files to the hard limit when kickd starts, and kickd logs the resulting limit at the DEBUG level as `Open file limit`.

On Linux and Windows, when changes arrive faster than kickd reads them, the kernel drops some of them, and kickd logs `File watch error` with the reason `event_overflow`.
kickd also keeps at most 10000 changes for one debounce window, and logs `File changes dropped, too many pending` beyond that.

Network file systems, such as SMB and NFS shares, report changes made on other machines unreliably or not at all.
Cloud-synced folders can apply changes in ways that the kernel does not report as ordinary writes.

## Services

kickd installs itself as a service through the kardianos/service library:

| Item | macOS: launchd | Linux: systemd | Windows: Service Control Manager |
|---|---|---|---|
| Definition | `~/Library/LaunchAgents/kickd.plist`, or `/Library/LaunchDaemons/kickd.plist` | `~/.config/systemd/user/kickd.service`, or `/etc/systemd/system/kickd.service` | An entry in the service database of Windows |
| Account | The user, or root | The user, or root | SYSTEM |
| Starts | At login, or at boot | At login, or at boot | At boot |
| Restart after an exit | At once, but launchd starts a job at most once every 10 seconds | After 2 minutes, from `Restart=always` and `RestartSec=120` | After 5 seconds, from the recovery settings of the service |
| Working directory | The directory of the config file | The directory of the config file | `C:\Windows\System32` |
| Output before the log file opens | `~/kickd.err.log`, or `/var/log/kickd.err.log` | journald | Not kept |

The environment of a service differs from the environment of a terminal:

- **macOS**: launchd sets `PATH` to `/usr/bin:/bin:/usr/sbin:/sbin`, so programs from Homebrew are not found by name.
- **Linux**: a system-wide unit has no `User=` setting, so systemd does not set `HOME`, and `~` in the config file stays unexpanded. The unit that kickd writes reads extra environment variables from `/etc/sysconfig/kickd` when that file exists.
- **Windows**: SYSTEM has its own profile, `C:\Windows\system32\config\systemprofile`, and uses only the system `PATH`. Drive letters mapped by a user do not exist for it.

When a service stops, the agent stops its commands and waits for them for at most 30 seconds.
When the agent crashes instead, only systemd stops the commands that the agent started.
Under launchd and on Windows, those commands keep running without the agent.

## Permissions and privacy

On macOS and Linux, `kickd init` creates the config file with the permission 0600, and a new queue database, with its `-wal` and `-shm` files, gets 0600 as well, because both can hold secrets and command output.
The log file is created with the permissions that the umask allows, usually 0644, so other users can read it.
On Windows, the files take the permissions of their folder, and files that an administrator creates under `C:\ProgramData` cannot be changed by other users.

macOS guards some locations, such as the Desktop, Documents and Downloads folders and removable volumes, with a privacy framework called TCC.
macOS checks the access of a command against the permission of kickd, the program that started it, so kickd needs Full Disk Access to watch or process those locations.
A kickd started by launchd cannot show the dialog that asks for permission, so an access without permission can hang instead of failing.

## Time zones

The `timezone` of a cron trigger names a zone of the IANA time zone database, such as `Asia/Tokyo`.
Windows has no copy of this database, and minimal Linux systems may lack one, so kickd carries its own copy inside the executable.
The copy adds about 450 KB to the executable, and kickd uses it only when the OS has no database.

## Paths

- **Home directory**: `~` expands to `$HOME` on macOS and Linux, and to `%USERPROFILE%` on Windows. Without a home directory, as in a systemd unit without `User=`, `~` stays unexpanded.
- **Environment variables**: `${VAR}` expands in paths and in `env` values on every OS. `%VAR%` expands only inside a `shell` string on Windows, where cmd expands it.
- **Backslashes**: in YAML, a backslash inside double quotes starts an escape sequence, so Windows paths go in single quotes, go without quotes, or use forward slashes.
