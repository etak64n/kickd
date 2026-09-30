# Parameter settings

[Documentation index](../../README.md#documentation)

In kickd, a named command in the config file is an **event**, and asking kickd to run an event is **firing** it.
A **parameter** is a named value that a firing passes to the command of the event, such as the Git ref that a deploy checks out.
The `params` key of an event declares its parameters:

```yaml
events:
  - name: deploy
    command: 'git -C ~/app fetch && git -C ~/app checkout "$KICKD_DATA_REF"'
    params:
      - name: ref
        default: main
        description: 'the branch, tag or commit to check out'
    triggers:
      - type: webhook
        path: '/hooks/deploy'
        token: 'replace-with-a-long-random-string'
      - type: manual
```

Two kinds of firing pass parameters:

- **`kickd event`**: as `KEY=VALUE` arguments after the event name, as in `kickd event deploy ref=v1.2`, or as a JSON object of strings with `--data`.
- **A webhook request**: as values in the query of the URL, as in `/hooks/deploy?ref=v1.2`. A query value whose name no parameter declares, and an empty query value, pass nothing.

The other triggers pass no parameters, so their firings get the defaults.

Once an event declares parameters, a firing can pass only those, and `kickd event` refuses other names with exit code 2.
The command receives every declared parameter, with the value of the firing, its default, or an empty value.
An event without `params` accepts any names from `kickd event`, made of letters, digits and `_`, and webhook requests pass no parameters to it.

`kickd event` reads the config file each time it runs, so it checks a firing against the parameters of the file as it is.
Webhook requests use the parameters of the config that the long-running kickd process, the agent, has loaded, which changes when the file is saved.

## `name`

`name` identifies the parameter.

- **Type**: a string of 1 to 64 characters: letters, digits and `_`, not starting with a digit.
- **Default**: none. `name` is required, and no two parameters of one event can have the same name.
- **Takes effect**: at once for `kickd event`, and for webhook requests when the config file is saved.

The command receives the value in the environment variable `KICKD_DATA_` followed by the name in capitals, so `ref` arrives in `KICKD_DATA_REF`.
The same value is in the variable `KICKD_DATA`, a JSON object of every parameter, and in the `data` field of the payload JSON.
In the command line and the query, the name is written as the config file writes it.

```yaml
events:
  - name: backup
    command: 'rsync -a "$KICKD_DATA_SOURCE/" ~/backup/'
    params:
      - name: source
        default: '/home/me/work'
    triggers:
      - type: manual
```

## `required`

`required` makes a firing without the parameter an error.

- **Type**: `true` or `false`, written without quotes.
- **Default**: `false`.
- **Takes effect**: at once for `kickd event`, and for webhook requests when the config file is saved.

A firing that lacks a required parameter does not run:

- **`kickd event`** exits with code 2 and prints `parameter NAME is required`.
- **A webhook request** gets 400 with `{"error":"missing parameter NAME"}`.

A required parameter cannot have a `default`, because a default would always supply it.
An event with a required parameter cannot have cron, file, after, startup or wake triggers, because those triggers pass no parameters and each of their firings would fail.

```yaml
events:
  - name: restore
    command: 'rsync -a "$HOME/backup/$KICKD_DATA_DATE/" "$HOME/restore/"'
    params:
      - name: date
        required: true
    triggers:
      - type: manual
```

## `default`

`default` is the value of the parameter when a firing does not pass one.

- **Type**: a string. A value is the text that the file gives, so `7` and `'7'` are the same value.
- **Default**: none, which gives an empty value to a firing that does not pass the parameter.
- **Takes effect**: at once for `kickd event`, and for webhook requests when the config file is saved.

Firings of cron, file, after, startup and wake triggers always get the default, because these triggers pass no parameters.
For a webhook request, an empty query value, as in `?ref=`, counts as not passed, so the default applies.

```yaml
events:
  - name: cleanup
    command: 'find "$HOME/tmp" -type f -mtime "+$KICKD_DATA_DAYS" -delete'
    params:
      - name: days
        default: '7'
    triggers:
      - type: cron
        schedule: '0 4 * * *'
      - type: manual
```

## `description`

`description` is a text about the parameter, for people.

- **Type**: a string.
- **Default**: none.
- **Takes effect**: at once for `kickd events`, which reads the config file each time it runs.

`kickd events --json` shows it among the parameters of the event.
The agent does not use it.
