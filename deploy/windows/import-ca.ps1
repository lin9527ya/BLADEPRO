[CmdletBinding()]
param(
  [string]$InstallDir = "C:\BLADEPRO",
  [switch]$Remove
)

$ErrorActionPreference = "Stop"
$ca = Join-Path $InstallDir "data\traffic\_ca\mitmproxy-ca-cert.pem"

if ($Remove) {
  $certs = Get-ChildItem Cert:\CurrentUser\Root |
    Where-Object { $_.Subject -match '(?i)mitmproxy|bladepro' }
  if (-not $certs) {
    Write-Host "No BLADEPRO/mitmproxy certificate found in CurrentUser\Root."
    exit 0
  }
  foreach ($cert in $certs) {
    Write-Host ("Removing {0} {1}" -f $cert.Thumbprint, $cert.Subject)
    Remove-Item -LiteralPath ("Cert:\CurrentUser\Root\" + $cert.Thumbprint) -Force
  }
  exit 0
}

if (-not (Test-Path -LiteralPath $ca)) {
  throw "CA file not found: $ca`nStart BLADEPRO once with traffic capture enabled, then retry."
}

$cert = Import-Certificate -FilePath $ca -CertStoreLocation Cert:\CurrentUser\Root
Write-Host ("Imported {0} into CurrentUser\Root." -f $cert.Thumbprint)
Write-Host "Chrome/Edge, Go tools and .NET can now validate the recording proxy."
Write-Host "Remove with: .\import-ca.ps1 -InstallDir '$InstallDir' -Remove"
