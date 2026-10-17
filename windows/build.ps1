# Builds the Flock tray app into windows\dist\FlockTray-x64\ (self-contained: no .NET or
# Windows App SDK install needed on the target machine).
#
#   .\windows\build.ps1
#
# Only x64 is built for now; Windows on ARM runs it under emulation.
# Needs the .NET 10 SDK (winget install Microsoft.DotNet.SDK.10).
$ErrorActionPreference = "Stop"

$Arch = "x64"
$platform = "x64"
$out = Join-Path $PSScriptRoot "dist\FlockTray-$Arch"
# As in build-installer.ps1; Settings > About shows it.
$version = $env:FLOCK_VERSION
if (-not $version) { $version = git -C (Split-Path $PSScriptRoot -Parent) describe --tags --always --dirty 2>$null }
if (-not $version) { $version = "dev" }

dotnet test (Join-Path $PSScriptRoot "FlockKit.Tests")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

dotnet publish (Join-Path $PSScriptRoot "FlockTray\FlockTray.csproj") -c Release -r "win-$Arch" -p:Platform=$platform `
    "-p:InformationalVersion=$version" -o $out
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# Ours; the .NET and Windows App SDK files come signed by Microsoft.
if ($env:SIGN_METADATA) {
    $unsigned = Get-ChildItem $out -Recurse -Include *.exe, *.dll |
        Where-Object { (Get-AuthenticodeSignature $_.FullName).Status -eq "NotSigned" }
    if ($unsigned) {
        & (Join-Path $PSScriptRoot "sign.ps1") $unsigned.FullName
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
}

Write-Host "built $out\FlockTray.exe"
