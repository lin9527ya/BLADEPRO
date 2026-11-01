@echo off
rem Control the portable PostgreSQL used by the no-admin Windows deployment.
rem
rem ASCII only: cmd.exe reads .bat in the OEM code page, UTF-8 text desyncs it.
rem
rem Usage: pg-ctl.bat [start^|stop^|status^|restart]

setlocal
set "ROOT=%~dp0..\.."
set "PGBIN=%ROOT%\pgsql\bin"
set "DATA=%ROOT%\pgdata"
set "LOG=%DATA%\server.log"

if not exist "%PGBIN%\pg_ctl.exe" goto nopg
if not exist "%DATA%\PG_VERSION" goto nodata

set "ACTION=%~1"
if "%ACTION%"=="" set "ACTION=status"

if /i "%ACTION%"=="start" goto start
if /i "%ACTION%"=="stop" goto stop
if /i "%ACTION%"=="restart" goto restart
if /i "%ACTION%"=="status" goto status
echo [pg] unknown action: %ACTION%
exit /b 2

:start
"%PGBIN%\pg_isready.exe" -h 127.0.0.1 -p 5433 >nul 2>&1
if not errorlevel 1 (
	echo [pg] already running on 127.0.0.1:5433
	exit /b 0
)
echo [pg] starting portable PostgreSQL from %DATA%
start "" /b "%PGBIN%\pg_ctl.exe" -D "%DATA%" -l "%LOG%" start
exit /b 0

:stop
"%PGBIN%\pg_ctl.exe" -D "%DATA%" -m fast -w stop
exit /b %ERRORLEVEL%

:restart
call "%~f0" stop
call "%~f0" start
exit /b 0

:status
"%PGBIN%\pg_isready.exe" -h 127.0.0.1 -p 5433
exit /b %ERRORLEVEL%

:nopg
echo [pg] portable PostgreSQL not found under %ROOT%\pgsql
echo [pg] this helper is for the source-tree deployment; a zip install normally uses a Windows service
exit /b 1

:nodata
echo [pg] data directory not initialized: %DATA%
exit /b 1
