# Webhooks

[Documentation index](../README.md#documentation)

In kickd, a named command in the config file is an **event**.
A **webhook trigger** fires an event when an HTTP request arrives at the trigger's path.
When at least one event has a webhook trigger, kickd starts one HTTP server for all of them.
The server listens on `webhook.listen`, which is `127.0.0.1:8787` by default.

## Calling a webhook

```sh
# A webhook with a token
curl -X POST -H "Authorization: Bearer <token>" http://127.0.0.1:8787/hooks/deploy

# A webhook with a secret: sign the body with HMAC-SHA256
BODY='{"ref":"main"}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')
curl -X POST -H "X-Hub-Signature-256: sha256=$SIG" -d "$BODY" http://127.0.0.1:8787/hooks/deploy

# Health check: responds 200 with "ok"
curl http://127.0.0.1:8787/healthz
```

## Authentication

A webhook trigger can require a token, a signature, or both:

- **token**: the caller sends the token as `Authorization: Bearer <token>`, in the `X-Kickd-Token` header, or as the query parameter `token`.
- **secret**: the caller computes the HMAC-SHA256 of the request body with the secret as the key, and sends `sha256=<hex>` in `X-Hub-Signature-256` or in `X-Kickd-Signature`. `X-Hub-Signature-256` with `sha256=<hex>` is the format that GitHub webhooks use.

When a trigger has both, a request must pass both checks.
A request that fails a check gets 401.

## Request IDs

Every firing has a **request ID**.
The agent's log records of the firing carry it as `requestId`, and the command receives it as `KICKD_REQUEST_ID`.

When the caller sends an `X-Request-ID` header, kickd uses its value as the request ID.
kickd accepts values of up to 128 characters made of letters, digits, `-`, `_`, `.` and `:`.
Without the header, or when the value has other characters, kickd generates a new ID.
Either way, the response carries the ID in its `X-Request-ID` header and in the `requestId` field of its body.

## Parameters

An event can declare **parameters**, named values that a firing passes to the command, under `params`.
Query values whose names match declared parameters become parameters.
Query values with other names, and empty values, do not become parameters.
A request without a required parameter gets 400.

```sh
curl -X POST -H "Authorization: Bearer <token>" "http://127.0.0.1:8787/hooks/deploy?ref=v1.2"
```

## Responses

Responses are JSON.

| Situation | Status | Body |
|---|---|---|
| Accepted and queued | 202 | `{"requestId":"...","event":"deploy","status":"queued"}` |
| The event already had the maximum number of waiting runs | 409 | `{"requestId":"...","event":"deploy","status":"dropped"}` |
| With `wait: true`, skipped because the event was running and its `concurrency` is `skip` | 409 | `{"requestId":"...","event":"deploy","status":"skipped"}` |
| With `wait: true`, the command exited with code 0 | 200 | `{"requestId":"...","event":"deploy","exitCode":0,"durationMs":...,"output":"...","outputTruncated":false,"error":""}` |
| With `wait: true`, the command failed or timed out | 500 | The same fields as for exit code 0, with the reason, such as `timeout`, in `error` |
| Authentication failed | 401 | `{"requestId":"...","error":"unauthorized"}` |
| The method is not in `methods` | 405 | `{"requestId":"...","error":"method not allowed"}` |
| A required parameter is missing | 400 | `{"requestId":"...","error":"missing parameter ref"}` |
| The body could not be read | 400 | `{"requestId":"...","error":"cannot read body"}` |
| The body is larger than `webhook.max_body_bytes` | 413 | `{"requestId":"...","error":"body too large"}` |

With `wait: true`, `output` holds the first 64 KB of the command's output.
Output beyond 64 KB is cut off, and `outputTruncated` is `true`.

If the caller disconnects before a `wait: true` response, the run still continues to the end.
The `Request completed` log record of that request then shows `status` 499.

## Exposing webhooks to other machines

The HTTP server of kickd speaks plain HTTP without TLS.
To accept calls from other machines, keep `listen` on a loopback address, and put a reverse proxy or a tunnel that handles TLS, such as Cloudflare Tunnel or Tailscale, in front of kickd.
When kickd listens on an address other than loopback and a webhook trigger has neither `token` nor `secret`, `kickd check` prints a warning, and the agent logs one when it starts.
