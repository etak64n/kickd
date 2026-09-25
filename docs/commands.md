# Running commands

[Documentation index](../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event is recorded as a **run**.
kickd starts the command of each run as a child process.
A `command` given as a string runs with `/bin/sh -c` on macOS and Linux and with `cmd /S /C` on Windows, and a list starts its program directly.

## The environment of a command

A command gets three layers of environment variables, each on top of the one before:

1. **The environment of kickd**, which depends on how kickd was started.
2. **The event's `env`**, which adds variables and replaces variables of kickd that have the same name. `${VAR}` in a value is the variable `VAR` of kickd.
3. **The variables of the run**, such as `KICKD_EVENT`, `KICKD_RUN_ID` and `KICKD_DATA_REF`, which replace variables of the same name from the other layers. [What a command receives](payload.md) lists them.

In a terminal, `kickd run` has the environment of the shell that started it.
A service gets the environment of its service manager instead, which holds far fewer variables:

| Started by | `PATH` | Other variables |
|---|---|---|
| launchd, as a LaunchAgent on macOS | `/usr/bin:/bin:/usr/sbin:/sbin` | `HOME`, `USER`, `LOGNAME`, `SHELL` and `TMPDIR` of the user. `LANG` is not set. |
| systemd, as a system-wide unit on Linux | `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin` | `LANG` from the locale of the system. `HOME` and `USER` are not set, because the unit has no `User=` setting. |
| systemd, as a per-user unit on Linux | The same as a system-wide unit | `HOME`, `USER` and `XDG_RUNTIME_DIR` of the user |
| A Windows service | The system `PATH` | The variables of the SYSTEM account: `USERPROFILE` is `C:\Windows\system32\config\systemprofile`, and `TEMP` is `C:\Windows\TEMP`. |

So a program that works in a terminal can fail as a service, because the service does not find it or misses a variable that the terminal had.
An event that prints its environment shows what its commands get:

```yaml
events:
  - name: show-env
    command: ['env']   # on Windows: command: 'set'
    triggers:
      - type: manual
```

`kickd event show-env --wait` fires it, and `kickd show` with the printed run ID displays the output.

## Where commands run

- **Working directory**: `workdir` is the directory in which the command runs. Without it, the command runs in the directory of the config file. A program or script given with a relative path, such as `./deploy.sh` or `.venv/bin/python`, starts at the working directory.
- **Programs**: a program name without a path, such as `python3`, is looked up in the `PATH` of the command's environment, for a string command as for a list. An event puts the directories of its programs in front of that `PATH` with `env`.

`kickd check` prints the working directory of each event, and the `PATH` that an event sets.
In a list, kickd passes every element as it is, so `~` and `$VAR` in the arguments stay unexpanded; `workdir` and a relative program cover most needs, and a string command expands them.

## Python

A script run with the Python on the `PATH`:

```yaml
events:
  - name: report
    command: ['python3', 'report.py', '--daily']
    workdir: '~/reports'
    env:
      PYTHONUNBUFFERED: '1'
    triggers:
      - type: manual
```

Python writes its output in blocks when the output goes to a pipe, as it does under kickd, so the lines of a long run reach kickd's log only at the end.
`PYTHONUNBUFFERED: '1'` makes Python write each line at once, as `python3 -u` does.

A virtual environment needs no activation: its own `python` runs the script with the packages of the environment.
The program path is relative, so it starts at the working directory:

```yaml
events:
  - name: report
    command: ['.venv/bin/python', 'report.py']   # on Windows: '.venv\Scripts\python.exe'
    workdir: '~/reports'
    triggers:
      - type: manual
```

With uv, `uv run` picks the environment of the project.
The installer of uv puts it in `~/.local/bin`, which a service does not have on its `PATH`:

```yaml
events:
  - name: report
    command: ['uv', 'run', 'report.py']
    workdir: '~/reports'
    env:
      PATH: '${HOME}/.local/bin:${PATH}'
    triggers:
      - type: manual
```

On Windows, the Python launcher `py` is in the system `PATH` when Python was installed for all users, so `command: ['py', '-3', 'report.py']` works for a service as well.

A script reads the parameters of the firing from `KICKD_DATA_<NAME>`, and the whole payload from the JSON file at `KICKD_PAYLOAD_FILE`:

```python
import json
import os

ref = os.environ.get("KICKD_DATA_REF", "main")
with open(os.environ["KICKD_PAYLOAD_FILE"], encoding="utf-8") as f:
    payload = json.load(f)
print(f"{payload['event']}: run {payload['runId']}, ref {ref}")
```

## Node.js

A script run with `node`:

```yaml
events:
  - name: build-site
    command: ['node', 'build.mjs']
    workdir: '~/site'
    env:
      PATH: '/opt/homebrew/bin:${PATH}'   # where Homebrew puts node on a Mac with Apple silicon
    triggers:
      - type: manual
```

`npm` and `npx` are Node.js scripts themselves, so the `PATH` must hold the directory of `node` for them as well:

```yaml
events:
  - name: build-site
    command: ['npm', 'run', 'build']
    workdir: '~/site'
    env:
      PATH: '/opt/homebrew/bin:${PATH}'
    triggers:
      - type: manual
```

nvm adds the directory of the selected Node.js version to `PATH` in the startup files of the shell, which a service does not read.
Give that directory in `env` instead, such as `PATH: '${HOME}/.nvm/versions/node/v22.11.0/bin:${PATH}'`, and change it after installing another version.
Volta keeps its shims in `~/.volta/bin`, which does not change between versions.

On Windows, the installer of Node.js puts `node.exe` in the system `PATH`, so `command: ['node', 'build.mjs']` works for a service.
`npm` is the batch file `npm.cmd` there, which runs through cmd, so give it as a string, `command: 'npm run build'`.

A script reads the parameters and the payload in the same way as in Python:

```js
import { readFileSync } from 'node:fs';

const ref = process.env.KICKD_DATA_REF ?? 'main';
const payload = JSON.parse(readFileSync(process.env.KICKD_PAYLOAD_FILE, 'utf8'));
console.log(`${payload.event}: run ${payload.runId}, ref ${ref}`);
```
