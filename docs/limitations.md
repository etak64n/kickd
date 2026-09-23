# Known limitations

[Documentation index](../README.md#documentation)

In kickd, a named command in the config file is an **event**, and the long-running kickd process is the **agent**.

- **Untested platforms**: the service setup on Linux and Windows, and the LaunchDaemon setup on macOS, have not yet been tested on real machines. The LaunchAgent setup on macOS has been.
- **Paths must exist**: watched directories and `workdir` must exist when the config is loaded. A missing directory is a config error.
- **File descriptors on macOS**: file watching on macOS uses kqueue, which needs one file descriptor for each watched file. Watching a large tree recursively can reach the limit on open file descriptors.
- **Patterns**: `include` and `exclude` patterns do not support `**`.
- **One agent per queue**: several agents cannot share one queue database.
- **Status after a crash**: for about 15 seconds after the agent crashes, `kickd status` still reports it as running. `kickd status` judges whether the agent is alive by a timestamp that the agent writes every 5 seconds.
- **No TLS**: the webhook server speaks plain HTTP only.
- **Unsigned executables**: the release executables are not signed with an Apple Developer ID or a Windows code signing certificate. Gatekeeper on macOS and SmartScreen on Windows may block files downloaded with a browser.
