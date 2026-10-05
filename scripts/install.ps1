#Requires -RunAsAdministrator
<#
.SYNOPSIS
Installs the Claimward VPN app and its privileged helper service.

.DESCRIPTION
Run from the unpacked release folder (claimward-app.exe, claimward-helper.exe,
wintun.dll beside this script), in an elevated PowerShell:

    .\install.ps1 -Server https://vpn.example.org

It
  1. copies the programs and wintun.dll to "C:\Program Files\Claimward"
     (writable by administrators only: the service runs from there);
  2. creates the local group "Claimward Users" and adds -User to it (the
     person who will use the app; by default whoever runs this);
  3. creates C:\ProgramData\Claimward with a protected ACL (SYSTEM and
     Administrators full control, the group list/traverse only) and writes
     helper.json there, naming the servers the helper may enroll with,
     writable by SYSTEM and Administrators alone;
  4. registers and starts the ClaimwardHelper service (LocalSystem);
  5. adds a "Claimward VPN" Start Menu shortcut for all users;
  6. with -Provider, writes the app's own configuration for the current user
     (%AppData%\Claimward\config.json), unless one exists.

Group membership takes effect at the person's next sign-in to Windows: until
then the helper's socket refuses them.

.PARAMETER Server
The claimward-vpn-server URL(s) the helper may enroll with. Required on a
first install; omitted, an existing helper.json is kept.

.PARAMETER User
The account to add to "Claimward Users". Empty: add nobody.
#>
param(
    [string[]]$Server,
    [string]$User = "$env:USERDOMAIN\$env:USERNAME",
    [string]$Source = $PSScriptRoot,
    [ValidateSet('', 'github', 'oidc', 'go-authn')]
    [string]$Provider = '',
    [string]$GitHubClientId = '',
    [string]$OidcIssuer = '',
    [string]$OidcClientId = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3

$ServiceName = 'ClaimwardHelper'
$GroupName = 'Claimward Users'
$InstallDir = Join-Path $env:ProgramFiles 'Claimward'
$DataDir = Join-Path $env:ProgramData 'Claimward'
$HelperConfig = Join-Path $DataDir 'helper.json'
$Shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\Claimward VPN.lnk'
$Utf8NoBom = New-Object System.Text.UTF8Encoding $false

function Set-Sddl([string]$Path, [string]$Sddl) {
    $acl = Get-Acl -LiteralPath $Path
    $acl.SetSecurityDescriptorSddlForm($Sddl)
    Set-Acl -LiteralPath $Path -AclObject $acl
}

# 1. The programs.
$files = 'claimward-app.exe', 'claimward-helper.exe', 'wintun.dll'
foreach ($f in $files) {
    if (-not (Test-Path -LiteralPath (Join-Path $Source $f))) { throw "$f is missing from $Source" }
}
$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing -and $existing.Status -ne 'Stopped') {
    Write-Host "Stopping $ServiceName to replace it"
    Stop-Service -Name $ServiceName -Force
}
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
foreach ($f in $files + 'LICENSE', 'wintun-LICENSE.txt', 'README.md', 'uninstall.ps1') {
    $p = Join-Path $Source $f
    if (Test-Path -LiteralPath $p) { Copy-Item -LiteralPath $p -Destination $InstallDir -Force }
}

# 2. The group allowed to use the helper's socket.
if (-not (Get-LocalGroup -Name $GroupName -ErrorAction SilentlyContinue)) {
    New-LocalGroup -Name $GroupName -Description 'May use the Claimward VPN helper' | Out-Null
    Write-Host "Created the local group '$GroupName'"
}
if ($User) {
    $members = @(Get-LocalGroupMember -Group $GroupName -ErrorAction SilentlyContinue | ForEach-Object { $_.Name })
    if ($members -notcontains $User) {
        Add-LocalGroupMember -Group $GroupName -Member $User
        Write-Host "Added $User to '$GroupName': it takes effect at their next sign-in to Windows"
    }
}
$groupSid = (Get-LocalGroup -Name $GroupName).SID.Value

# 3. The helper's directory and configuration. The helper sets the same ACL
# itself when it starts (owned by SYSTEM then); this makes helper.json safe
# from the moment it is written.
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
Set-Sddl $DataDir "O:BAG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;;0x1200a9;;;$groupSid)"
if ($Server) {
    foreach ($s in $Server) {
        $u = $null
        if (-not [Uri]::TryCreate($s, [UriKind]::Absolute, [ref]$u) -or ($u.Scheme -ne 'https' -and $u.Scheme -ne 'http')) {
            throw "not an http(s) URL: $s"
        }
    }
    $json = ConvertTo-Json -InputObject @{ servers = @($Server) }
    [System.IO.File]::WriteAllText($HelperConfig, $json, $Utf8NoBom)
    Write-Host "Wrote $HelperConfig"
} elseif (-not (Test-Path -LiteralPath $HelperConfig)) {
    throw "$HelperConfig does not exist: pass -Server https://your-claimward-server"
}
Set-Sddl $HelperConfig 'O:BAG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)'

# 4. The service.
$helperExe = Join-Path $InstallDir 'claimward-helper.exe'
if (-not $existing) {
    & $helperExe install -config $HelperConfig
    if ($LASTEXITCODE -ne 0) { throw "claimward-helper install failed ($LASTEXITCODE)" }
}
Start-Service -Name $ServiceName
Write-Host "$ServiceName is $((Get-Service -Name $ServiceName).Status)"

# 5. The Start Menu shortcut.
$shell = New-Object -ComObject WScript.Shell
$lnk = $shell.CreateShortcut($Shortcut)
$lnk.TargetPath = Join-Path $InstallDir 'claimward-app.exe'
$lnk.WorkingDirectory = $InstallDir
$lnk.Description = 'Claimward VPN'
$lnk.Save()
Write-Host "Added the Start Menu shortcut $Shortcut"

# 6. The app's own configuration, for the current user.
if ($Provider) {
    $appDir = Join-Path $env:APPDATA 'Claimward'
    $appConfig = Join-Path $appDir 'config.json'
    if (Test-Path -LiteralPath $appConfig) {
        Write-Host "Kept the existing $appConfig"
    } else {
        New-Item -ItemType Directory -Force -Path $appDir | Out-Null
        $cfg = [ordered]@{
            server_url       = @($Server)[0]
            provider         = $Provider
            github_client_id = $GitHubClientId
            oidc_issuer      = $OidcIssuer
            oidc_client_id   = $OidcClientId
        }
        [System.IO.File]::WriteAllText($appConfig, (ConvertTo-Json -InputObject $cfg), $Utf8NoBom)
        Write-Host "Wrote $appConfig"
    }
}
Write-Host 'Claimward VPN is installed. Start it from the Start Menu.'
