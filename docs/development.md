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

Pushing a tag whose name starts with `v` publishes a release: GitHub Actions runs the tests, builds the six executables, and uploads them with `LICENSE`, `THIRD_PARTY_LICENSES.txt` and `checksums.txt`.

```sh
git tag v0.2.0
git push origin v0.2.0
```

## Source layout

```
cmd/kickd/          The kickd command: the agent (run), queue commands (event, events, queue, runs, show, cancel, status), setup (check, init, service)
internal/config/    Loading, defaults and validation of the config, and the example config example.yaml
internal/queue/     The SQLite queue: run records, recovery of interrupted runs, the heartbeat of the agent
internal/runner/    Running commands, and consuming the queue: concurrency, reruns of interrupted runs
internal/trigger/   Cron (robfig/cron), webhooks (net/http), file watching (fsnotify)
internal/agent/     Builds the triggers and the queue consumer from the config, and reloads the config
internal/logging/   JSON and text log output, and log file rotation
internal/event/     The types of a firing and its payload
scripts/            build-all.sh, the cross build for every platform; third-party-licenses.sh, the license texts for releases
.github/workflows/  ci.yml, the tests on every OS; release.yml, the release workflow
```
