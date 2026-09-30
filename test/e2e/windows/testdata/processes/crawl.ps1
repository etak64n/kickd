$crawler = Start-Process -FilePath powershell -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 60' -WindowStyle Hidden -PassThru
Set-Content -LiteralPath crawler.pid -Value $crawler.Id
$crawler.WaitForExit()
