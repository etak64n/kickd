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
scripts/            build-all.sh, the cross build for every platform; third-party-licenses.sh, the list of licenses
.github/workflows/  ci.yml, the tests on every OS; vulncheck.yml, the vulnerability check; release.yml, the release workflow
```
