# Deploys the app. This one reports what kickd passed it.
Write-Output "event=$env:KICKD_EVENT"
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "dir=$((Get-Location).Path)"
