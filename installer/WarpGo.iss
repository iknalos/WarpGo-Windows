; WarpGo for Windows installer (Inno Setup 6). Built by CI:
;   iscc /DAppVersion=1.0.N installer\WarpGo.iss   ->  dist\WarpGo-Setup.exe
#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

[Setup]
AppId={{6E1C2B5A-57A4-4F52-9C47-5741525047AF}
AppName=WarpGo
AppVersion={#AppVersion}
AppVerName=WarpGo {#AppVersion}
AppPublisher=iknalos
AppPublisherURL=https://github.com/iknalos/WarpGo-Windows
AppSupportURL=https://github.com/iknalos/WarpGo-Windows/issues
AppUpdatesURL=https://github.com/iknalos/WarpGo-Windows/releases
AppComments=Free Cloudflare WARP client for Windows: WireGuard + MASQUE tunnel with Auto mode
VersionInfoVersion={#AppVersion}
VersionInfoDescription=WarpGo setup
DefaultDirName={autopf}\WarpGo
DefaultGroupName=WarpGo
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
OutputDir=..\dist
OutputBaseFilename=WarpGo-Setup
SetupIconFile=..\winres\icon.ico
UninstallDisplayIcon={app}\WarpGo.exe
UninstallDisplayName=WarpGo (Cloudflare WARP client)
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; The app holds this mutex while it runs, so setup asks to close it first.
AppMutex=WarpGoWindowsApp
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "..\dist\WarpGo.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\internal\wintun\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion
Source: "..\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\internal\wintun\LICENSE.txt"; DestDir: "{app}"; DestName: "wintun-LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\WarpGo"; Filename: "{app}\WarpGo.exe"; Comment: "Cloudflare WARP tunnel"
Name: "{autodesktop}\WarpGo"; Filename: "{app}\WarpGo.exe"; Tasks: desktopicon

[Run]
; Also runs after a silent in-app update, so WarpGo comes back by itself.
Filename: "{app}\WarpGo.exe"; Description: "{cm:LaunchProgram,WarpGo}"; Flags: nowait postinstall; Check: not NoLaunch

[UninstallRun]
Filename: "{sys}\schtasks.exe"; Parameters: "/Delete /F /TN WarpGo"; Flags: runhidden; RunOnceId: "DeleteStartupTask"

[UninstallDelete]
; WebView2 cache. Settings and WARP accounts in %AppData%\WarpGo are kept.
Type: filesandordirs; Name: "{localappdata}\WarpGo"

[Code]
// /NOLAUNCH skips starting WarpGo after install (used by CI).
function NoLaunch(): Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), '/NOLAUNCH') = 0 then
      Result := True;
end;
