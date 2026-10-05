#Requires -RunAsAdministrator
<#
.SYNOPSIS
Removes the Claimward VPN app and its helper service.

.DESCRIPTION
Stops and removes the ClaimwardHelper service (which takes the tunnel down),
the Start Menu shortcut and "C:\Program Files\Claimward". With -RemoveData it
also removes C:\ProgramData\Claimward (helper.json, the log) and the local
group "Claimward Users". Each person's own settings and session under
%AppData%\Claimward are theirs and are left alone.
#>
param([switch]$RemoveData)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3

$InstallDir = Join-Path $env:ProgramFiles 'Claimward'
$helperExe = Join-Path $InstallDir 'claimward-helper.exe'
if (Get-Service -Name 'ClaimwardHelper' -ErrorAction SilentlyContinue) {
    if (Test-Path -LiteralPath $helperExe) {
        & $helperExe uninstall
        if ($LASTEXITCODE -ne 0) { throw "claimward-helper uninstall failed ($LASTEXITCODE)" }
    } else {
        Stop-Service -Name 'ClaimwardHelper' -Force
        & sc.exe delete ClaimwardHelper | Out-Null
    }
}
Get-Process -Name 'claimward-app' -ErrorAction SilentlyContinue | Stop-Process -Force
Remove-Item -LiteralPath (Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\Claimward VPN.lnk') -ErrorAction SilentlyContinue
# The uninstaller may be running from the folder it removes: copy it out first if so.
Remove-Item -LiteralPath $InstallDir -Recurse -Force -ErrorAction SilentlyContinue
if ($RemoveData) {
    Remove-Item -LiteralPath (Join-Path $env:ProgramData 'Claimward') -Recurse -Force -ErrorAction SilentlyContinue
    Remove-LocalGroup -Name 'Claimward Users' -ErrorAction SilentlyContinue
}
Write-Host 'Claimward VPN is removed.'
