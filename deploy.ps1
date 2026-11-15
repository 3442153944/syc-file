<#
.SYNOPSIS
  FileSync 一键入口：backend / web / android / harmony。

.USAGE
  powershell -NoProfile -File deploy.ps1 -Target backend -Force
  powershell -NoProfile -File deploy.ps1 -Target web -Build
  powershell -NoProfile -File deploy.ps1 -Target android
  powershell -NoProfile -File deploy.ps1 -Target harmony

.NOTES
  参数会原样透传给对应脚本；查看帮助：-Target backend -Help
#>
[CmdletBinding()]
param(
    [ValidateSet('', 'backend', 'web', 'android', 'harmony')]
    [string]$Target = '',
    [Parameter(ValueFromRemainingArguments = $true)]
    [object[]]$Rest
)

$ErrorActionPreference = 'Stop'
$Root = $PSScriptRoot

$map = @{
    backend = Join-Path $Root 'new_server\deploy-win.ps1'
    web     = Join-Path $Root 'new_server\deploy-web.ps1'
    android = Join-Path $Root 'Android\build.ps1'
    harmony = Join-Path $Root 'harmony\build.ps1'
}

function Show-Usage {
    Write-Host ''
    Write-Host 'FileSync 一键构建/部署' -ForegroundColor Cyan
    Write-Host ''
    Write-Host '  powershell -NoProfile -File deploy.ps1 -Target backend [-SkipBuild] [-NoSupervisor] [-Force]'
    Write-Host '  powershell -NoProfile -File deploy.ps1 -Target web [-Build] [-DistDir <dir>]'
    Write-Host '  powershell -NoProfile -File deploy.ps1 -Target android [-Tasks ...]'
    Write-Host '  powershell -NoProfile -File deploy.ps1 -Target harmony [-Hap]'
    Write-Host ''
    Write-Host '说明：'
    Write-Host '  backend : 构建 sys_core + Go 后端，生成 Windows 配置并启动（nginx 已反代 8991）'
    Write-Host '  web     : 把前端构建产物发布到 E:\fileSynchronization\web（-Build 先 pnpm build）'
    Write-Host '  android : 打包 Android（sys_core JNI + Gradle）'
    Write-Host '  harmony : 打包 HarmonyOS（sys_core prebuilts + HAP）'
    Write-Host ''
}

if (-not $Target) { Show-Usage; exit 2 }
$script = $map[$Target]
if (-not (Test-Path $script)) {
    Write-Host "找不到脚本: $script" -ForegroundColor Red
    exit 1
}

$cmdArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $script) + @($Rest)
Write-Host ''
Write-Host ("==> 一键 $Target : powershell " + ($cmdArgs -join ' ')) -ForegroundColor Cyan
$started = Get-Date
& powershell @cmdArgs
$code = $LASTEXITCODE
$elapsed = [math]::Round(((Get-Date) - $started).TotalSeconds, 1)
if ($code -eq 0) {
    Write-Host "`n[ok] $Target 完成，用时 ${elapsed}s" -ForegroundColor Green
} else {
    Write-Host "`n[x] $Target 失败 (exit $code)，用时 ${elapsed}s" -ForegroundColor Red
}
exit $code
