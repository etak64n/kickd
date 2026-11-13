# Security policy

kickd runs the commands of its config, as a user or as root and SYSTEM, and it can accept HTTP requests that fire them.
A way to make kickd run a command that its config does not allow, to read what it keeps from other users, or to stop it from working is a vulnerability.

## Reporting a vulnerability

Report a vulnerability privately, with the **Report a vulnerability** button on the [Security tab](https://github.com/etak64n/kickd/security) of the repository, and not in a public issue.
The report stays private until a release fixes the problem.

A report helps most when it gives:

- the output of `kickd version`, and the OS and its version;
- how kickd runs: in a terminal, as a service of one user, or as a service of the whole machine;
- the config, or the part of it that matters, without its tokens and secrets;
- the steps that show the problem, and what an attacker gains.

## Supported versions

Fixes go into the next release.
Before version 1.0, only the latest release receives fixes.

## Outside the scope

kickd runs whatever its config says, with the rights of the user that runs it.
A command in the config that does harm, or a config that its owner made writable by other users, is not a vulnerability of kickd.
kickd refuses a config of the whole machine that other users can change, as [User accounts](https://etak64n.github.io/kickd/guides/users/#the-user-of-the-agent) describes.
