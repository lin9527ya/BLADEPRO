@echo off
rem BLADEPRO launcher / supervisor (Windows).
rem
rem Why ASCII-only: cmd.exe reads .bat files in the OEM code page (GBK on
rem Chinese Windows), so UTF-8 comments desync the parser and corrupt the
rem script. Keep this file free of non-ASCII bytes.
rem
rem Usage:
rem   start.bat                  run in foreground (Ctrl-C stops)
rem   start.bat -addr :9000      extra args are passed through to bladepro
rem
rem It only does one thing: run bladepro.exe, then decide from the exit code
rem whether to restart it.
rem
rem   0      user stop        -> exit loop
rem   75     restart request  -> rerun at once (page one-click update/rollback)
rem   other  crash            -> rerun after backoff (1->2->4 ... max 60s)
rem
rem Download, SHA256 check and swapping the binary are not handled here: the
rem bladepro process does all of it at startup (selfupdate package).

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=bladepro.exe"
if not exist "%BIN%" (
	echo [bladepro] executable not found: %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
rem Explicit path: works even when NoDefaultCurrentDirectoryInExePath=1.
"%~dp0%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [bladepro] stopped normally
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem Update/rollback staged: the new binary is swapped in at next startup.
	echo [bladepro] restart requested - applying new version ...
	set /a delay=1
	goto loop
)

echo [bladepro] abnormal exit ^(code=!code!^), restarting in !delay!s 1>&2
rem "timeout" fails when the console is redirected; use ping as a fallback
rem (N seconds needs N+1 pings).
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
