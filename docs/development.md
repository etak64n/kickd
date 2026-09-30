# Development

[Documentation index](../README.md#documentation)

kickd needs Go 1.25 or later to build and test.

```sh
go test ./...              # run the tests
go test -race ./...        # run the tests with the race detector
go vet ./...               # static checks
./scripts/build-all.sh     # build kickd for six platforms into dist/
```

GitHub Actions runs `go vet` and the tests on Linux, macOS and Windows for every push to `main` and every pull request.
On Linux, it also runs the tests with the race detector, builds kickd for every platform, and runs the tests with the oldest Go version that `go.mod` allows.
On all three OSes, it also runs the end-to-end tests of that OS.

## End-to-end tests

The end-to-end tests in `test/e2e` run the kickd executable the way its users do.
Each OS has a package of its own, `test/e2e/macos`, `test/e2e/linux` and `test/e2e/windows`, with its own harness and its own test data.
A test copies a directory of `testdata`, which holds a config and the scripts of its events, into a new home directory, starts `kickd run`, fires events through their triggers or with `kickd event`, and checks the runs that kickd records.
Each test checks one behavior, which its name says, and [test/e2e/README.md](../test/e2e/README.md) lists them all.

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

Pushing a tag whose name starts with `v` publishes a release: GitHub Actions runs the tests, builds the six executables, and uploads them.

govulncheck, the vulnerability checker of the Go team, checks the dependencies and the Go standard library for every push to `main`, every pull request, and once a week.

After a change to the dependencies in `go.mod`, run `./scripts/third-party-licenses.sh` and commit `internal/licenses/licenses.txt`, the text that `kickd licenses` prints. CI fails while that file is out of date.

```sh
git tag v0.2.0
git push origin v0.2.0
```

## Source layout

```
cmd/kickd/          The kickd command: the agent (run), run commands (event, events, queue, runs, show, cancel, status), setup (check, init, service)
internal/config/    Loading, defaults and validation of the config, and the example config example.yaml
internal/queue/     The SQLite database: run records, recovery of interrupted runs, the heartbeat of the agent
internal/runner/    Running commands, and consuming the queue: concurrency, reruns of interrupted runs
internal/trigger/   Cron schedules (gocron), webhooks (net/http), file watching (fsnotify)
internal/agent/     Builds the triggers and the queue consumer from the config, and reloads the config
internal/logging/   JSON and text log output, and log file rotation
internal/event/     The types of a firing and its payload
internal/licenses/  The license texts that kickd licenses prints
test/e2e/           The end-to-end tests of each OS: the kickd executable run the way its users do
scripts/            build-all.sh, the cross build for every platform; third-party-licenses.sh, the list of licenses
.github/workflows/  ci.yml, the tests on every OS; vulncheck.yml, the vulnerability check; release.yml, the release workflow
```
