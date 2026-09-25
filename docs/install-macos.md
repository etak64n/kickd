# Installing on macOS

[Documentation index](../README.md#documentation)

On macOS, **launchd** starts and restarts long-running programs.
launchd reads a definition file for each program, starts the program as the file describes, and starts it again when it exits.

A launchd definition that runs with the permissions of a logged-in user is called a **LaunchAgent**.
A LaunchAgent runs while that user is logged in.
`kickd service install --user` installs kickd as a LaunchAgent.

In kickd, a named command in the config file is an **event**, and the long-running kickd process is the **agent**.

## 1. Place the executable

Put the `kickd` executable in `/usr/local/bin`.
`/usr/local/bin` is on the default `PATH` of macOS, so `kickd` works from any directory.

The releases page of kickd has an executable for each kind of Mac: `kickd-darwin-arm64` for Apple silicon, and `kickd-darwin-amd64` for an Intel CPU.
The page also shows the SHA-256 digest of every file.
These commands download the executable for Apple silicon, print its SHA-256 hash to compare with the digest on the page, and install it:

```sh
curl -fLO https://github.com/etak64n/kickd/releases/latest/download/kickd-darwin-arm64
shasum -a 256 kickd-darwin-arm64
sudo mkdir -p /usr/local/bin
sudo install -m 755 kickd-darwin-arm64 /usr/local/bin/kickd
kickd version
```

With Go installed, `go install` builds kickd from source instead:

```sh
go install github.com/etak64n/kickd/cmd/kickd@latest
sudo mkdir -p /usr/local/bin
sudo install -m 755 "$(go env GOPATH)/bin/kickd" /usr/local/bin/kickd
```

The service definition records the path of the executable that installed it.
Move the executable to its final place before installing the service.

macOS adds a **quarantine attribute** (`com.apple.quarantine`) to files downloaded with a browser or received through AirDrop.
Gatekeeper, the macOS check for downloaded programs, blocks a quarantined executable when it starts, because the kickd executables are not signed with an Apple Developer ID.
Files downloaded with `curl`, built on the same Mac, or copied with `scp` have no quarantine attribute.
For a file downloaded with a browser, remove the attribute:

```sh
xattr -d com.apple.quarantine /usr/local/bin/kickd
```

## 2. Create the config file

`kickd init` writes an example config to `~/Library/Application Support/kickd/config.yaml`.
It puts the log of kickd in `~/Library/Logs/kickd/kickd.log`, and the database in `~/Library/Application Support/kickd/kickd.db`.
The example is the macOS config of the README, with four events, one for each kind of trigger.
Delete the events that are not needed, and change the paths to match the Mac.
Watched directories and working directories must exist, so `kickd check` reports paths that do not exist as errors.

```sh
kickd init
"${EDITOR:-vi}" ~/Library/Application\ Support/kickd/config.yaml
kickd check
```

`kickd check` prints every error in the config file.
When there are none, it prints where the log and the database go, and each event and its triggers.

## 3. Try it in the foreground

`kickd run` runs the agent in the terminal.
Ctrl+C stops it.

```sh
kickd run
```

In the foreground, log records appear in the terminal as text, colored by level.
With the example config, the same records are also written as JSON to `~/Library/Application Support/kickd/kickd.log`.

## 4. Run kickd as a LaunchAgent

```sh
kickd service install --user
kickd service start --user
kickd service status --user
```

`install` writes the definition file `~/Library/LaunchAgents/kickd.plist`, which holds the absolute paths of the executable and the config file.
`start` starts the agent, and from then on launchd starts it at every login.
When the agent exits, launchd starts it again.

Every `kickd service` action takes the same `--user` as `install`.
Without `--user`, an action applies to the system-wide definition in `/Library/LaunchDaemons`.

On macOS 13 and later, a "Background Items Added" notification may appear when the service is installed.
When background activity for kickd is turned off in System Settings > General > Login Items & Extensions, launchd does not start kickd.

When the service stops, kickd sends SIGTERM to the commands that are running and waits up to 10 seconds for them to exit.
Commands still running after 10 seconds are stopped with SIGKILL.
The runs stopped this way are recorded as interrupted.
When the agent starts again, it handles each interrupted run as the event's `on_interrupt` setting says: `abandon` gives the run up, and `rerun` starts it again.

## PATH under launchd

The `PATH` of a kickd started by launchd is only `/usr/bin:/bin:/usr/sbin:/sbin`.
Commands installed with Homebrew live in `/opt/homebrew/bin`, so this `PATH` does not find them.

kickd looks up the programs of an event in the `PATH` that the event gives its command, whether the command is a string or a list.
Give the event a `PATH` with `env`:

```yaml
events:
  - name: to-m4a
    command: 'ffmpeg -i "$KICKD_FILE_PATH" "${KICKD_FILE_PATH%.*}.m4a"'
    env:
      PATH: '/opt/homebrew/bin:${PATH}'
    triggers:
      - type: file
        path: ~/Movies/Recordings
        include: ['*.mov']
```

`env` adds environment variables for the command, and `${PATH}` in its values expands to the `PATH` of kickd itself.
`kickd check` prints the `PATH` that each event sets.

## Access to protected folders

macOS protects the Desktop, Documents and Downloads folders, cloud-synced folders such as iCloud Drive and Dropbox, and external and network volumes.
A program needs the user's permission to read these places.
A kickd started by launchd cannot show the permission dialog, so a command that reads a protected place may hang instead of failing with an error.

To watch a protected folder, or to run commands that read or write one, grant kickd Full Disk Access:

1. Open System Settings > Privacy & Security > Full Disk Access.
2. Click "+" and add `/usr/local/bin/kickd`. In the file dialog, Command+Shift+G opens a field for typing the path.
3. Restart kickd with `kickd service stop --user` and `kickd service start --user`.

Commands started by kickd get their access from kickd's permission.
macOS ties the permission to the code signature of the executable.
A new build of kickd has a different signature, so after replacing the executable, remove kickd from the list and add it again.

In cloud-synced folders, file changes are not always reported to programs that watch them.
Before relying on a watch there, put a file in the folder and check that the event fires.

## Logs of the service

The service writes its log as JSON to the file set by `log.path`, which is `~/Library/Application Support/kickd/kickd.log` with the example config.
When the file grows past `log.max_size_mb`, kickd renames it to `kickd.log.1` and keeps up to `log.max_backups` old files.
If kickd fails before it opens the log file, launchd writes that output to `~/kickd.err.log`.
launchd also creates `~/kickd.out.log`.
Both files stay empty while kickd runs normally.

## Updating the executable

```sh
kickd service stop --user
sudo install -m 755 kickd /usr/local/bin/kickd
kickd service start --user
```

Events fired with `kickd event` while the service is stopped, and runs that were waiting, stay in the queue and run after the service starts again.
If kickd had Full Disk Access, remove it from the list and add it again.

## Uninstalling the service

```sh
kickd service uninstall --user
```

`uninstall` stops kickd and deletes the definition file.
The config file, the database and the logs stay.

## Running without a login

To run kickd while no one is logged in, install it with `sudo` and without `--user`.
This kind of launchd definition is called a **LaunchDaemon**.
A LaunchDaemon runs as root from the time the Mac starts, and its definition file is `/Library/LaunchDaemons/kickd.plist`.

A LaunchDaemon runs as root, so `~` in the config file does not refer to the user's home folder.
Write absolute paths in the config file, and give the config file with `-c` when installing.
A config file outside the home folder gets absolute paths from `kickd init`: the log goes to `/Library/Logs/kickd/kickd.log`, and the database to `/Library/Application Support/kickd/kickd.db`.

```sh
sudo kickd init -c "/Library/Application Support/kickd/config.yaml"
sudo kickd service install -c "/Library/Application Support/kickd/config.yaml"
sudo kickd service start
```

If kickd fails to start, the output is written to `/var/log/kickd.err.log`.
For every change, the CI of kickd sets up a LaunchDaemon this way on the macOS 26 machines of GitHub Actions, and a LaunchAgent as well.
It fires an event, checks the user who runs the command, and checks that launchd starts kickd again after a crash.
