# Prints the changes that fired the event.
Write-Output "file=$env:KICKD_FILE_PATH"
Write-Output "op=$env:KICKD_FILE_OP"
Write-Output "count=$env:KICKD_FILE_COUNT"
Write-Output "paths=$env:KICKD_FILE_PATHS"
