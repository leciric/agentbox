; The native app's Windows installer (scripts/native-package.sh windows):
;
;   makensis -DVERSION=0.18.0 -DSOURCE=<dir with agentbox-native.exe, README.txt, LICENSE.txt>
;            -DOUTFILE=<setup.exe> native/packaging/windows/installer.nsi
;
; One click, like the Electron app's: it installs for this user alone, in
; %LOCALAPPDATA%\Programs\AgentBox Native, so Windows asks for no
; administrator, and the app can update itself in place afterwards
; (native/crates/app/src/update.rs swaps the .exe where it is). It adds a
; Start menu entry and an entry in "Installed apps" with an uninstaller, then
; opens the app. /S installs silently and opens nothing, for scripts
; (install.ps1) and for the packaging's own check; /D=<dir> installs
; elsewhere.
;
; The app is one .exe with the agentbox tool inside it, which it writes to
; %LOCALAPPDATA%\AgentBox\bin as it starts: that folder, and AgentBox's WSL
; distro, are AgentBox's own and stay when this app is uninstalled (the
; Electron app shares them).
;
; A copy that is running can't be overwritten, but Windows lets it be renamed:
; the old .exe goes aside as agentbox-native.exe.old-<ticks>, the name the
; app's own update uses, which the app removes the next time it starts
; (update.rs, clean_up).

Unicode true
SetCompressor /SOLID lzma
RequestExecutionLevel user
ManifestDPIAware true

!ifndef VERSION
  !error "VERSION is not set: -DVERSION=0.18.0"
!endif
!ifndef SOURCE
  !error "SOURCE is not set: -DSOURCE=<dir with agentbox-native.exe>"
!endif
!ifndef OUTFILE
  !define OUTFILE "AgentBox-Native-experimental-${VERSION}-windows-x64-setup.exe"
!endif
; VIProductVersion takes four numbers: 0.18.0-nightly.20261106.157 is 0.18.0.0.
!ifndef NUMERIC_VERSION
  !define NUMERIC_VERSION "0.0.0.0"
!endif

!define NAME "AgentBox Native (experimental)"
!define EXE "agentbox-native.exe"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\AgentBoxNative"

Name "${NAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\AgentBox Native"
InstallDirRegKey HKCU "${UNINSTALL_KEY}" "InstallLocation"
Icon "agentbox-native.ico"
UninstallIcon "agentbox-native.ico"
BrandingText "AgentBox ${VERSION}"
ShowInstDetails nevershow
ShowUninstDetails nevershow
AutoCloseWindow true

VIProductVersion "${NUMERIC_VERSION}"
VIAddVersionKey "ProductName" "${NAME}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "${NAME} setup"
VIAddVersionKey "LegalCopyright" "MIT licensed"

Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

!include "FileFunc.nsh"
!include "x64.nsh"

Function .onInit
  ; GPUI draws with Direct3D 11 on x64 Windows; WSL 2, which AgentBox runs
  ; in, needs 64-bit Windows too.
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "AgentBox needs 64-bit Windows." /SD IDOK
    Abort
  ${EndIf}
FunctionEnd

Section "Install"
  SetOutPath "$INSTDIR"
  ; The copy before this one goes; one that's running can't, and goes aside,
  ; where the app cleans it up.
  Delete "$INSTDIR\${EXE}"
  IfFileExists "$INSTDIR\${EXE}" 0 fresh
    System::Call "kernel32::GetTickCount() i .r0"
    Rename "$INSTDIR\${EXE}" "$INSTDIR\${EXE}.old-$0"
  fresh:
  File "/oname=${EXE}" "${SOURCE}\agentbox-native.exe"
  File "/oname=README.txt" "${SOURCE}\README.txt"
  File "/oname=LICENSE.txt" "${SOURCE}\LICENSE.txt"
  File "agentbox-native.ico"
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  CreateShortcut "$SMPROGRAMS\${NAME}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\agentbox-native.ico"

  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${NAME}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "AgentBox"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLInfoAbout" "https://downloads.agentbox.linting.dev"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\agentbox-native.ico"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "EstimatedSize" "$0"
SectionEnd

; Opened once it's installed, as the Electron app's installer does; never
; from a silent install.
Function .onInstSuccess
  IfSilent +2
    Exec '"$INSTDIR\${EXE}"'
FunctionEnd

Section "Uninstall"
  Delete "$SMPROGRAMS\${NAME}.lnk"
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\README.txt"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\agentbox-native.ico"
  Delete "$INSTDIR\Uninstall.exe"
  ; What an update or a reinstall left aside, and a download cut short.
  FindFirst $0 $1 "$INSTDIR\${EXE}.old-*"
  loop:
    StrCmp $1 "" done
    Delete "$INSTDIR\$1"
    FindNext $0 $1
    Goto loop
  done:
  FindClose $0
  Delete "$INSTDIR\.${EXE}.update*"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
SectionEnd
