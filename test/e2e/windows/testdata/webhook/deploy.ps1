# Reports the request that fired the event.
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "request=$env:KICKD_REQUEST_ID"
Write-Output ('body=' + (Get-Content -Raw -LiteralPath $env:KICKD_PAYLOAD_FILE))
