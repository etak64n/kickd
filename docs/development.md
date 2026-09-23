# Development

[Documentation index](../README.md#documentation)

kickd needs Go 1.25 or later to build and test.

```sh
go test ./...              # run the tests
go test -race ./...        # run the tests with the race detector
go vet ./...               # static checks
./scripts/build-all.sh     # build kickd for six platforms into dist/
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
```
