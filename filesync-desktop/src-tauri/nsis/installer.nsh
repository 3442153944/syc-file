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

    ; 开机自启项（tauri-plugin-autostart 写入 HKCU\...\Run，值为产品名）
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "私有云"
    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "私有云"
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "com.sunyuanling.filesync-desktop"

    ; 只卸载我们自己的 syc0 隧道，不动用户其它 WireGuard 隧道
    IfFileExists "$INSTDIR\wireguard\wireguard.exe" 0 wg_done
      ExecWait '"$INSTDIR\wireguard\wireguard.exe" /uninstalltunnelservice syc0'
    wg_done:
    Delete "$PROGRAMDATA\syc\syc0.conf"
    RMDir "$PROGRAMDATA\syc"

    ; 旧版本曾把私钥写到应用配置目录，卸载时一并清掉
    RmDir /r "$APPDATA\com.sunyuanling.filesync-desktop\vpn"
  ${EndIf}
!macroend
