# Examples

[Documentation index](../README.md#documentation)

Each file here is a complete kickd config for one task.
Copy a file to the location of the config file, or copy its events into an existing config, then adjust the paths and the commands.
The comments at the top of each file say what the example needs and how to install the service.
The `base_dir`, `log` and `database` sections at the top say where the log and the database of kickd go.

| File | What it does | Automatic triggers | Runs on |
|---|---|---|---|
| [github-deploy.yaml](github-deploy.yaml) | Deploys an app when GitHub reports a push to main | Webhook | Linux |
| [restart-service.yaml](restart-service.yaml) | Restarts a known service when a monitoring tool or a pipeline asks | Webhook | Linux |
| [rebuild-on-change.yaml](rebuild-on-change.yaml) | Runs make when the sources of a project change | File | macOS, Linux |
| [git-autocommit.yaml](git-autocommit.yaml) | Commits, pulls and pushes a folder kept in Git | File, cron | macOS, Linux |
| [transcode-videos.yaml](transcode-videos.yaml) | Converts video clips to MP4 with ffmpeg | File | macOS, Linux |
| [restic-backup.yaml](restic-backup.yaml) | Backs up /home with restic, and thins out old snapshots | Cron | Linux |
| [certbot-renew.yaml](certbot-renew.yaml) | Renews Let's Encrypt certificates and reloads nginx | Cron | Linux |
| [disk-space-alert.yaml](disk-space-alert.yaml) | Sends a push notification through ntfy when a disk is 90% full | Cron | Linux |
| [postgres-dump.yaml](postgres-dump.yaml) | Dumps a PostgreSQL database every night, or any database on demand | Cron | Linux |
| [windows-mirror.yaml](windows-mirror.yaml) | Mirrors a Documents folder to a second drive with robocopy | Cron | Windows |
| [windows-cleanup.yaml](windows-cleanup.yaml) | Deletes old downloads with PowerShell | Cron | Windows |

Every event can also be fired by hand with `kickd event NAME`.
