# Sends a notification. This one reports what kickd passed it.
Write-Output "event=$env:KICKD_EVENT"
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "after=$env:KICKD_AFTER_EVENT"
Write-Output "status=$env:KICKD_AFTER_STATUS"
Write-Output "dir=$((Get-Location).Path)"
