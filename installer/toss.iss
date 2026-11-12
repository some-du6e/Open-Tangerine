; Per-user install: no admin prompt, lives in %LOCALAPPDATA%\Programs\Toss.
[Setup]
AppId={{6B0F2C1E-8C1D-4A57-9E1B-3D7A2F4C9B10}
AppName=Toss
AppVersion=0.1.0
AppPublisher=Toss
DefaultDirName={localappdata}\Programs\Toss
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\dist
OutputBaseFilename=Toss-Setup
UninstallDisplayIcon={app}\toss.exe
Compression=lzma2
SolidCompression=yes
CloseApplications=force
LicenseFile=..\LICENSE

[Tasks]
Name: startup; Description: "Start Toss when I sign in"

[Files]
Source: "..\dist\toss.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"

[Icons]
Name: "{autoprograms}\Toss"; Filename: "{app}\toss.exe"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "Toss"; ValueData: """{app}\toss.exe"""; Tasks: startup

[Run]
Filename: "{app}\toss.exe"; Description: "Start Toss now"; Flags: nowait postinstall

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/im toss.exe /f"; Flags: runhidden; RunOnceId: "StopToss"

[Code]
// The tray menu can also turn on "Start with Windows", so always clean it up.
procedure CurUninstallStepChanged(Step: TUninstallStep);
begin
  if Step = usPostUninstall then
    RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', 'Toss');
end;
