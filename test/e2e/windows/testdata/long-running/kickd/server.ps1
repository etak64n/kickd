# Runs until stop.ps1 leaves the file stop, and then removes it.
Remove-Item -LiteralPath stop -ErrorAction SilentlyContinue
Set-Content -LiteralPath server.pid -Value $PID
Write-Output "$env:GREETING from $env:KICKD_TRIGGER"
while (-not (Test-Path -LiteralPath stop)) { Start-Sleep -Milliseconds 100 }
Write-Output "stopped by $(Get-Content -LiteralPath stop)"
Remove-Item -LiteralPath stop
