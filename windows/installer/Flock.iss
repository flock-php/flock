; Flock-Setup.exe: installs the tray app with the flock CLI next to it, for the current user only
; (no administrator rights). The app sets Flock up on its first start (`flock install`, one UAC
; prompt for the hosts helper and the CA). Built by windows\build-installer.ps1, which passes
; AppVersion, Arch (x64 | arm64) and GoArch (amd64 | arm64).

#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef Arch
  #define Arch "x64"
#endif
#ifndef GoArch
  #define GoArch "amd64"
#endif

[Setup]
AppId={{BD44E167-D13B-4AD9-8DA7-E976DEFCFB50}
AppName=Flock
AppVersion={#AppVersion}
AppPublisher=Flock
DefaultDirName={localappdata}\Programs\Flock
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif
OutputDir=..\dist
OutputBaseFilename=Flock-Setup-{#Arch}
SetupIconFile=..\FlockTray\Assets\AppIcon.ico
UninstallDisplayIcon={app}\FlockTray.exe
UninstallDisplayName=Flock
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
; The tray app is stopped in PrepareToInstall; the Restart Manager would ask the user instead.
CloseApplications=no

[Tasks]
Name: startup; Description: "Open Flock when I sign in"

[Files]
Source: "..\dist\FlockTray-{#Arch}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
Source: "..\..\dist\windows-{#GoArch}\flock.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows-{#GoArch}\flock-helper.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{userprograms}\Flock"; Filename: "{app}\FlockTray.exe"

[Registry]
; The same value the app's "Open Flock at sign-in" setting writes (removed again on uninstall).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "Flock"; ValueData: """{app}\FlockTray.exe"" --background"; Tasks: startup

[Run]
Filename: "{app}\FlockTray.exe"; Description: "Open Flock"; Flags: postinstall nowait skipifsilent

[Code]
// The running tray app locks its files; an update replaces them, and the new app starts afterwards.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM FlockTray.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if ResultCode = 0 then
    Sleep(500);
  Result := '';
end;

// /PURGE: the app's uninstall dialog, when the user also wants their data deleted.
// /SKIPFLOCK: `flock uninstall` started this uninstaller after removing Flock itself.
function HasParam(Name: String): Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), Name) = 0 then
      Result := True;
end;

// Runs at uninstall time, before the app's files go: `flock uninstall` removes the daemon, PATH
// entry, hosts helper and CA trust (UAC prompt) and Flock's files, keeping the user's settings and
// databases unless /PURGE. Skipped when Flock was never set up (it would ask for permission).
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
  Params: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM FlockTray.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    if not HasParam('/SKIPFLOCK') and FileExists(ExpandConstant('{userappdata}\Flock\bin\flock.exe')) then
    begin
      Params := 'uninstall --keep-app';
      if HasParam('/PURGE') then
        Params := Params + ' --purge';
      Exec(ExpandConstant('{app}\flock.exe'), Params, '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    end;
  end;
  if CurUninstallStep = usPostUninstall then
    RegDeleteValue(HKEY_CURRENT_USER, 'Software\Microsoft\Windows\CurrentVersion\Run', 'Flock');
end;
