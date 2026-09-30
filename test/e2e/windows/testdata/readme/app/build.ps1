# Builds the app. This one reports what kickd passed it.
Write-Output "event=$env:KICKD_EVENT"
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "file=$env:KICKD_FILE_PATH"
