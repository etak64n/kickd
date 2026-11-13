# Asks the server to stop, and waits until it has stopped, as the stop
# command of a server does.
Set-Content -LiteralPath stop -Value $env:KICKD_STOP_REASON
while (Test-Path -LiteralPath stop) { Start-Sleep -Milliseconds 100 }
