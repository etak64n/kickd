# The server runs in the temporary folder, which it does not hold open.
$server = Start-Process -FilePath powershell -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 30' -WindowStyle Hidden -WorkingDirectory $env:TEMP -PassThru
Set-Content -LiteralPath server.pid -Value $server.Id
Write-Output 'started'
