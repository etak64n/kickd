# Reports the run that ended.
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "after=$env:KICKD_AFTER_EVENT"
Write-Output "run=$env:KICKD_AFTER_RUN_ID"
Write-Output "status=$env:KICKD_AFTER_STATUS"
Write-Output "exit=$env:KICKD_AFTER_EXIT_CODE"
