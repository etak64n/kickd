# Notes the attempt that the payload file holds, and the file, then syncs.
$attempt = (Get-Content -Raw -Encoding UTF8 $env:KICKD_PAYLOAD_FILE | ConvertFrom-Json).attempt
Add-Content -Path payloads.txt -Value "$attempt $env:KICKD_PAYLOAD_FILE"
Start-Sleep -Seconds 4
Write-Output 'synced'
