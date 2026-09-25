# How commands run

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event becomes a **run**.
The long-running kickd process, the **agent**, starts the command of each run as a child process and waits for it to exit.

## Starting the process

The `command` of an event is a string or a list:

- **A list** is the program and its arguments. kickd starts the program directly and passes the arguments as they are. A program name without a path is looked up in the `PATH` of the environment that the command gets, which the event's `env` can change. A relative program path starts at the working directory.
- **A string** is interpreted by a shell. On macOS and Linux, kickd runs `/bin/sh -c` with the string. On Windows, it runs `cmd /S /C "<string>"`, and passes this command line to cmd as it is, so quotes inside the string reach cmd unchanged. A list could not do this on Windows, where the arguments of a list are quoted by the rules of Go, which cmd does not follow.

The command runs in the directory set by `workdir`.
Without `workdir`, it runs in the directory of the config file, whether kickd runs as a service or in a terminal.

## Environment

kickd builds the environment of the command in three layers.
When a name appears in more than one layer, the later layer wins:

1. The environment of the agent.
2. The event's `env`. `${VAR}` in a value expands to the variable `VAR` of the agent.
3. The variables that describe the run: `KICKD_EVENT`, `KICKD_REQUEST_ID`, `KICKD_RUN_ID`, `KICKD_ATTEMPT`, the parameters as `KICKD_DATA_<NAME>`, and the other variables that start with `KICKD_`.

The `KICKD_` variables come last, so `env` cannot override them.

## Payload

The **payload** is the JSON description of a run: the event, the trigger, the parameters, and the details of the firing.
Before it starts the command, kickd writes the payload to a new file named `kickd-payload-<random>.json` in the temporary directory of the OS.
On macOS and Linux, only the user that runs kickd can read the file.
The path of the file is in `KICKD_PAYLOAD_FILE`, and kickd deletes the file when the command exits.
A rerun gets a new file with the new attempt number.

## Standard input

Without `stdin`, the standard input of the command is the null device, so a command that reads its standard input sees the end of the input at once.
With `stdin: payload`, the command reads the payload JSON from its standard input.

## Output

kickd connects the standard output and the standard error of the command to pipes, and reads both as data arrives:

- **Stored output**: both streams go into one buffer line by line, in the order in which the lines end, so a line of one stream never splits a line of the other. A line longer than 8 KB goes in before its end. kickd keeps the first 64 KB. The run record stores this buffer, and a webhook with `wait: true` returns it.
- **Error tail**: kickd keeps the last 20 lines of standard error, up to 4 KB. The log record of a failed run carries them as `stderrTail`.
- **Log records**: at the DEBUG level, every line of output becomes a `Run output` record, cut at 8 KB.

In log records, kickd replaces values that look like passwords or tokens with `***masked***`.
The stored output and the output returned to a webhook are not masked.
With `log_output: false`, kickd neither logs the output nor stores it, and a webhook with `wait: true` still returns it.

## Stopping a command

kickd stops a running command in three situations:

- **Timeout**: the run passed the event's `timeout`. The run becomes `failed`, with the reason `timeout`.
- **Cancel**: `kickd cancel` asked to stop the run. The run becomes `canceled`.
- **Agent stop**: the agent is stopping. The run becomes `interrupted`, and the event's `on_interrupt` decides what happens to it when the agent starts again.

On macOS and Linux, kickd starts every command in its own **process group**, a set of processes that the kernel can signal together.
Child processes of the command join the same group, unless they create groups of their own.
To stop the command, kickd sends SIGTERM to the whole group, so the command and its children can clean up and exit.
If the command has not exited after 10 seconds, kickd kills it with SIGKILL.
Once the command has exited, kickd sends SIGKILL to the group, so no child of a stopped command keeps running.

On Windows, kickd ends the command and all its child processes at once with `taskkill /T /F`.
A service has no reliable way to ask a console program on Windows to exit, so a command on Windows gets no chance to clean up.

## Exit status

kickd records the exit code of the command.
On macOS and Linux, a command ended by a signal has no exit code, so kickd records `-1` and the name of the signal, such as `SIGTERM`.
A command that could not start, for example because the program does not exist, is recorded with exit code `-1` and the reason `start_failed`.

## Processes left in the background

A command can start a background process and exit.
When the background process keeps the standard output or the standard error of the command open, kickd keeps waiting for the pipes to close.
After 10 seconds, kickd closes the pipes itself and records the run as `failed` with the reason `wait_failed`, even though the command exited with code 0.
kickd does not stop the background process.

A command that starts a long-running background process should redirect the output of that process, for example with `nohup server >/dev/null 2>&1 &`, so the pipes close when the command exits.

## A crash of the agent

When the agent itself crashes or is killed, it cannot stop the commands it started.
Each command runs in a process group of its own, so on macOS and Windows, and in a terminal, the commands keep running until they finish.
Under systemd on Linux, the commands belong to the unit of kickd, and systemd stops them when the process of kickd exits.
When the agent starts again, it treats their runs as interrupted, and under `on_interrupt: rerun` a rerun can start while an old command still runs.

## Stopping the service

When the service manager stops kickd, the agent stops every running command and waits for the commands to exit, for at most 30 seconds.
If they have not exited by then, the agent logs `Agent stop timed out` and exits.
The runs of those commands stay `running` in the database, and the next start treats them as cut off by a crash.
