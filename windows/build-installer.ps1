# Builds Flock-Setup-<arch>.exe into windows\dist\: the flock CLI and hosts helper, the tray app
# (via build.ps1, which also runs the FlockKit tests) and the Inno Setup installer around them.
#
#   .\windows\build-installer.ps1               # for this machine's architecture
#   .\windows\build-installer.ps1 -Arch arm64
#
# Needs Go, the .NET 10 SDK and Inno Setup 6 (winget install GoLang.Go Microsoft.DotNet.SDK.10 JRSoftware.InnoSetup).
param(
    [ValidateSet("x64", "arm64")]
    [string]$Arch = $(if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x64" })
)
$ErrorActionPreference = "Stop"

$repo = Split-Path $PSScriptRoot -Parent
$goArch = if ($Arch -eq "arm64") { "arm64" } else { "amd64" }
$version = git -C $repo describe --tags --always --dirty 2>$null
if (-not $version) { $version = "dev" }

# Go and Inno Setup may have been installed after this terminal was opened.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { $env:Path = "$env:ProgramFiles\Go\bin;$env:Path" }
$iscc = @("$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe", "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe", "$env:ProgramFiles\Inno Setup 6\ISCC.exe") |
    Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) { throw "Inno Setup 6 not found (winget install JRSoftware.InnoSetup)" }

$out = Join-Path $repo "dist\windows-$goArch"
Push-Location $repo
try {
    $env:GOOS = "windows"; $env:GOARCH = $goArch
    go build -ldflags "-s -w -X flock/internal/cli.Version=$version" -o "$out\flock.exe" ./cmd/flock
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    go build -ldflags "-s -w -X main.version=$version" -o "$out\flock-helper.exe" ./cmd/flock-helper
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}

& (Join-Path $PSScriptRoot "build.ps1") -Arch $Arch
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& $iscc /Q "/DAppVersion=$version" "/DArch=$Arch" "/DGoArch=$goArch" (Join-Path $PSScriptRoot "installer\Flock.iss")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "built $(Join-Path $PSScriptRoot "dist\Flock-Setup-$Arch.exe")"
