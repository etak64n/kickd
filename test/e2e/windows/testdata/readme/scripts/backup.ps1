# Copies C:\work to C:\backup\work. robocopy exits with 8 or more when it
# fails.
robocopy C:\work C:\backup\work /MIR
if ($LASTEXITCODE -ge 8) { exit $LASTEXITCODE }
exit 0
