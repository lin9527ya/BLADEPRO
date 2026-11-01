[CmdletBinding()]
param(
  [string]$InstallDir = "C:\BLADEPRO",
  [string]$ConfigPath = "",
  [string]$Psql = "",
  [SecureString]$NewPassword
)

$ErrorActionPreference = "Stop"
if (-not $ConfigPath) { $ConfigPath = Join-Path $InstallDir "config.json" }
if (-not (Test-Path -LiteralPath $ConfigPath)) { throw "Config not found: $ConfigPath" }

$cfg = Get-Content -LiteralPath $ConfigPath -Raw | ConvertFrom-Json
$db = $cfg.database
if (-not $db) { throw "config.json has no database section" }
if ($db.dsn) { throw "DSN-form config is not supported by this helper; use psql manually." }

if (-not $Psql) {
  $cmd = Get-Command psql -ErrorAction SilentlyContinue
  if ($cmd) { $Psql = $cmd.Source }
  else {
    $candidate = Get-ChildItem "$env:ProgramFiles\PostgreSQL\*\bin\psql.exe" -ErrorAction SilentlyContinue |
      Sort-Object FullName -Descending | Select-Object -First 1
    if ($candidate) { $Psql = $candidate.FullName }
  }
}
if (-not $Psql -or -not (Test-Path -LiteralPath $Psql)) { throw "psql.exe not found; pass -Psql <path>." }

if (-not $NewPassword) {
  $NewPassword = Read-Host "New password for user BLADEPRO" -AsSecureString
  $confirm = Read-Host "Confirm password" -AsSecureString
  $a = [Runtime.InteropServices.Marshal]::PtrToStringBSTR([Runtime.InteropServices.Marshal]::SecureStringToBSTR($NewPassword))
  $b = [Runtime.InteropServices.Marshal]::PtrToStringBSTR([Runtime.InteropServices.Marshal]::SecureStringToBSTR($confirm))
  if ($a -ne $b) { throw "Passwords do not match" }
}
$plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR([Runtime.InteropServices.Marshal]::SecureStringToBSTR($NewPassword))
if (-not $plain) { throw "Password cannot be empty" }

$port = if ($db.port) { [string]$db.port } else { "5432" }
$hostName = if ($db.host) { [string]$db.host } else { "127.0.0.1" }
$user = [string]$db.user
$dbname = [string]$db.dbname
$sslmode = if ($db.sslmode) { [string]$db.sslmode } else { "disable" }

$env:PGPASSWORD = [string]$db.password
$env:PGSSLMODE = $sslmode
$env:BLADEPRO_RESET_NEWPASS = $plain
$sql = @'
\set ON_ERROR_STOP on
\getenv newpw BLADEPRO_RESET_NEWPASS
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO settings(key, value)
VALUES ('auth.password_hash', crypt(:'newpw', gen_salt('bf', 10)))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
'@

try {
  $sql | & $Psql -h $hostName -p $port -U $user -d $dbname
  if ($LASTEXITCODE -ne 0) { throw "psql failed with exit code $LASTEXITCODE" }
  Write-Host "Password reset. Login username is always BLADEPRO."
} finally {
  Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
  Remove-Item Env:BLADEPRO_RESET_NEWPASS -ErrorAction SilentlyContinue
}
