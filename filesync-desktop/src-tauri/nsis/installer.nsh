; installer.nsh - custom NSIS hooks for filesync-desktop
;
; PREUNINSTALL runs at the beginning of the uninstall section in two situations:
;   1. Manual uninstall (control panel): uninstaller is launched plainly,
;      command line has no _?= flag -> full cleanup (log, config).
;   2. Upgrade / reinstall: our installer runs the old uninstaller in place
;      with the _?= flag -> keep config and logs, user settings must survive
;      the update (only app files get replaced).
;
; "$INSTDIR\sync" is never removed in either case: it contains user-synced files.

!macro NSIS_HOOK_PREUNINSTALL
  ${GetOptions} $CMDLINE "_?=" $0
  ${If} ${Errors}
    RmDir /r "$INSTDIR\log"
    RmDir /r "$INSTDIR\config"
  ${EndIf}
!macroend
