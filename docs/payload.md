# What a command receives

[Documentation index](../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event is recorded as a **run**.
The information about one run that kickd passes to the command is called the **payload**.
kickd passes the payload in environment variables and in a JSON file.

## Environment variables

Every command receives the same environment variables, whatever its event and its trigger.
A variable about another kind of trigger is set to an empty string, so a script can read any of them without checking that it exists, even under `set -u`.
The name of a variable about one kind of trigger starts with that kind: `KICKD_MANUAL_`, `KICKD_CRON_`, `KICKD_WEBHOOK_` or `KICKD_FILE_`.

| Variable | Filled for | Contents |
|---|---|---|
| `KICKD_EVENT` | Every run | The event name |
| `KICKD_RUN_ID` | Every run | The run ID, which `kickd show` accepts |
| `KICKD_REQUEST_ID` | Every run | The request ID of the firing. kickd's log records of the firing carry the same value, so a command that writes it into its own logs links the two. |
| `KICKD_ATTEMPT` | Every run | The attempt number: 1 for the first run, one higher for each rerun after an interruption |
| `KICKD_TIME` | Every run | When the event fired, in UTC, in RFC 3339 format |
| `KICKD_TRIGGER` | Every run | The kind of trigger: `manual`, `cron`, `webhook` or `file`. `manual` means `kickd event`. |
| `KICKD_TRIGGER_ID` | Every run | The trigger, in the form `manual`, `cron:0 3 * * *`, `webhook:/hooks/x` or `file:/path` |
| `KICKD_PAYLOAD_FILE` | Every run | The path of a JSON file that holds the whole payload. kickd deletes the file when the command ends. |
| `KICKD_DATA` | Every run | All parameters, the named values passed with the firing, as one JSON object: `{}` for an event without parameters |
| `KICKD_DATA_<NAME>` | Every run | One variable for each parameter, with the name in upper case: `ref` arrives as `KICKD_DATA_REF` |
| `KICKD_MANUAL_SOURCE` | `manual` | The user and host that ran `kickd event` |
| `KICKD_CRON_SCHEDULE` | `cron` | The cron expression that fired |
| `KICKD_CRON_SCHEDULED_AT` | `cron` | The scheduled time that the run stands for, in UTC, in RFC 3339 format. When several scheduled times passed at once, the latest of them |
| `KICKD_CRON_MISSED` | `cron` | `1` when that time passed while the machine slept or kickd was stopped, so the run makes up for it, and `0` otherwise |
| `KICKD_WEBHOOK_METHOD` | `webhook` | The HTTP method of the request |
| `KICKD_WEBHOOK_PATH` | `webhook` | The URL path of the request |
| `KICKD_WEBHOOK_REMOTE_ADDR` | `webhook` | The address the request came from |
| `KICKD_FILE_PATH` | `file` | The file that changed last |
| `KICKD_FILE_OP` | `file` | The kind of the last change |
| `KICKD_FILE_COUNT` | `file` | The number of changes combined into this firing |
| `KICKD_FILE_PATHS` | `file` | The paths of those changes, joined with `:` on macOS and Linux and with `;` on Windows |

An event with `params` gets a `KICKD_DATA_<NAME>` variable for every declared parameter, whatever the trigger.
A parameter that the firing leaves out takes its `default`, and without a default it is empty.
An event without `params` accepts parameters of any name, so it gets a variable for each parameter that the firing passes.

## The payload JSON

The JSON file at `KICKD_PAYLOAD_FILE` holds the same keys for every run.
With `stdin: payload` in the event, the command also receives the JSON on standard input.

`data` holds the parameters, as `KICKD_DATA` does.
Four keys hold the details of one kind of trigger each, and they are empty for the other kinds: `source` is `""`, `files` is `[]`, and `cron` and `webhook` are `null`.

| Key | Filled for | Contents |
|---|---|---|
| `source` | `manual` | The user and host that ran `kickd event` |
| `files` | `file` | The changes combined into the firing, each with `path` and `op` |
| `cron` | `cron` | `schedule`, `scheduledAt` and `missed`, the same values as the `KICKD_CRON_` variables |
| `webhook` | `webhook` | `method`, `path` and `remoteAddr`, and the `headers`, `query` and `body` of the request |

In `webhook`, a header or query parameter with several values keeps only its first value.
The body is stored as a JSON string, so bytes that are not valid UTF-8 are replaced with U+FFFD.
kickd removes the headers `Authorization` and `X-Kickd-Token` and the query parameter `token`, which carry credentials.

The payload of a webhook firing looks like this:

```json
{
  "requestId": "gh-delivery-7f3a",
  "runId": 42,
  "attempt": 1,
  "event": "deploy",
  "trigger": "webhook",
  "triggerId": "webhook:/hooks/deploy",
  "time": "2026-09-23T08:41:12.345678Z",
  "data": {"ref": "v1.2"},
  "source": "",
  "files": [],
  "cron": null,
  "webhook": {
    "method": "POST",
    "path": "/hooks/deploy",
    "remoteAddr": "127.0.0.1:52344",
    "headers": {"Content-Type": "application/json", "X-Github-Event": "push"},
    "query": {"ref": "v1.2"},
    "body": "{\"ref\":\"refs/heads/main\"}"
  }
}
```

The payload of `kickd event` has the same keys, with the details in `source`:

```json
{
  "requestId": "39c4ba680cdea67c",
  "runId": 43,
  "attempt": 1,
  "event": "deploy",
  "trigger": "manual",
  "triggerId": "manual",
  "time": "2026-09-23T14:20:07.123456Z",
  "data": {"ref": "v1.2"},
  "source": "alice@laptop",
  "files": [],
  "cron": null,
  "webhook": null
}
```

A shell script can read the payload with `jq`, for example `jq -r .webhook.body "$KICKD_PAYLOAD_FILE"`.
