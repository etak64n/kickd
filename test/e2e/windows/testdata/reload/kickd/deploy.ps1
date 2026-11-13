# Deploys to $env:TARGET once the release is ready: when the file ready
# exists.
Write-Output "target=$env:TARGET"
while (-not (Test-Path ready)) { Start-Sleep -Milliseconds 100 }
Write-Output 'deployed'
