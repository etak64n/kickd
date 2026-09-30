[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Write-Output "event=$env:KICKD_EVENT"
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "msg=$env:KICKD_DATA_MSG"
Write-Output "dir=$((Get-Location).Path)"
Write-Output ('payload=' + (Get-Content -Raw -Encoding UTF8 -LiteralPath $env:KICKD_PAYLOAD_FILE))
[Console]::Error.WriteLine('to stderr')
exit [int]$env:KICKD_DATA_CODE
