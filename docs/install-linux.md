# Installing on Linux

[Documentation index](index.md)

On Linux with systemd, kickd runs as a systemd **unit**.
A unit is a definition file that tells systemd how to start, stop and restart a program.

In kickd, a named command in the config file is an **event**, and the long-running kickd process is the **agent**.

## 1. Place the executable

Put the `kickd` executable in `/usr/local/bin`.

The releases page of kickd has an executable for each CPU: `kickd-linux-amd64` for x86-64, and `kickd-linux-arm64` for 64-bit ARM.
`uname -m` prints the CPU: `x86_64` calls for amd64, and `aarch64` for arm64.
These commands download the executable for x86-64 and install it:

```sh
curl -fLO https://github.com/etak64n/kickd/releases/latest/download/kickd-linux-amd64
sudo install -m 755 kickd-linux-amd64 /usr/local/bin/kickd
kickd version
```

With Go installed, `go install` builds kickd from source instead:

```sh
go install github.com/etak64n/kickd/cmd/kickd@latest
sudo install -m 755 "$(go env GOPATH)/bin/kickd" /usr/local/bin/kickd
```

The unit file records the path of the executable that installed it.
Move the executable to `/usr/local/bin` before installing the service.

## 2. Create the config file

A kickd installed as a system-wide unit runs as root, and runs the commands in its config as root.
Run as root, kickd reads the config for the whole machine, `/etc/kickd/config.yaml`, in a directory that only root can change.
With `sudo`, `kickd init` writes two files there, each readable and writable only by its owner: the config file `config.yaml`, with the settings of kickd, and `event.example.yaml`, with four events, one for each kind of trigger.

```sh
sudo kickd init
sudoedit /etc/kickd/event.example.yaml
sudo kickd check
```

The events are those of the README.
Delete the events that are not needed, and change the paths to match the machine.
kickd reads the events of every `.yaml` and `.yml` file in `/etc/kickd` that has an `events` section, so events can also go in files of their own, such as `/etc/kickd/backup.yaml`.
Watched directories and working directories must exist, so `kickd check` reports paths that do not exist as errors.

kickd records every run in its **database**, a SQLite file.
For the config for the whole machine, `kickd init` puts the files where Linux keeps them for a service: the log in `/var/log` and the database in `/var/lib`, where data that changes lives:

```yaml
log:
  path: '/var/log/kickd/kickd.log'
database:
  path: '/var/lib/kickd/kickd.db'
```

kickd creates the directories when it first opens the files.
With `sudo`, every kickd command reads the config for the whole machine and its database, which belongs to root.
Run `kickd event` and the other commands with `sudo` as well, such as `sudo kickd event deploy` and `sudo kickd history`.

systemd collects the standard error of units with **journald**, and `journalctl` reads what journald collected.
Without `log.path`, kickd writes its log to standard error, so deleting the `path` line of `log` sends the log to journald.
The records sent to journald are JSON.
With `format: text`, they are tab-separated text.
With both, journald and the file get the same records.

systemd sets `HOME` only for units with a `User=` setting, and the unit that kickd writes has none.
Without `HOME`, `~` in the config file is not expanded.
Write absolute paths in the config file, and pass `HOME` with the event's `env` to commands that need it.
The unit that kickd writes also reads environment variables from `/etc/sysconfig/kickd` when that file exists, so a line `HOME=/root` there gives the whole agent a home directory.

## 3. Try it in the foreground

```sh
sudo kickd run
```

Ctrl+C stops it.
In the foreground, log records appear in the terminal as text.
When `log.path` is set, the same records are also written to that file as JSON.

## 4. Run kickd as a system-wide unit

```sh
sudo kickd service install
sudo kickd service start
systemctl status kickd
journalctl -u kickd -f
```

`install` writes the unit file `/etc/systemd/system/kickd.service` and enables it, so the agent starts when Linux boots.
When the agent process exits, systemd starts it again after 5 seconds.
After `systemctl stop`, systemd leaves it stopped.

When the config file, or another YAML file next to it, is saved, the agent reloads the config.
`sudo systemctl reload kickd` also reloads it, because `reload` sends SIGHUP to the agent, and the agent reloads its config on SIGHUP.

When the service stops, kickd sends SIGTERM to the commands that are running and waits up to 10 seconds for them to exit.
Commands still running after 10 seconds are stopped with SIGKILL.
The runs stopped this way are recorded as interrupted.
When the agent starts again, it handles each interrupted run as the event's `on_interrupt` setting says: `abandon` gives the run up, and `rerun` starts it again.

## Running kickd as a per-user unit

systemd also runs one instance for each logged-in user.
A unit registered with a user's instance is called a **per-user unit**.
A per-user unit runs with that user's permissions, and systemd sets `HOME` for it.
`kickd service install`, run without `sudo`, installs kickd as a per-user unit of the user who runs it: it writes `~/.config/systemd/user/kickd.service` and enables the unit, so the unit starts when the user's instance of systemd starts.

A user's instance of systemd normally runs only while the user is logged in.
`loginctl enable-linger` keeps it running from boot, whether or not the user is logged in.

A per-user unit runs as the user, so it reads its config from `~/.kickd`, where `kickd init` writes `config.yaml` and `event.example.yaml`, and the config puts the log and the database in `~/.kickd` as well.

```sh
kickd init
"${EDITOR:-vi}" ~/.kickd/event.example.yaml
kickd check
sudo loginctl enable-linger "$USER"
kickd service install
kickd service start
journalctl --user -u kickd -f
```

`kickd service` without `sudo` works on the per-user unit, and with `sudo` on the system-wide unit.

## Uninstalling the service

```sh
sudo kickd service stop
sudo kickd service uninstall
```

`uninstall` disables the unit and deletes the unit file.
It leaves a running agent running, so stop the agent first.
For a per-user unit, run both commands without `sudo`.

## Limit on watched directories

On Linux, kickd watches files with the kernel's **inotify**.
inotify uses one watch for each watched directory.
The number of watches per user is limited by `fs.inotify.max_user_watches`.
Directories beyond the limit are not watched, and kickd logs a warning with their number.
Before watching a large tree recursively, check the limit and raise it:

```sh
sysctl fs.inotify.max_user_watches
echo fs.inotify.max_user_watches=524288 | sudo tee /etc/sysctl.d/90-kickd.conf
sudo sysctl --system
```

## Where these steps are tested

For every change, the CI of kickd follows these steps from `kickd init` on, on the Ubuntu 24.04 machines of GitHub Actions, for both a system-wide unit and a per-user unit.
It fires an event, checks the user who runs the command, and checks that systemd starts kickd again after a crash.
