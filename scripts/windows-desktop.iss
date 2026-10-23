; Built only from the manifest-bound native Windows staging tree.
#ifndef AppVersion
  #error AppVersion required
#endif
#ifndef StageDir
  #error StageDir required
#endif
#ifndef OutputDir
  #error OutputDir required
#endif
[Setup]
AppId={{F93EBAAA-EC43-4D71-9820-91B93C5D2B17}
AppName=Overkill
AppVersion={#AppVersion}
AppPublisher=Sahaj Tech Ltd
AppPublisherURL=https://github.com/Sahaj-Tech-ltd/overkill
DefaultDirName={localappdata}\Programs\Overkill
DefaultGroupName=Overkill
PrivilegesRequired=lowest
MinVersion=10.0
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutputDir}
OutputBaseFilename=Overkill-{#AppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\Overkill.exe
CloseApplications=yes
RestartApplications=no
SetupLogging=yes
LicenseFile={#StageDir}\resources\app\LICENSE
[Files]
Source: "{#StageDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
[Icons]
Name: "{group}\Overkill"; Filename: "{app}\Overkill.exe"
Name: "{group}\Uninstall Overkill"; Filename: "{uninstallexe}"
[Run]
Filename: "{app}\Overkill.exe"; Description: "Open Overkill"; Flags: nowait postinstall skipifsilent
