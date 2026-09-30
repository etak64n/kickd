# Quick start

[Documentation index](index.md)

In kickd, a named command in the config file is an **event**, and firing an event requests one run of its command.
The long-running kickd process, called the **agent**, runs the commands.

kickd is one executable, which the [releases page](https://github.com/etak64n/kickd/releases/latest) has for each platform, and the first step of the installation guide of each OS puts it on the `PATH`.
With `kickd` on the `PATH`, these steps define an event, run the agent in a terminal and fire the event.

1. Create the config file `~/.kickd/config.yaml`, `%USERPROFILE%\.kickd\config.yaml` on Windows, with one event, `hello`.
   kickd reads this file for the user who runs it, so run the steps without `sudo`, and on Windows in a PowerShell that was not opened as administrator.
   Without `database.path`, kickd keeps its database, `kickd.db`, next to the config file.

   ```yaml
   events:
     - name: hello
       command: 'echo hello from kickd'
       triggers:
         - type: manual   # kickd event hello fires it
   ```

2. Start the agent in the foreground.
   Ctrl+C stops it.

   ```sh
   kickd run
   ```

3. In another terminal, fire the event.
   With `--wait`, `kickd event` waits for the run to finish and prints the result.

   ```sh
   kickd event hello --wait
   ```

   ```text
   RUN  EVENT  ATTEMPT  STATUS     EXIT  DURATION  DETAIL
   1    hello  1        succeeded  0     12ms      -
   ```

4. Show the run, including the output of its command.

   ```sh
   kickd show 1
   ```

To keep kickd running in the background, install it as a service by following the guide for the OS: [macOS](install-macos.md), [Linux](install-linux.md) or [Windows](install-windows.md).
