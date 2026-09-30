# Reports the time that the schedule fired for.
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "scheduled=$env:KICKD_CRON_SCHEDULED_AT"
Write-Output "missed=$env:KICKD_CRON_MISSED"
