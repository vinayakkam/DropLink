; DropLink installer (Inno Setup 6.6 or later). Built by tools/release:
;
;   ISCC /DAppVersion=1.2.3 /DFileVersion=1.2.3.0 /DStageDir=<stage> /DOutputDir=<out> packaging\windows\droplink.iss
;
; <stage> holds droplink.exe (the Tauri shell) and droplink-sidecar.exe.
;
; Per-user install: no administrator rights, nothing written outside the
; user's profile. The wizard follows Windows' light or dark mode and shows
; DropLink's pixel art (packaging/windows/art, drawn by tools/installerart).
; DropLink's own updater runs this installer with /SILENT /RELAUNCH=1: it
; closes the running app, installs over it and starts it again.

#ifndef AppVersion
  #error Pass /DAppVersion=x.y.z (desktop/src-tauri/tauri.conf.json's version)
#endif
#ifndef FileVersion
  #error Pass /DFileVersion=x.y.z.0
#endif
#ifndef StageDir
  #error Pass /DStageDir=<directory holding droplink.exe and droplink-sidecar.exe>
#endif
#ifndef OutputDir
  #define OutputDir "out"
#endif
#define RepoDir AddBackslash(SourcePath) + "..\.."
#define Art AddBackslash(SourcePath) + "art\"

[Setup]
AppId={{DC0033FF-5018-45BA-A0DF-D3422C656703}
AppName=DropLink
AppVersion={#AppVersion}
AppVerName=DropLink {#AppVersion}
AppPublisher=DropLink contributors
AppPublisherURL=https://github.com/vinayakkam/DropLink
AppSupportURL=https://github.com/vinayakkam/DropLink/issues
AppUpdatesURL=https://github.com/vinayakkam/DropLink/releases
VersionInfoVersion={#FileVersion}
VersionInfoProductVersion={#FileVersion}
VersionInfoDescription=DropLink installer
DefaultDirName={autopf}\DropLink
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
; The updater relaunches the app itself; offer to close a running copy.
CloseApplications=yes
RestartApplications=no
UsePreviousTasks=yes
LicenseFile={#RepoDir}\LICENSE
SetupIconFile={#RepoDir}\desktop\src-tauri\icons\icon.ico
UninstallDisplayIcon={app}\droplink.exe
UninstallDisplayName=DropLink
OutputDir={#OutputDir}
OutputBaseFilename=droplink-{#AppVersion}-windows-x64-setup
Compression=lzma2/max
SolidCompression=yes

; The look: Windows' light or dark mode, DropLink's pixel scene behind
; every page and the app icon on a tile, as in the app.
WizardStyle=modern dynamic
WizardBackImageFile={#Art}back-light.png,{#Art}back-light@2x.png
WizardBackImageFileDynamicDark={#Art}back-dark.png,{#Art}back-dark@2x.png
WizardBackColor=#eef9fc
WizardBackColorDynamicDark=#05070d
WizardImageFile={#Art}welcome-light.png
WizardImageFileDynamicDark={#Art}welcome-dark.png
WizardImageBackColor=#eef9fc
WizardImageBackColorDynamicDark=#05070d
WizardSmallImageFile={#Art}small-light.png,{#Art}small-light@2x.png
WizardSmallImageFileDynamicDark={#Art}small-dark.png,{#Art}small-dark@2x.png
WizardSmallImageBackColor=#eef9fc
WizardSmallImageBackColorDynamicDark=#05070d

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Messages]
WelcomeLabel1=Welcome to DropLink
WelcomeLabel2=DropLink sends files, folders and text straight from one device to another, over your local network or across the internet. No server ever stores your files.%n%nIt installs for you alone, with no administrator rights, and keeps itself up to date.%n%nClick Next to continue, or Cancel to exit Setup.
FinishedHeadingLabel=DropLink is ready for launch

[Tasks]
Name: desktopicon; Description: "Create a desktop shortcut"; Flags: unchecked
Name: startup; Description: "Start DropLink when I sign in (it waits in the tray for incoming cargo)"; Flags: unchecked

[Files]
Source: "{#StageDir}\droplink.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#StageDir}\droplink-sidecar.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#RepoDir}\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{userprograms}\DropLink"; Filename: "{app}\droplink.exe"
Name: "{userdesktop}\DropLink"; Filename: "{app}\droplink.exe"; Tasks: desktopicon

[Registry]
; droplink://join/<CODE> links open DropLink and join that room.
Root: HKCU; Subkey: "Software\Classes\droplink"; ValueType: string; ValueName: ""; ValueData: "URL:DropLink"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\droplink"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""
Root: HKCU; Subkey: "Software\Classes\droplink\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: """{app}\droplink.exe"",0"
Root: HKCU; Subkey: "Software\Classes\droplink\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\droplink.exe"" ""%1"""
; Start at sign-in, only when the task is ticked.
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "DropLink"; ValueData: """{app}\droplink.exe"""; Tasks: startup; Flags: uninsdeletevalue

[Run]
; A normal install offers to launch; an update (/RELAUNCH=1) always does.
Filename: "{app}\droplink.exe"; Description: "Launch DropLink"; Flags: nowait postinstall skipifsilent
Filename: "{app}\droplink.exe"; Flags: nowait; Check: RelaunchRequested

[UninstallDelete]
; Settings, keys and the trust list live in %APPDATA%\com.droplink.app and
; are kept, so a reinstall keeps your identity and trusted devices.
Type: dirifempty; Name: "{app}"

[Code]
function RelaunchRequested: Boolean;
begin
  Result := ExpandConstant('{param:RELAUNCH|0}') = '1';
end;
