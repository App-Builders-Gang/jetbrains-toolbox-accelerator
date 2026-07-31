<#
.SYNOPSIS
  jtaccel installer for Windows.

.DESCRIPTION
  Downloads the correct release binary, verifies its SHA-256 against the published
  checksums, installs it to %LOCALAPPDATA%\jtaccel and adds that to the user PATH,
  then runs `jtaccel install` to wire up Toolbox.

  Re-running this script updates to the latest release.

  Run from PowerShell:
    irm https://github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/releases/latest/download/install.ps1 | iex
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$Repo = 'App-Builders-Gang/jetbrains-toolbox-accelerator'
$Base = "https://github.com/$Repo/releases/latest/download"

# Determine the target asset. PowerShell on Windows reports ARM64 as 'Arm64'.
$arch = if ([Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture -band 0xE) {
    # Fallback for older .NET that lacks the enum member.
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'ARM64' { 'arm64' }
        default { 'amd64' }
    }
} else {
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
}
$asset = "jtaccel-windows-$arch.exe"

$tmp = Join-Path $env:TEMP "jtaccel-install-$(New-Guid)"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

Write-Host "Downloading $asset..."
$exePath = Join-Path $tmp $asset
$sumsPath = Join-Path $tmp 'SHA256SUMS'
Invoke-WebRequest -Uri "$Base/$asset" -OutFile $exePath
Invoke-WebRequest -Uri "$Base/SHA256SUMS" -OutFile $sumsPath

# Verify the checksum: read the line for our asset and compare the hash.
$sums = Get-Content $sumsPath
$expected = ($sums | Select-String -Pattern "\s$asset$").Line.Split(' ')[0]
if (-not $expected) { throw "Could not find $asset in SHA256SUMS" }
$actual = (Get-FileHash $exePath -Algorithm SHA256).Hash.ToLower()
if ($expected.ToLower() -ne $actual) {
    throw "Checksum mismatch! expected=$expected actual=$actual"
}
Write-Host "Checksum OK."

$installDir = Join-Path $env:LOCALAPPDATA 'jtaccel'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$dest = Join-Path $installDir 'jtaccel.exe'
Move-Item -Path $exePath -Destination $dest -Force

# Add to the user PATH if not already present (User-scope, no admin).
$path = [Environment]::GetEnvironmentVariable('Path', 'User')
$rel = ";$installDir;"
if (-not ";$path;".Contains($rel)) {
    [Environment]::SetEnvironmentVariable('Path', "$path;$installDir", 'User')
    Write-Host "Added $installDir to PATH (new shells only)"
}
$env:Path = "$env:Path;$installDir"

Write-Host "`nInstalled jtaccel to $dest"
Write-Host "Configuring Toolbox..."
& $dest install

Write-Host ""
Write-Host "Done. Toolbox downloads are now accelerated."
Write-Host ""
Write-Host "Watch it work:   jtaccel status"
Write-Host "Foreground log:  jtaccel run"
Write-Host "Undo everything: jtaccel uninstall"
