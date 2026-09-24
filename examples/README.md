# Examples

[Documentation index](../README.md#documentation)

Each file here is a complete kickd config for one task.
Copy a file to the location of the config file, or copy its events into an existing config, then adjust the paths and the commands.
The comments at the top of each file say what the example needs and how to install the service.

| File | What it does | Automatic triggers | Runs on |
|---|---|---|---|
| [scan-inbox.yaml](scan-inbox.yaml) | Adds a text layer to scanned PDFs with ocrmypdf and files them away | File | macOS, Linux |
| [transcode-videos.yaml](transcode-videos.yaml) | Converts camera clips to MP4 with ffmpeg | File | macOS, Linux |
| [notes-git-sync.yaml](notes-git-sync.yaml) | Commits, pulls and pushes a folder of notes | File, cron | macOS, Linux |
| [mac-screenshots.yaml](mac-screenshots.yaml) | Sorts screenshots into one folder per month | File | macOS |
| [github-deploy.yaml](github-deploy.yaml) | Deploys an app when GitHub reports a push to main | Webhook | Linux |
| [wake-on-lan.yaml](wake-on-lan.yaml) | Wakes a machine on the home network from a phone | Webhook | macOS, Linux |
| [restic-backup.yaml](restic-backup.yaml) | Backs up /home with restic, and thins out old snapshots | Cron | Linux |
| [certbot-renew.yaml](certbot-renew.yaml) | Renews Let's Encrypt certificates and reloads nginx | Cron | Linux |
| [disk-space-alert.yaml](disk-space-alert.yaml) | Sends a push notification through ntfy when a disk is 90% full | Cron | Linux |
| [postgres-dump.yaml](postgres-dump.yaml) | Dumps a PostgreSQL database every night, or any database on demand | Cron | Linux |
| [windows-mirror.yaml](windows-mirror.yaml) | Mirrors a Documents folder to a second drive with robocopy | Cron | Windows |
| [windows-cleanup.yaml](windows-cleanup.yaml) | Deletes old downloads with PowerShell | Cron | Windows |

Every event can also be fired by hand with `kickd event NAME`.
