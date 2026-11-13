---
title: Development
description: Tests, the documentation site, and the source layout of kickd.
---

kickd needs Go 1.25 or later to build and test.

```sh
go test ./...              # run the tests
go test -race ./...        # run the tests with the race detector
go vet ./...               # static checks
./scripts/build-all.sh     # build kickd for six platforms into dist/
```

## Checks on GitHub Actions

For every push to `main` and every pull request, the CI workflow runs these checks:

- **Unit tests** and `go vet` on Linux, macOS and Windows, and the unit tests again with the race detector on Linux.
- **End-to-end tests** of each OS on that OS, then the end-to-end tests that change the machine.
- **End-to-end tests with the race detector**: the end-to-end tests of Linux once more, with kickd built with `-race`, so that the detector watches the agent itself.
- **Staticcheck**, a checker for bugs that `go vet` does not look for, for the three OSes.
- **Fuzzing** of the config parser and of the webhook handler, for 30 seconds each: generated configs and requests must not crash them, every webhook path of a config that the parser accepts must register with the server, and no request may fire an event without its token or signature.
- **Builds** of kickd for every platform, and the tests with the oldest Go version that `go.mod` allows.

govulncheck, the vulnerability checker of the Go team, checks the dependencies and the Go standard library for every push to `main`, every pull request, and once a week.
The Coverage workflow, once a week and when run by hand, measures which statements the unit tests and the end-to-end tests run on each OS, and writes the coverage of each package into the summary of its run.

## End-to-end tests

The end-to-end tests in `test/e2e` run the kickd executable the way its users do.
Each OS has a package of its own, `test/e2e/macos`, `test/e2e/linux` and `test/e2e/windows`, with its own harness and its own test data.
A test copies a directory of `testdata` into a new home directory, where `.kickd` holds the config file and the scripts of its events, starts `kickd run`, fires events through their triggers or with `kickd event`, and checks the runs that kickd records.
The runners of GitHub Actions run the Windows tests as an administrator, and an administrator's kickd reads the config file in the folder `kickd` of `ProgramData`, so the folder of each Windows test is also the `ProgramData` folder of its kickd.
Each test checks one behavior, which its name says, and [test/e2e/README.md](https://github.com/etak64n/kickd/blob/main/test/e2e/README.md) lists them all.

They build only with the tag `e2e`, and each package only on its own OS:

```sh
go test -tags e2e ./test/e2e/macos     # on macOS; ./test/e2e/linux on Linux, ./test/e2e/windows on Windows
```

A test whose program, such as Ruby or `rustc`, is not installed is skipped, and with `KICKD_E2E_ALL=1` it fails instead.

Some tests change the machine.
The service tests install kickd as a service, the way the installation guides do, and write the config, the log and the database to the places of the OS, such as `/etc/kickd`.
The clock tests move the wall clock 2 minutes, to check that wake triggers do not take it for a sleep.
These tests need sudo on macOS and Linux and an administrator on Windows, so they run only with `KICKD_MACHINE_TEST=1`, on a machine that can be thrown away, such as a runner of GitHub Actions:

```sh
KICKD_MACHINE_TEST=1 go test -tags e2e -run 'TestService|TestClock' ./test/e2e/linux
```

A runner cannot sleep, so the unit tests of `internal/trigger` check wake triggers with a clock that the test moves, and the end-to-end tests check that wake triggers do not fire while the machine is awake.

## Releases

Pushing a tag whose name starts with `v` publishes a release.
The release workflow runs every check of the CI workflow for the tagged commit, on all three OSes, and builds the six executables only when all of them passed.
[Building kickd](/kickd/project/build/#release-builds) describes the rest.

```sh
git tag v0.2.0
git push origin v0.2.0
```

After a change to the dependencies in `go.mod`, run `./scripts/third-party-licenses.sh` and commit `internal/licenses/licenses.txt`, the text that `kickd licenses` prints. CI fails while that file is out of date.

## The documentation site

The documentation site is built with Starlight, the documentation theme of Astro, from the Markdown files in `docs/src/content/docs`, and `docs/astro.config.mjs` lists them in the sidebar.
These commands build it into `docs/dist`, with Node.js and the version of pnpm that `packageManager` in `docs/package.json` names:

```sh
pnpm --dir docs install
pnpm --dir docs exec astro build
```

A broken link or a link to a missing heading fails the build.
GitHub Actions builds the site for every pull request that changes the docs.
For every release, it publishes the site to GitHub Pages, at https://etak64n.github.io/kickd/, so that the site describes the released version.
Running the Docs workflow by hand on GitHub publishes the site from the branch or tag that it runs on, such as `main` after a change of the docs alone.

## Source layout

```
cmd/kickd/          The kickd command: the agent (run), run commands (event, events, history, show, cancel, status), setup (check, init, service)
internal/config/    Loading, defaults and validation of the config, and the examples that kickd init writes
internal/queue/     The SQLite database: run records, recovery of interrupted runs, the heartbeat of the agent
internal/runner/    Running commands, and consuming the queue: concurrency, reruns of interrupted runs
internal/trigger/   Cron schedules (gocron), webhooks (net/http), file watching (fsnotify)
internal/agent/     Builds the triggers and the queue consumer from the config, and reloads the config
internal/logging/   JSON and text log output, and log file rotation
internal/event/     The types of a firing and its payload
internal/licenses/  The license texts that kickd licenses prints
test/e2e/           The end-to-end tests of each OS: the kickd executable run the way its users do
docs/               The documentation site, which Starlight builds from the pages in src/content/docs
scripts/            build-all.sh, the cross build for every platform; third-party-licenses.sh, the list of licenses
.github/workflows/  ci.yml, the tests on every OS; vulncheck.yml, the vulnerability check; release.yml, the release workflow; docs.yml, the documentation site
```
