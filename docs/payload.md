# What a command receives

[Documentation index](../README.md#documentation)

In kickd, a named command in the config file is an **event**, and each firing of an event is recorded as a **run**.
The information about one run that kickd passes to the command is called the **payload**.
kickd passes the payload in environment variables and in a JSON file.

Every command receives these environment variables:

| Variable | Contents |
|---|---|
| `KICKD_EVENT` | The event name |
| `KICKD_REQUEST_ID` | The request ID of the firing. kickd's log records of the firing carry the same value, so a command that writes it into its own logs links the two. |
| `KICKD_RUN_ID` | The run ID, which `kickd show` accepts |
| `KICKD_ATTEMPT` | The attempt number: 1 for the first run, one higher for each rerun after an interruption |
| `KICKD_TRIGGER` | The kind of trigger: `manual`, `cron`, `webhook` or `file`. `manual` means `kickd event`. |
| `KICKD_TRIGGER_ID` | The trigger, in the form `manual`, `cron:0 3 * * *`, `webhook:/hooks/x` or `file:/path` |
| `KICKD_TIME` | When the event fired, in UTC, in RFC 3339 format |
| `KICKD_EVENT_DATA` | All parameters, the named values passed with the firing, as one JSON object |
| `KICKD_DATA_<NAME>` | One variable for each parameter, with the name in upper case: `ref` arrives as `KICKD_DATA_REF` |
| `KICKD_PAYLOAD_FILE` | The path of a JSON file that holds the whole payload. kickd deletes the file when the command ends. |

Depending on the trigger, these variables are also set:

| Variable | Contents |
|---|---|
| `KICKD_SOURCE` | For `kickd event`, the user and host that fired the event |
| `KICKD_FILE_PATH` | The file that changed last |
| `KICKD_FILE_OP` | The kind of the last change |
| `KICKD_FILE_COUNT` | The number of changes combined into this firing |
| `KICKD_FILE_PATHS` | The paths of those changes, joined with `:` on macOS and Linux and with `;` on Windows |
| `KICKD_CRON_SCHEDULE` | The cron expression that fired |
| `KICKD_CRON_SCHEDULED_AT` | The scheduled time that the run stands for, in UTC, in RFC 3339 format. When several scheduled times passed at once, the latest of them |
| `KICKD_CRON_MISSED` | `1` when that time passed while the machine slept or kickd was stopped, so the run makes up for it, and `0` otherwise |
| `KICKD_WEBHOOK_METHOD` | The HTTP method of the request |
| `KICKD_WEBHOOK_PATH` | The URL path of the request |
| `KICKD_WEBHOOK_REMOTE_ADDR` | The address the request came from |

With `stdin: payload` in the event, the command also receives the payload JSON on standard input.

The payload of a file firing holds `files`, a list of the combined changes, each with `path` and `op`.
The payload of a cron firing holds `cron.schedule`, the cron expression that fired, and `cron.scheduledAt` and `cron.missed`, the same values as `KICKD_CRON_SCHEDULED_AT` and `KICKD_CRON_MISSED`.

The payload of a webhook firing holds the request body, headers and query.
A header or query parameter with several values keeps only its first value.
The body is stored as a JSON string, so bytes that are not valid UTF-8 are replaced with U+FFFD.
kickd removes the headers `Authorization` and `X-Kickd-Token` and the query parameter `token`, which carry credentials.
A webhook payload looks like this:

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

The payload of `kickd event` holds, in `source`, the user and host that fired the event:

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
  "source": "alice@laptop"
}
```

A shell script can read the payload with `jq`, for example `jq -r .webhook.body "$KICKD_PAYLOAD_FILE"`.
