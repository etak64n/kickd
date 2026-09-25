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
On all three OSes, it also runs the use-case tests, with every kind of command required, and the service tests.

## Use-case tests

The use-case tests in `test/usecase` run the kickd executable the way its users do.
Each test writes the config of the README with `kickd init`, replaces the commands with small scripts, starts the agent, and fires the events through their triggers.
The tests check:

- the triggers of the README config, and that only an event with a manual trigger can be fired by hand
- file changes of every kind, directories created later, and `include`, `exclude`, `changes` and `recursive`
- cron schedules in time zones with offsets of whole hours, half an hour and three quarters of an hour, and `missed: run` and `missed: skip` across a stop of kickd
- the signature of a webhook with `secret`, parameters from the query, `wait: true`, and `methods`
- the `concurrency` and `on_interrupt` settings, timeouts, `kickd cancel` and reloading
- commands of many kinds: commands of the OS in a string, and programs in sh, Python, Node.js, Ruby, Perl, Rust, PowerShell and cmd
- Japanese and spaces in paths, file names and parameters
- processes that a command leaves running in the background, and the processes that a timeout or `kickd cancel` stops
- the ways of running programs in [Running commands](commands.md): a virtual environment of Python, `npm run`, a program in the `PATH` of the event, and `stdin: payload`
- the output of `kickd check`, `events`, `status`, `queue`, `runs` and `show`

They build only with the tag `usecase`:

```sh
go test -tags usecase ./test/usecase
```

A kind of command whose program is not installed is skipped, and with `KICKD_USECASE_ALL=1` it fails the test instead.

The service tests install kickd as a service of the whole system and as a per-user service, the way the installation guides do.
They write the config, the log and the database to the places of the OS, such as `/etc/kickd`, and need sudo on macOS and Linux and an administrator on Windows.
So they run only with `KICKD_SERVICE_TEST=1`, on a machine that can be thrown away, such as a runner of GitHub Actions:

```sh
KICKD_SERVICE_TEST=1 go test -tags usecase -run TestService ./test/usecase
```

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
test/usecase/       The use-case tests: the kickd executable with the README config, commands of many kinds, and services
scripts/            build-all.sh, the cross build for every platform; third-party-licenses.sh, the list of licenses
.github/workflows/  ci.yml, the tests on every OS; vulncheck.yml, the vulnerability check; release.yml, the release workflow
```
