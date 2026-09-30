@echo off
chcp 65001 >nul
echo event=%KICKD_EVENT%
echo trigger=%KICKD_TRIGGER%
echo msg=%KICKD_DATA_MSG%
echo dir=%CD%
set /p "=payload=" <nul
type "%KICKD_PAYLOAD_FILE%"
echo.
echo to stderr 1>&2
exit /b %KICKD_DATA_CODE%
