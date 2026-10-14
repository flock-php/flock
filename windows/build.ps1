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

dotnet test (Join-Path $PSScriptRoot "FlockKit.Tests")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

dotnet publish (Join-Path $PSScriptRoot "FlockTray\FlockTray.csproj") -c Release -r "win-$Arch" -p:Platform=$platform -o $out
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "built $out\FlockTray.exe"
