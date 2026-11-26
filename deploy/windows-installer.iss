; Inno Setup 6 script for IntelHub Windows Desktop Installer (.exe)
; Build locally or in GitHub Actions:
;   iscc /DMyAppVersion=1.0.0 deploy\windows-installer.iss

#ifndef MyAppVersion
  #define MyAppVersion "1.0.0"
#endif

#define MyAppName "IntelHub"
#define MyAppDisplayName "IntelHub 突发情报台"
#define MyAppPublisher "IntelHub"
#define MyAppExeName "intel_hub_client.exe"

[Setup]
AppId={{8F5E3B29-9D4A-4B8F-9B61-1B9E2E8D4C71}
AppName={#MyAppDisplayName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppDisplayName} v{#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={localappdata}\Programs\{#MyAppName}
DefaultGroupName={#MyAppDisplayName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputDir=..\dist
OutputBaseFilename=IntelHub-Setup-v{#MyAppVersion}-win64
SetupIconFile=..\client\windows\runner\resources\app_icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=force
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a Desktop shortcut (创建桌面快捷方式)"; GroupDescription: "Additional tasks:"; Flags: checkedonce
Name: "autostart"; Description: "Launch IntelHub automatically at Windows startup (开机自动启动)"; GroupDescription: "Additional tasks:"; Flags: unchecked

[Files]
Source: "..\client\build\windows\x64\runner\Release\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\{#MyAppDisplayName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppDisplayName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "IntelHub"; ValueData: """{app}\{#MyAppExeName}"""; Flags: uninsdeletevalue; Tasks: autostart

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "Launch {#MyAppDisplayName} now (立即启动)"; Flags: nowait postinstall skipifsilent
