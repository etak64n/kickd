# -NoNewWindow shares the console and the output of the script with the
# server.
$server = Start-Process -FilePath powershell -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 30' -NoNewWindow -WorkingDirectory $env:TEMP -PassThru
Set-Content -LiteralPath server.pid -Value $server.Id
Write-Output 'started'
