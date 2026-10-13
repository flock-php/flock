# Builds the Flock tray app into windows\dist\FlockTray-<arch>\ (self-contained: no .NET or
# Windows App SDK install needed on the target machine).
#
#   .\windows\build.ps1               # for this machine's architecture
#   .\windows\build.ps1 -Arch arm64
#
# Needs the .NET 10 SDK (winget install Microsoft.DotNet.SDK.10).
param(
    [ValidateSet("x64", "arm64")]
    [string]$Arch = $(if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x64" })
)
$ErrorActionPreference = "Stop"

$platform = if ($Arch -eq "arm64") { "ARM64" } else { "x64" }
$out = Join-Path $PSScriptRoot "dist\FlockTray-$Arch"

dotnet test (Join-Path $PSScriptRoot "FlockKit.Tests")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

dotnet publish (Join-Path $PSScriptRoot "FlockTray\FlockTray.csproj") -c Release -r "win-$Arch" -p:Platform=$platform -o $out
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "built $out\FlockTray.exe"
