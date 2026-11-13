---
title: Known limitations
description: What kickd does not do yet, and what has not been tested.
---

In kickd, a named command in the config file is an **event**, and the long-running kickd process is the **agent**.

- **Tested systems**: the CI installs kickd as a service on the Ubuntu 24.04, macOS 26 and Windows Server 2025 machines of GitHub Actions. Other Linux distributions and the desktop versions of Windows have not been tried.
- **Paths must exist**: watched directories and `workdir` must exist when the config is loaded. A missing directory is a config error.
- **File descriptors on macOS**: file watching on macOS uses kqueue, which needs one file descriptor for each watched file. Watching a large tree recursively can reach the limit on open file descriptors.
- **Patterns**: `include` and `exclude` patterns do not support `**`.
- **Status after a crash**: for about 15 seconds after the agent crashes, `kickd status` still reports it as running. `kickd status` judges whether the agent is alive by a timestamp that the agent writes every 5 seconds.
- **No TLS**: the webhook server speaks plain HTTP only.
- **Permissions on Windows**: kickd checks that only root can change the config of root on macOS and Linux, and does not check the permissions of the config on Windows.
- **Changes before version 1.0**: a minor release, such as 0.9 after 0.8, can change the config, the command line and the database in ways that the previous release does not accept. The notes of each release on GitHub say what changed and how to upgrade.
- **Unsigned executables**: the release executables are not signed with an Apple Developer ID or a Windows code signing certificate. Gatekeeper on macOS and SmartScreen on Windows may block files downloaded with a browser.
