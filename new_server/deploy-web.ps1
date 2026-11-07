<#
.SYNOPSIS
  Windows 一键部署 web（把前端构建产物发布到 E:\fileSynchronization\web）。

.USAGE
  powershell -NoProfile -File new_server\deploy-web.ps1 -Build
  powershell -NoProfile -File new_server\deploy-web.ps1 -DistDir D:\some\dist
  powershell -NoProfile -File new_server\deploy-web.ps1 -WebRoot E:\tmp\webtest -DistDir filesync-desktop\dist

.NOTES
  默认从 filesync-desktop\dist 部署；-Build 会先在 filesync-desktop 里执行 pnpm build。
  部署前把现有 web 目录整体备份到 E:\fileSynchronization\backup\web-<时间戳>；失败自动回滚。
#>
[CmdletBinding()]
param(
    [string]$DistDir = '',
    [string]$WebRoot = 'E:\fileSynchronization\web',
    [switch]$Build,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$Repo = Split-Path $PSScriptRoot
$WebRoot = [IO.Path]::GetFullPath($WebRoot)

function Step([string]$m) { Write-Host "`n==> $m" -ForegroundColor Cyan }
function Ok([string]$m) { Write-Host "  [ok] $m" -ForegroundColor Green }
function Warn([string]$m) { Write-Host "  [!] $m" -ForegroundColor Yellow }
function Die([string]$m) { Write-Host "  [x] $m" -ForegroundColor Red; exit 1 }

function Invoke-Robocopy([string]$src, [string]$dst, [switch]$Purge) {
    if (-not (Test-Path $src)) { Die "源目录不存在: $src" }
    $args = @($src, $dst, '/E', '/NFL', '/NDL', '/NJH', '/NJS', '/NP')
    if ($Purge) { $args += '/PURGE' }
    & robocopy @args | Out-Null
    $code = $LASTEXITCODE
    if ($code -ge 8) { Die "robocopy 失败 ($code): $src -> $dst" }
}

$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
            [Environment]::GetEnvironmentVariable('Path', 'User')

# ── 1. 构建（可选）──────────────────────────────────────────
if ($Build) {
    Step '构建前端 (pnpm build)'
    $desktop = Join-Path $Repo 'filesync-desktop'
    if (-not (Test-Path (Join-Path $desktop 'package.json'))) { Die "找不到前端项目: $desktop" }
    $pnpm = Get-Command pnpm.cmd -ErrorAction SilentlyContinue
    if (-not $pnpm) { $pnpm = Get-Command pnpm -ErrorAction SilentlyContinue }
    if (-not $pnpm) { Die '找不到 pnpm（先安装 Node.js + pnpm）' }
    Push-Location $desktop
    try {
        & $pnpm.Source build
        if ($LASTEXITCODE -ne 0) { Die "pnpm build 失败 ($LASTEXITCODE)" }
    } finally { Pop-Location }
    Ok '前端构建完成'
}

# ── 2. 定位构建产物 ─────────────────────────────────────────
Step '定位构建产物'
if (-not $DistDir) {
    foreach ($cand in @((Join-Path $Repo 'filesync-desktop\dist'), (Join-Path $Repo 'web\dist'))) {
        if (Test-Path (Join-Path $cand 'index.html')) { $DistDir = $cand; break }
    }
}
if (-not $DistDir) { Die '找不到构建产物（可用 -DistDir 指定，或用 -Build 先构建）' }
$DistDir = [IO.Path]::GetFullPath($DistDir)
if (-not (Test-Path (Join-Path $DistDir 'index.html'))) { Die "构建产物不完整（没有 index.html）: $DistDir" }
Ok "构建产物: $DistDir"

# ── 3. 备份现有 web ─────────────────────────────────────────
Step "备份现有 web: $WebRoot"
if (-not (Test-Path $WebRoot)) { New-Item -ItemType Directory -Force -Path $WebRoot | Out-Null }
$backupRoot = 'E:\fileSynchronization\backup'
New-Item -ItemType Directory -Force -Path $backupRoot | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backup = Join-Path $backupRoot ("web-" + $stamp)
if (-not $Force -and (Get-ChildItem $WebRoot -ErrorAction SilentlyContinue | Select-Object -First 1)) {
    Invoke-Robocopy $WebRoot $backup
    Ok "已备份到 $backup"
} else {
    Warn 'web 目录为空或 -Force：跳过备份'
}

# ── 4. 部署 + 校验 ─────────────────────────────────────────
Step '部署 web'
try {
    Invoke-Robocopy $DistDir $WebRoot
    if (-not (Test-Path (Join-Path $WebRoot 'index.html'))) { throw '部署后缺少 index.html' }
    Ok "已发布到 $WebRoot"
} catch {
    Warn "部署失败: $($_.Exception.Message)"
    if (Test-Path $backup) {
        Invoke-Robocopy $backup $WebRoot -Purge
        Ok '已回滚到备份'
    }
    exit 1
}

# ── 5. 清理仍被引用的旧 assets（保留 .well-known/rules）─────
Step '结果'
Write-Host "  web 目录 : $WebRoot"
Write-Host "  入口     : https://ddns.sunyuanling.cn"
Write-Host "  备份     : $(if (Test-Path $backup) { $backup } else { '无' })"
Write-Host ''
Write-Host '部署完成。' -ForegroundColor Green
