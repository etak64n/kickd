# Quick start

[Documentation index](index.md)

In kickd, a named command in the config file is an **event**, and firing an event requests one run of its command.
The long-running kickd process, called the **agent**, runs the commands.

kickd is one executable, which the [releases page](https://github.com/etak64n/kickd/releases/latest) has for each platform, and the first step of the installation guide of each OS puts it on the `PATH`.
With `kickd` on the `PATH`, these steps define an event, run the agent in a terminal and fire the event.

1. In an empty directory, create a config file named `kickd.yaml` that defines one event, `hello`.
   Without `database.path`, kickd keeps its database, `kickd.db`, next to `kickd.yaml`.

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
   kickd run -c kickd.yaml
   ```

3. In another terminal, fire the event.
   With `--wait`, `kickd event` waits for the run to finish and prints the result.

   ```sh
   kickd event hello --wait -c kickd.yaml
   ```

   ```text
   RUN  EVENT  ATTEMPT  STATUS     EXIT  DURATION  DETAIL
   1    hello  1        succeeded  0     12ms      -
   ```

4. Show the run, including the output of its command.

   ```sh
   kickd show 1 -c kickd.yaml
   ```

To keep kickd running in the background, install it as a service by following the guide for the OS: [macOS](install-macos.md), [Linux](install-linux.md) or [Windows](install-windows.md).
