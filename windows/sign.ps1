# Signs files with Artifact Signing, through SignTool and its plugin: SIGN_DLIB is the plugin
# (Azure.CodeSigning.Dlib.dll) and SIGN_METADATA its metadata.json (endpoint, account, certificate
# profile), as build.yml sets them up. Without them it does nothing, so local builds stay unsigned.
#
#   .\windows\sign.ps1 <file>...
#
# Also Inno Setup's SignTool (build-installer.ps1), for the installer and its uninstaller.
param([Parameter(Mandatory, ValueFromRemainingArguments)][string[]]$Files)
$ErrorActionPreference = "Stop"

if (-not $env:SIGN_METADATA) { exit 0 }

# The newest Windows SDK's; the plugin needs 10.0.22621.755 or later.
$signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" |
    Where-Object { $_.Directory.Parent.Name -as [version] } |
    Sort-Object { [version]$_.Directory.Parent.Name } | Select-Object -Last 1
if (-not $signtool) { throw "signtool.exe not found (Windows SDK)" }

# Timestamped: the certificates last three days, the signatures as long as the timestamp.
& $signtool.FullName sign /v /fd SHA256 /tr http://timestamp.acs.microsoft.com /td SHA256 `
    /dlib $env:SIGN_DLIB /dmdf $env:SIGN_METADATA @Files
exit $LASTEXITCODE
