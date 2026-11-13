# The first attempt runs for a minute, and a rerun ends at once.
Write-Output "attempt=$env:KICKD_ATTEMPT"
if ($env:KICKD_ATTEMPT -eq '1') { Start-Sleep -Seconds 60 }
