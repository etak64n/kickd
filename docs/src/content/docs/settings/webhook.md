# Webhook server settings

[Documentation index](../index.md)

In kickd, a named command in the config file is an **event**, and a **webhook trigger** fires an event when an HTTP request arrives at the path of the trigger.
The long-running kickd process, called the **agent**, runs one HTTP server for all the webhook triggers of its config file.
The `webhook` section of the config file sets that server:

```yaml
webhook:
  enabled: true
  listen: '127.0.0.1:9787'
  max_body_bytes: 1048576
```

Each webhook trigger sets its own path, methods and authentication among the triggers of its event, and [Trigger settings](triggers.md#webhook-triggers) describes those keys.
The server also answers requests to `/healthz` with 200 and `ok`, for health checks.
It refuses requests that a web browser sends, and it limits the connections that it serves at once and the time that a request may take; [Webhooks](../webhook.md#limits-of-the-server) describes these rules.

The agent reloads its config when the config file, or another YAML file next to it, is saved, added or removed: it waits 0.5 seconds after the change, checks the files, and switches to the new config when they have no errors.
At the switch, the agent stops its HTTP server and starts a new one with the new settings, so a change to the `webhook` section of the config file takes effect when the file is saved.
A request that arrives during the switch can fail to connect.
When the new config moves the server to another address, the agent binds the new address before it stops the old server, and an address in use keeps the previous config.

## `webhook.enabled`

`webhook.enabled` decides whether the agent runs the HTTP server at all.

- **Type**: `true` or `false`, written without quotes.
- **Default**: `true`.
- **Takes effect**: when the config file is saved.

With `false`, the agent starts no HTTP server, so no webhook trigger fires, and the other triggers work as usual.
The config file keeps its webhook triggers, so setting `true` again brings them back.
`kickd check` prints `webhook: disabled by webhook.enabled, so webhook triggers do not fire`, and the agent logs `Webhook triggers disabled` when it loads the config.

```yaml
webhook:
  enabled: false
events:
  - name: deploy
    command: ['./deploy.sh']
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        token: 'replace-with-a-long-random-string'
      - type: manual
```

## `webhook.listen`

`webhook.listen` is the address on which the HTTP server accepts connections.

- **Type**: an address in the form `host:port`.
- **Default**: `127.0.0.1:9787`.
- **Takes effect**: when the config file is saved.

The host decides which machines can connect:

- **`127.0.0.1` or `[::1]`**, the loopback addresses: only programs on the same machine.
- **`0.0.0.0`**, or an empty host as in `:9787`: every machine that can reach this machine.
- **The address of one network interface**, such as `192.168.1.20`: the machines that reach that interface.

The server starts only when at least one event has a webhook trigger.
When the port is in use by another program, the server cannot start:

- **At the start of the agent**: the agent stops with the FATAL record `Agent start failed`, before it starts any command. A service manager starts it again, and it stops again while the port stays in use.
- **At a reload that moves the server to another address**: the agent binds the new address first, and when that address is in use, it logs `Config reload failed` and keeps the previous config, whose server goes on answering.
- **At a reload that keeps the address**: the agent closes the old server and opens the new one at once. When another program takes the port in between, the agent logs `Trigger failed` and runs no triggers until a file of the config is saved again. Runs that wait in the queue, and firings with `kickd event`, still run.

The server speaks plain HTTP without TLS, so a token or a request body that crosses a network is readable on the way.
To accept requests from other machines, keep a loopback address here and put a reverse proxy or a tunnel that handles TLS in front of kickd.
With an address other than loopback, a webhook trigger that has neither `token` nor `secret` accepts requests from anyone who can connect, so `kickd check` prints a warning for it, and the agent logs `Config warning` when it loads the config.
On Windows, Windows Defender Firewall blocks connections from other machines to such an address until a rule allows them.

```yaml
webhook:
  listen: '127.0.0.1:9000'
```

## `webhook.max_body_bytes`

`webhook.max_body_bytes` is the largest request body that the server accepts.

- **Type**: a whole number of bytes, written without quotes.
- **Default**: `1048576`, which is 1 MB, or 1,048,576 bytes. The value `0`, and any negative number, also mean 1048576.
- **Takes effect**: when the config file is saved.

A request whose body is larger gets 413 with `{"error":"body too large"}`, and its event does not fire.
The server checks the token before it reads the body, so a request without the right token gets 401 whatever the size of its body.
It reads the body before it checks the signature, because the signature is computed over the body, so a request with a signature and a body that is too large gets 413.
The command of an accepted request receives the body in the `webhook.body` field of the payload JSON, so the limit also bounds the size of each payload.

```yaml
webhook:
  max_body_bytes: 65536
```
