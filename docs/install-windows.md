# Installing on Windows

[Documentation index](../README.md#documentation)

On Windows, kickd runs as a **Windows service**.
A Windows service is a program that runs from the time Windows starts, whether or not anyone is logged in.
The Service Control Manager starts and stops services.

The kickd service runs as the **SYSTEM account**.
SYSTEM is the account that Windows itself uses, and it has more privileges than an administrator.

In kickd, a named command in the config file is an **event**, and the long-running kickd process is the **agent**.

For every change, the CI of kickd follows these steps from `kickd init` on, on the Windows Server 2025 machines of GitHub Actions.
It fires an event, checks that the command runs as SYSTEM, and checks that Windows starts kickd again after a crash.

## 1. Place the executable

Put the executable at `C:\Program Files\kickd\kickd.exe`.
The releases page of kickd has an executable for each CPU: `kickd-windows-amd64.exe` for x64, and `kickd-windows-arm64.exe` for ARM.
`$env:PROCESSOR_ARCHITECTURE` in PowerShell prints the CPU: `AMD64` calls for amd64, and `ARM64` for arm64.

In PowerShell opened as administrator, these commands download the executable for x64 and add its folder to the system `PATH`:

```powershell
New-Item -ItemType Directory -Force 'C:\Program Files\kickd' | Out-Null
Invoke-WebRequest https://github.com/etak64n/kickd/releases/latest/download/kickd-windows-amd64.exe -OutFile 'C:\Program Files\kickd\kickd.exe'
$p = [Environment]::GetEnvironmentVariable('Path', 'Machine')
[Environment]::SetEnvironmentVariable('Path', "$p;C:\Program Files\kickd", 'Machine')
```

PowerShell windows opened after this find `kickd` by name.

The releases page shows the SHA-256 digest of every file.
This command prints the SHA-256 hash of the downloaded file, which must match the digest on the page:

```powershell
(Get-FileHash 'C:\Program Files\kickd\kickd.exe' -Algorithm SHA256).Hash.ToLower()
```

With Go installed, `go install github.com/etak64n/kickd/cmd/kickd@latest` builds `kickd.exe` into `$(go env GOPATH)\bin` instead.
Copy that file to `C:\Program Files\kickd\kickd.exe`.

The service definition records the path of the executable that installed it.
Move the executable to its final place before installing the service.

Windows marks files downloaded with a browser or received in a chat as coming from the internet.
SmartScreen may block a marked executable when it starts, because the kickd executables are not signed with a code signing certificate.
`Unblock-File` removes the mark, and does nothing to a file without one:

```powershell
Unblock-File 'C:\Program Files\kickd\kickd.exe'
```

## 2. Create the config file

The kickd service runs the commands in its config file as SYSTEM.
Keep the config file in `C:\ProgramData\kickd\config.yaml`, where only administrators can change it.
With the default permissions, other users cannot change files that an administrator creates under `C:\ProgramData`.

```powershell
kickd init -c C:\ProgramData\kickd\config.yaml
notepad C:\ProgramData\kickd\config.yaml
kickd check -c C:\ProgramData\kickd\config.yaml
```

The example is the Windows config of the README, with four events, one for each kind of trigger.
Their commands start the PowerShell scripts `backup.ps1`, `deploy.ps1`, `build.ps1` and `notify.ps1` in the working folders of the events, so write those scripts or replace the commands.
Watched directories and working directories must exist, so `kickd check` reports paths that do not exist as errors.

kickd records every run in its **database**, a SQLite file.
The config file is outside the home folder, so `kickd init` puts the database at `C:\ProgramData\kickd\kickd.db`, and the log at `C:\ProgramData\kickd\kickd.log`, next to the config file.
A database created by the service belongs to administrators and SYSTEM, so run `kickd event` and the other commands that read or write it in an administrator PowerShell as well.

Write Windows paths in single quotes, as the examples do: in single quotes, a backslash (`\`) is an ordinary character.
In double quotes, a backslash starts an escape sequence, so a double-quoted `C:\Data\Import` fails to load with `found unknown escape character`.
A path without quotes, or with forward slashes, also works:

```yaml
# Any one of these forms works.
path: 'C:\Data\Import'
path: C:\Data\Import
path: C:/Data/Import
```

## 3. Try it in the foreground

```powershell
kickd run -c C:\ProgramData\kickd\config.yaml
```

Ctrl+C stops it.
In the foreground, log records appear in PowerShell as text, colored by level.
With the example config, the same records are also written as JSON to `C:\ProgramData\kickd\kickd.log`.
Once kickd runs as a service, this command follows the log file:

```powershell
Get-Content C:\ProgramData\kickd\kickd.log -Wait -Tail 20
```

In the foreground, kickd runs commands as the user who opened PowerShell.
The service runs them as SYSTEM, so an event that works in the foreground can behave differently in the service.

## 4. Run kickd as a Windows service

In PowerShell opened as administrator:

```powershell
kickd service install -c C:\ProgramData\kickd\config.yaml
kickd service start
kickd service status
```

The installed service starts automatically when Windows starts.
If the agent exits unexpectedly, the Service Control Manager starts it again after 5 seconds.
The Services list (`services.msc`) shows the service as "kickd (event-driven command runner)".

When the config file is saved, the agent reloads it.

When the service stops, kickd ends the commands that are running, together with their child processes.
Windows gives kickd no reliable signal to ask a command to exit, so the commands end at once, without a chance to clean up.
The runs stopped this way are recorded as interrupted.
When the agent starts again, it handles each interrupted run as the event's `on_interrupt` setting says: `abandon` gives the run up, and `rerun` starts it again.

Windows has no per-user form of service.
kickd ignores `--user` on Windows and installs a service that runs as SYSTEM.

## The environment of commands run as SYSTEM

Commands run as SYSTEM see a different environment from a logged-in user's:

- **Home folder**: `~` and `%USERPROFILE%` point to `C:\Windows\system32\config\systemprofile`. Write absolute paths in the config file.
- **PATH**: only the system `PATH` applies, so tools added to a user's `PATH` are not found by name. Add their folders to `PATH` with the event's `env`, or give the program as an absolute path.
- **Working directory**: commands of events without `workdir` run in the folder of the config file, such as `C:\ProgramData\kickd`.
- **Network drives**: drive letters mapped by a user do not exist for SYSTEM, and a user's saved credentials are not used for shared folders.

## Accepting webhooks from other machines

When `webhook.listen`, the address that the webhook server listens on, is not a loopback address, Windows Defender Firewall blocks connections from other machines.
To accept them, add a rule that allows incoming connections to kickd:

```powershell
New-NetFirewallRule -DisplayName kickd -Direction Inbound -Program 'C:\Program Files\kickd\kickd.exe' -Action Allow
```

The webhook server speaks plain HTTP without TLS, so tokens and request bodies cross the network unencrypted.
Accept connections from other machines only on a trusted network, or put a reverse proxy that handles TLS in front of kickd.

## Uninstalling the service

```powershell
kickd service stop
kickd service uninstall
```

`uninstall` deletes the service definition.
It leaves a running agent running, so stop the agent first.
