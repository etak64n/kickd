# Trigger settings

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, and a **trigger** fires an event.
Each firing is recorded as a **run** in the **database**, a SQLite file, and the long-running kickd process, called the **agent**, starts the command of each run.

The `triggers` key of an event lists its triggers, and the event fires only through them.
Each trigger is a mapping with a `type`, one of seven:

| Type | Fires the event |
|---|---|
| `manual` | When `kickd event NAME` names the event |
| `cron` | At the times of a cron expression |
| `webhook` | When an HTTP request arrives at a path |
| `file` | When files change in a directory |
| `after` | When a run of another event ends with one of the given statuses |
| `startup` | When the agent starts |
| `wake` | When the machine wakes from sleep |

A trigger takes only the keys of its type, so `schedule` on a file trigger is an error of the config file.
An event can list several triggers, of the same type or of different types, and at most one of each of `manual`, `startup` and `wake`:

```yaml
events:
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    triggers:
      - type: cron
        schedule: '0 3 * * *'
        timezone: Asia/Tokyo
      - type: manual
```

The agent reloads its config file when the file is saved: it waits 0.5 seconds after the save, checks the new file, and switches to it when the file has no errors.
At the switch, the agent stops its triggers and starts them again from the new file, so a change to a cron, webhook, file, after or wake trigger takes effect when the config file is saved.

Every run gets environment variables that describe its firing, and each type of trigger except `startup` fills its own: `KICKD_MANUAL_SOURCE` for manual triggers, and `KICKD_CRON_*`, `KICKD_WEBHOOK_*`, `KICKD_FILE_*`, `KICKD_AFTER_*` and `KICKD_WAKE_*` for the others.
[What a command receives](../payload.md) lists all of them.

## Manual triggers

A manual trigger lets `kickd event NAME` fire the event.
An event without one cannot be fired by hand: `kickd event` refuses it with exit code 2 and names the line to add.

`kickd event` writes the run to the database from a process of its own, so it works while the agent is stopped, and the run waits until the agent starts.
The command receives the user and the machine that ran `kickd event`, as `user@host`, in `KICKD_MANUAL_SOURCE`.

### `type: manual`

A manual trigger has no other keys.

- **Type**: `manual`.
- **Default**: none. `type` is required.
- **Takes effect**: at once for `kickd event`, which reads the config file each time it runs.

```yaml
events:
  - name: notify
    command: 'echo "kickd notification" | mail -s kickd me@example.com'
    triggers:
      - type: manual
```

## Cron triggers

A cron trigger fires the event at the times of a cron expression.
The agent looks at the clock at least once a second, so a firing starts within about a second of its time.

### `schedule`

`schedule` is the cron expression that gives the times.

- **Type**: a string with a cron expression.
- **Default**: none. `schedule` is required.
- **Takes effect**: when the config file is saved.

An expression has five fields, separated by spaces: minute, hour, day of month, month and day of week.
A sixth field in front gives the second, so `30 0 3 * * *` fires at 3:00:30.
Each field takes a value such as `5`, a range such as `1-5`, a step such as `*/15`, a list such as `1,15`, or `*` for every value.
The day of week is a number from 0 to 7, where 0 and 7 are Sunday, or a name from `sun` to `sat`.
When both day fields are restricted, a day that matches either of them fires, as in the cron of Unix systems.

The day fields also take days that move within the month: `L` for the last day of the month, `15W` for the weekday nearest to the 15th, `5L` for the last Friday, and `fri#3` for the third Friday.
Descriptors stand for whole expressions: `@hourly`, `@daily`, `@weekly`, `@monthly` and `@yearly`, and `@every 10m` fires every 10 minutes.
The [gocron README](https://github.com/etak64n/gocron#expressions) describes every form.

`kickd check` reports an expression that it cannot read as an error, and the agent logs `Cron schedule added` with the next time, `nextRunAt`, for each cron trigger.

```yaml
events:
  - name: report
    command: ['python3', 'report.py']
    workdir: '~/reports'
    triggers:
      - type: cron
        schedule: '0 9 * * mon-fri'
```

### `timezone`

`timezone` is the time zone in which the expression reads its times.

- **Type**: the name of a time zone of the IANA database, such as `Asia/Tokyo`, `Europe/Berlin` or `UTC`.
- **Default**: none, which reads the times in the local time zone of the machine.
- **Takes effect**: when the config file is saved.

kickd has the time zone database built in, so the names work on Windows too.
An expression that starts with `CRON_TZ=` and a name, as in `CRON_TZ=Asia/Tokyo 0 3 * * *`, takes that zone instead of `timezone`.

On the day daylight saving time starts, the clock skips some times, such as 2:00 to 2:59 in New York, and a time that the clock skips fires just after the gap: `30 2 * * *` fires at 3:30.
On the day daylight saving time ends, the clock shows some times twice, and such a time fires once, the first time that the clock shows it.

```yaml
events:
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    triggers:
      - type: cron
        schedule: '0 3 * * *'
        timezone: Asia/Tokyo
```

### `missed`

`missed` decides what happens to times that passed while the machine slept or the agent was not running.

- **Type**: one of `run` and `skip`.
- **Default**: `run`.
- **Takes effect**: when the config file is saved.

The agent looks at the clock at least once a second, so it notices a time late only after the machine slept or while it was not running itself.
A time that the agent notices more than a minute late counts as **missed**.

- **run**: the event fires once, right after the machine wakes or the agent starts, however many times passed. The command receives `KICKD_CRON_MISSED=1`, and in `KICKD_CRON_SCHEDULED_AT`, the latest of the missed times.
- **skip**: the missed times do not fire, and the event waits for its next time.

The agent logs `Missed schedule caught up` or `Missed schedule skipped`, with the number of missed times in `count`.

The agent keeps, in the database, when it last handled each cron trigger, and measures the times that passed from there.
The record belongs to the event name, the position of the trigger in the list, `timezone` and `schedule`, so a change to any of them starts a new record.
A trigger with a new record fires only at the times that come after the agent starts it.

```yaml
events:
  - name: digest
    command: ['python3', 'digest.py']
    workdir: '~/digest'
    triggers:
      - type: cron
        schedule: '0 8 * * *'
        missed: skip
```

## Webhook triggers

A webhook trigger fires the event when an HTTP request arrives at its path.
The agent runs one HTTP server for all webhook triggers of the config file, and the `webhook` section of the config file sets its address and limits; [Webhook server settings](webhook.md) describes that section.

The command receives the request in the payload JSON: its method, path, headers, query and body.
The payload leaves out the `Authorization` and `X-Kickd-Token` headers and the `token` query value, so the token does not reach the command.
Query values whose names the event declares in `params` become parameters of the firing.
[Webhooks](../webhook.md) describes how to call a webhook and every response.

### `path`

`path` is the URL path at which the trigger receives requests.

- **Type**: a string that starts with `/`, such as `/hooks/deploy`.
- **Default**: none. `path` is required.
- **Takes effect**: when the config file is saved.

No two webhook triggers of one config file can have the same path, and `/healthz` is reserved for the health check of the server.
A request to a path that no trigger has gets 404.
A path that ends with `/` also receives the requests to every path below it, so `/hooks/` receives `/hooks/deploy` too.

```yaml
events:
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        token: 'replace-with-a-long-random-string'
```

### `methods`

`methods` lists the HTTP methods that the trigger accepts.

- **Type**: a list of method names, such as `[POST]`, in upper or lower case.
- **Default**: none, which accepts every method.
- **Takes effect**: when the config file is saved.

A request with another method gets 405, and its event does not fire.

```yaml
events:
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        methods: [POST]
        token: 'replace-with-a-long-random-string'
```

### `token`

`token` is a shared secret that each request must carry.

- **Type**: a string.
- **Default**: none, which asks requests for no token.
- **Takes effect**: when the config file is saved.

A request carries the token in one of three places, which kickd checks in this order:

- **`Authorization: Bearer <token>`**, the form of most HTTP clients and services.
- **The header `X-Kickd-Token`**.
- **The query value `token`**, as in `/hooks/deploy?token=<token>`, for callers that cannot set headers. The URL, and the token in it, can end up in the logs of proxies.

A request without the token, or with another one, gets 401, and its event does not fire.
A long random value makes a good token, such as the output of `openssl rand -hex 32`.
kickd does not write the token to its log.

```yaml
events:
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        token: 'replace-with-a-long-random-string'
```

### `secret`

`secret` is the key with which each request must sign its body.

- **Type**: a string.
- **Default**: none, which asks requests for no signature.
- **Takes effect**: when the config file is saved.

The caller computes the HMAC-SHA256 of the request body, with the secret as the key, and sends it as `sha256=` and the hex digits in the header `X-Hub-Signature-256` or `X-Kickd-Signature`.
`X-Hub-Signature-256` is the header that GitHub webhooks send, so the secret of a GitHub webhook works here as it is.
A request without a signature, or with one that does not match its body, gets 401, and its event does not fire.
A trigger with both `token` and `secret` requires both from each request.

```yaml
events:
  - name: deploy
    command: ['./deploy.sh']
    workdir: '~/app'
    triggers:
      - type: webhook
        path: '/hooks/github'
        methods: [POST]
        secret: 'the-secret-of-the-github-webhook'
```

### `wait`

`wait` decides whether the response waits for the run.

- **Type**: `true` or `false`, written without quotes.
- **Default**: `false`.
- **Takes effect**: when the config file is saved.

- **false**: the server answers 202 as soon as the run is in the queue, or 409 when the event already has 1000 waiting runs and drops the firing.
- **true**: the server answers when the run ends: 200 when the command exited with code 0, and 500 otherwise. The body holds the exit code, the duration, and the first 64 KB of the output of the command. A firing that the event skips or drops gets 409.

A caller that stops waiting leaves the run running to its end, so the HTTP client of the caller needs a timeout longer than the longest run.

```yaml
events:
  - name: test
    command: ['make', 'test']
    workdir: '~/app'
    triggers:
      - type: webhook
        path: '/hooks/test'
        token: 'replace-with-a-long-random-string'
        wait: true
```

## File triggers

A file trigger fires the event when files change in a directory.
It collects the changes that arrive close together and fires once for them.

The command receives the changes in the payload JSON, as a list of paths with the kind of each change.
The variables `KICKD_FILE_PATH` and `KICKD_FILE_OP` give the last change, `KICKD_FILE_COUNT` the number of changes, and `KICKD_FILE_PATHS` every path, separated by `:` on macOS and Linux and by `;` on Windows.

### `path`

`path` is the directory that the trigger watches.

- **Type**: a path. A leading `~` is the home directory, `${VAR}` is the value of the environment variable `VAR`, and a relative path starts at the directory of the config file.
- **Default**: none. `path` is required.
- **Takes effect**: when the config file is saved.

The path must be a directory that exists when the config file is loaded, and anything else is an error of the config file.
To watch one file, watch its directory and name the file in `include`.
The agent logs `File watch started` when the trigger watches its directory.

On macOS, a kickd that runs as a service needs Full Disk Access to watch protected folders, such as Documents and Downloads, and cloud folders such as iCloud Drive do not always report their changes.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    triggers:
      - type: file
        path: '~/app/src'
```

### `recursive`

`recursive` decides whether the trigger also watches the directories below `path`.

- **Type**: `true` or `false`, written without quotes.
- **Default**: `false`, which watches only the files directly in `path`.
- **Takes effect**: when the config file is saved.

With `true`, the trigger watches every directory below `path`, including directories created later.
The files in a directory that is created or moved in count as created, because no watch saw them being written.
Directories that `exclude` leaves out are not watched at all.

On Linux, the kernel limits the number of watched directories of each user with `fs.inotify.max_user_watches`, and the trigger watches each directory separately.
Directories beyond the limit are not watched, and the agent logs a warning with their number.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    triggers:
      - type: file
        path: '~/app/src'
        recursive: true
```

### `include`

`include` lists patterns of the files that fire the event.

- **Type**: a list of patterns, such as `['*.md', 'docs/*.txt']`.
- **Default**: none, which lets every file fire the event.
- **Takes effect**: when the config file is saved.

A pattern is matched in one of two ways:

- **A pattern without `/`**, such as `*.md`: against the name of the file, in any directory.
- **A pattern with `/`**, such as `docs/*.md`: against the path of the file relative to `path`, written with `/` on every OS.

In a pattern, `*` matches any characters except `/`, `?` matches one character, and `[...]` matches one of the characters in the brackets.
`**` has no special meaning, so a pattern for every depth is a pattern without `/`.
A change of a file that no pattern matches does not fire the event.

```yaml
events:
  - name: publish
    command: ['make', 'html']
    workdir: '~/site'
    triggers:
      - type: file
        path: '~/site/docs'
        recursive: true
        include: ['*.md']
```

### `exclude`

`exclude` lists patterns of the paths that never fire the event.

- **Type**: a list of patterns, such as `['.git', '*.swp']`.
- **Default**: none.
- **Takes effect**: when the config file is saved.

A pattern is matched in one of two ways:

- **A pattern without `/`**, such as `.git`: against every part of the path relative to `path`, so a directory name leaves out everything below that directory.
- **A pattern with `/`**, such as `build/*.o`: against the whole path relative to `path`, written with `/` on every OS.

The patterns have the same syntax as those of `include`, and a path that matches both `include` and `exclude` does not fire the event.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    triggers:
      - type: file
        path: '~/app'
        recursive: true
        exclude: ['.git', 'build', '*.swp', '*~']
```

### `changes`

`changes` lists the kinds of change that fire the event.

- **Type**: a list of `create`, `write`, `remove`, `rename` and `chmod`.
- **Default**: `[create, write, remove, rename]`, every kind except `chmod`.
- **Takes effect**: when the config file is saved.

The kinds are:

- **create**: a file or a directory appeared, including one moved in.
- **write**: the content of a file changed.
- **remove**: a file or a directory was deleted.
- **rename**: a file or a directory was renamed or moved away. The change carries the old path, and the new path, when it is inside the watched directory, arrives as `create`.
- **chmod**: the permissions or other attributes of a file changed.

Many editors save a file by writing a temporary file and renaming it over the old one, so such a save arrives as a `create` of the saved file rather than as a `write`.

```yaml
events:
  - name: import
    command: ['./import.sh']
    workdir: '~/uploads'
    triggers:
      - type: file
        path: '~/uploads'
        changes: [create]
```

### `debounce`

`debounce` is how long the trigger waits for more changes before it fires.

- **Type**: a duration: a number with one of the units `ms`, `s`, `m` and `h`, such as `500ms` or `2s`.
- **Default**: `1s`. The value `0s` also means 1 second.
- **Takes effect**: when the config file is saved.

After a change, the trigger waits until no new change has arrived for this long, and then fires once for all the changes of the wait.
Each path appears once for each kind of change, and one firing holds up to 10,000 changes; the agent drops the changes beyond them and logs a warning.
Saving several files, or switching a branch in Git, causes a burst of changes, and a longer `debounce` turns the burst into one firing.
A negative duration is an error of the config file.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    triggers:
      - type: file
        path: '~/app/src'
        recursive: true
        debounce: 2s
```

## After triggers

An after trigger fires the event when a run of another event ends with one of the given statuses.
It joins events into steps, such as a deploy after a build that succeeded, or a notification after a backup that failed.

Only the last attempt of a firing counts: a run that a stop or a crash cut off, and that runs again under `on_interrupt: rerun`, fires nothing until its rerun ends.
A run that ends while the agent is stopped, such as a waiting run that `kickd cancel` canceled, fires the event when the agent starts.
The command receives the run that ended in `KICKD_AFTER_EVENT`, `KICKD_AFTER_RUN_ID`, `KICKD_AFTER_STATUS` and `KICKD_AFTER_EXIT_CODE`.
`KICKD_AFTER_EXIT_CODE` is empty for a run whose command did not exit, such as a skipped run.

### `event`

`event` is the event whose runs the trigger follows.

- **Type**: the name of another event of the config file.
- **Default**: none. `event` is required.
- **Takes effect**: when the config file is saved.

An event cannot follow itself, and follows another event with at most one after trigger.
After triggers cannot form a cycle, such as two events that follow each other, because each run would fire the next one forever, and `kickd check` names the events of such a cycle.

```yaml
events:
  - name: build
    command: ['make', 'build']
    workdir: '~/app'
    triggers:
      - type: manual
  - name: deploy
    command: ['make', 'deploy']
    workdir: '~/app'
    triggers:
      - type: after
        event: build
        status: [succeeded]
```

### `status`

`status` lists the statuses of the runs that fire the event.

- **Type**: a list of `succeeded`, `failed`, `canceled`, `skipped`, `dropped` and `abandoned`.
- **Default**: none. `status` is required, with at least one status.
- **Takes effect**: when the config file is saved.

The statuses are those of a run that has ended:

- **succeeded**: the command exited with code 0.
- **failed**: the command exited with another code, ran past its `timeout`, or could not start.
- **canceled**: `kickd cancel` canceled the run.
- **skipped**: the run did not start, because its event was running and its `concurrency` is `skip`.
- **dropped**: the run was discarded, because its event already had 1000 waiting runs or the config no longer defines it.
- **abandoned**: a stop or a crash cut the run off, and it does not run again.

```yaml
events:
  - name: backup
    command: 'rsync -a ~/work/ ~/backup/work/'
    triggers:
      - type: cron
        schedule: '0 3 * * *'
  - name: notify-failure
    command: 'echo "backup run $KICKD_AFTER_RUN_ID ended as $KICKD_AFTER_STATUS" | mail -s kickd me@example.com'
    triggers:
      - type: after
        event: backup
        status: [failed, abandoned]
```

## Startup triggers

A startup trigger fires the event once when the agent starts.
That includes a start by a service manager at boot, and a start again after a crash.
Saving the config file, and SIGHUP on macOS and Linux, reload the config without a start, so they do not fire the event.
The agent logs `Startup trigger fired` for each event that it fires.

### `type: startup`

A startup trigger has no other keys.

- **Type**: `startup`.
- **Default**: none. `type` is required.
- **Takes effect**: at the next start of the agent.

```yaml
events:
  - name: clean-tmp
    command: 'find "$HOME/tmp" -type f -mtime +7 -delete'
    triggers:
      - type: startup
```

## Wake triggers

A wake trigger fires the event when the machine wakes from a sleep of at least one second.
The agent reads, once a second, how long the machine has slept since it booted, from two clocks of the OS: one that stops while the machine sleeps, and one that does not.
A change of the wall clock moves neither of them, so setting the clock does not fire the event.
The command receives, in `KICKD_WAKE_SLEPT_AT`, when the agent last saw the machine awake, and in `KICKD_WAKE_SLEPT_SECONDS`, how long the machine slept.

### `type: wake`

A wake trigger has no other keys.

- **Type**: `wake`.
- **Default**: none. `type` is required.
- **Takes effect**: when the config file is saved.

```yaml
events:
  - name: sync-notes
    command: ['git', 'pull', '--ff-only']
    workdir: '~/notes'
    triggers:
      - type: wake
      - type: cron
        schedule: '0 * * * *'
```
