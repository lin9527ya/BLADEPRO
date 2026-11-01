[CmdletBinding()]
param(
  [string]$InstallDir = "C:\BLADEPRO",
  [string]$PgHost = "127.0.0.1",
  [int]$PgPort = 5433,
  [string]$PgUser = "bladepro",
  [string]$PgDatabase = "bladepro",
  [string]$PgAdminUser = "postgres",
  [switch]$CreateDatabase,
  [switch]$RegisterTask,
  [switch]$SkipPrereqCheck,
  [SecureString]$DatabasePassword
)

$ErrorActionPreference = "Stop"

function Fail([string]$Message) { throw $Message }
function Info([string]$Message) { Write-Host ("[+] " + $Message) }
function Warn([string]$Message) { Write-Warning $Message }

$exe = Join-Path $InstallDir "bladepro.exe"
$startBat = Join-Path $InstallDir "start.bat"
if (-not (Test-Path -LiteralPath $exe)) { Fail "bladepro.exe not found in $InstallDir. Extract the Windows release zip there first." }
if (-not (Test-Path -LiteralPath $startBat)) { Fail "start.bat not found in $InstallDir." }

$probe = Join-Path $InstallDir (".bladepro-install-probe-" + [guid]::NewGuid().ToString("N"))
try { New-Item -ItemType File -Path $probe -Force | Out-Null } catch { Fail "Install directory is not writable: $InstallDir" }
Remove-Item -LiteralPath $probe -Force -ErrorAction SilentlyContinue

if (-not $DatabasePassword) {
  $DatabasePassword = Read-Host ("Database password for {0} (input hidden)" -f $PgUser) -AsSecureString
}
$dbPass = [Runtime.InteropServices.Marshal]::PtrToStringBSTR([Runtime.InteropServices.Marshal]::SecureStringToBSTR($DatabasePassword))
if (-not $dbPass) { Fail "Database password cannot be empty." }

$configPath = Join-Path $InstallDir "config.json"
$example = Join-Path $InstallDir "config.example.json"
if (-not (Test-Path -LiteralPath $configPath)) {
  if (-not (Test-Path -LiteralPath $example)) { Fail "config.example.json not found in $InstallDir." }
  Copy-Item -LiteralPath $example -Destination $configPath
  Info "Created config.json from config.example.json"
}
$cfg = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
if (-not $cfg.database) { $cfg | Add-Member -NotePropertyName database -NotePropertyValue ([pscustomobject]@{}) }
$cfg.database.host = $PgHost
$cfg.database.port = $PgPort
$cfg.database.user = $PgUser
$cfg.database.password = $dbPass
$cfg.database.dbname = $PgDatabase
if (-not $cfg.database.sslmode) { $cfg.database.sslmode = "disable" }
# Empty means: use the packaged skills/ directory next to bladepro.exe.
$cfg.skill_dir = ""
$json = $cfg | ConvertTo-Json -Depth 10
[IO.File]::WriteAllText($configPath, $json, (New-Object Text.UTF8Encoding($false)))
Info "Wrote $configPath (skill_dir empty; database set to $PgHost`:$PgPort/$PgDatabase)"

# Best-effort ACL: keep the install tree private to the current user.
try {
  $who = "$env:USERDOMAIN\$env:USERNAME"
  & icacls $InstallDir /inheritance:r /grant:r "${who}:(OI)(CI)F" | Out-Null
  Info "Restricted install directory ACL to $who"
} catch { Warn "icacls failed; check ACLs manually for config.json and jwt.key." }

if ($CreateDatabase) {
  if ($PgUser -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { Fail "PgUser must be a simple PostgreSQL identifier." }
  if ($PgDatabase -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { Fail "PgDatabase must be a simple PostgreSQL identifier." }
  $psqlCmd = Get-Command psql -ErrorAction SilentlyContinue
  $psql = if ($psqlCmd) { $psqlCmd.Source } else {
    (Get-ChildItem "$env:ProgramFiles\PostgreSQL\*\bin\psql.exe" -ErrorAction SilentlyContinue | Sort-Object FullName -Descending | Select-Object -First 1).FullName
  }
  if (-not $psql -or -not (Test-Path -LiteralPath $psql)) { Fail "psql.exe not found; create the role/database manually or install PostgreSQL client tools." }
  $adminSecure = Read-Host ("PostgreSQL admin password for {0}" -f $PgAdminUser) -AsSecureString
  $adminPass = [Runtime.InteropServices.Marshal]::PtrToStringBSTR([Runtime.InteropServices.Marshal]::SecureStringToBSTR($adminSecure))
  $env:PGPASSWORD = $adminPass
  try {
    $roleExists = (& $psql -h $PgHost -p $PgPort -U $PgAdminUser -d postgres -tAc "SELECT 1 FROM pg_roles WHERE rolname='$PgUser'").Trim()
    $escaped = $dbPass.Replace("'", "''")
    if ($roleExists -eq "1") {
      & $psql -h $PgHost -p $PgPort -U $PgAdminUser -d postgres -v ON_ERROR_STOP=1 -c "ALTER ROLE `"$PgUser`" LOGIN PASSWORD '$escaped';"
    } else {
      & $psql -h $PgHost -p $PgPort -U $PgAdminUser -d postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE `"$PgUser`" LOGIN PASSWORD '$escaped';"
    }
    $dbExists = (& $psql -h $PgHost -p $PgPort -U $PgAdminUser -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$PgDatabase'").Trim()
    if ($dbExists -ne "1") {
      & $psql -h $PgHost -p $PgPort -U $PgAdminUser -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE `"$PgDatabase`" OWNER `"$PgUser`";"
    }
    Info "PostgreSQL role/database ready (owner: $PgUser)"
  } finally {
    Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
  }
}

if (-not $SkipPrereqCheck) {
  if (-not (Get-Command node -ErrorAction SilentlyContinue)) { Warn "Node.js not found: Playwright browser MCP will not start. Install Node LTS if you need it." }
  if (-not (Get-Command chrome.exe -ErrorAction SilentlyContinue) -and -not (Get-Command msedge.exe -ErrorAction SilentlyContinue)) {
    Warn "Chrome/Edge not found on PATH. Set BLADEPRO_CHROMIUM to a full chrome.exe/msedge.exe path if screenshots are needed."
  }
}

if ($RegisterTask) {
  $log = Join-Path $InstallDir "bladepro.log"
  $action = New-ScheduledTaskAction -Execute "cmd.exe" -Argument ('/c ""{0}" >> "{1}" 2>&1"' -f $startBat, $log) -WorkingDirectory $InstallDir
  $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
  $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
  Register-ScheduledTask -TaskName "BLADEPRO" -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null
  Info "Registered interactive logon task BLADEPRO"
}

Info "For local HTTP/SSE MCP servers set BLADEPRO_MCP_ALLOW_LOCAL=1 in the user environment (setx)."
Info "Next: run '$startBat', open http://127.0.0.1:9898, then import the MITM CA after capture is enabled:"
Info "  powershell -ExecutionPolicy Bypass -File deploy\windows\import-ca.ps1 -InstallDir '$InstallDir'"
