<#
.SYNOPSIS
  Windows 一键部署后端（构建 sys_core + Go 二进制、生成配置、起 redis、启动后端）。

.USAGE
  powershell -NoProfile -File new_server\deploy-win.ps1 -Force
  powershell -NoProfile -File new_server\deploy-win.ps1 -SkipBuild -NoSupervisor -Force
  powershell -NoProfile -File new_server\deploy-win.ps1 -ConfigPath D:\config.yaml -Force

.NOTES
  - 默认从生产 VM（ssh vm）读取 config.yaml 作为源，改写成 Windows 本机配置；密钥保留、不打印。
  - 默认启用 supervisor（frpc×3 + ddns-go）；本地首验用 -NoSupervisor，避免和 Linux 生产抢隧道。
  - 数据库不在这里处理（已迁移好）；nginx（宝塔）已反代 localhost:<port>。
#>
[CmdletBinding()]
param(
    [string]$InstallDir = 'E:\fileSynchronization\syc-file-win',
    [int]$Port = 8991,
    [switch]$SkipBuild,
    [switch]$Force,
    [string]$ConfigPath = '',
    [string]$NodeName = 'win-home',
    [switch]$NoSupervisor,
    [switch]$SkipTools,
    [string]$ProdHost = 'vm',
    [string]$FrpcExe = 'E:\内网穿透\frp\frpc.exe',
    [string]$DdnsExe = 'F:\proxy\ddns\ddns-go.exe',
    [string]$CloudflaredExe = 'C:\Program Files (x86)\cloudflared\cloudflared.exe'
)

$ErrorActionPreference = 'Stop'
$Repo = Split-Path $PSScriptRoot
$InstallDir = [IO.Path]::GetFullPath($InstallDir)

function Step([string]$m) { Write-Host "`n==> $m" -ForegroundColor Cyan }
function Info([string]$m) { Write-Host "    $m" -ForegroundColor Gray }
function Ok([string]$m) { Write-Host "  [ok] $m" -ForegroundColor Green }
function Warn([string]$m) { Write-Host "  [!] $m" -ForegroundColor Yellow }
function Die([string]$m) { Write-Host "  [x] $m" -ForegroundColor Red; exit 1 }

$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' +
            [Environment]::GetEnvironmentVariable('Path', 'User') + ';' +
            (Join-Path $env:USERPROFILE '.cargo\bin')

# ── 1. 环境检查 + 目录 ───────────────────────────────────────
Step '环境检查'
$goCmd = Get-Command go -ErrorAction SilentlyContinue
$go = if ($goCmd) { $goCmd.Source } elseif (Test-Path 'C:\Program Files\Go\bin\go.exe') { 'C:\Program Files\Go\bin\go.exe' } else { $null }
if (-not $go) { Die '找不到 go.exe' }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { Die '找不到 gcc（需要 MinGW-w64）' }
if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) { Die '找不到 cargo（Rust 工具链）' }
Ok "go: $go"
if ((Get-Service -Name 'mysql' -ErrorAction SilentlyContinue).Status -eq 'Running') { Ok 'MySQL 运行中' } else { Warn 'MySQL 未运行' }
$nginxSvc = Get-Service -Name 'nginx' -ErrorAction SilentlyContinue
if ($nginxSvc) { Ok "nginx: $($nginxSvc.Status)" } else { Warn '找不到 nginx 服务' }

Step "准备安装目录 $InstallDir"
foreach ($d in @($InstallDir, (Join-Path $InstallDir 'config'), (Join-Path $InstallDir 'deploy\frp'),
                 (Join-Path $InstallDir 'deploy\ddns'), (Join-Path $InstallDir 'log'), (Join-Path $InstallDir 'static\avatar'))) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}

# ── 2. 构建 ─────────────────────────────────────────────────
if (-not $SkipBuild) {
    Step '构建 sys_core'
    $sysCoreBuild = Join-Path $Repo 'sys_core\build.ps1'
    if (-not (Test-Path $sysCoreBuild)) { Die "找不到 $sysCoreBuild" }
    & powershell -NoProfile -ExecutionPolicy Bypass -File $sysCoreBuild --offline
    if ($LASTEXITCODE -ne 0) { Die "sys_core 构建失败 ($LASTEXITCODE)" }
    Ok 'sys_core 构建完成'

    Step '构建后端 (go build)'
    $lib = Join-Path $Repo 'sys_core\lib\libsys_core.a'
    if (-not (Test-Path $lib)) { Die "找不到 $lib" }
    $coreSha = (Get-FileHash $lib -Algorithm SHA256).Hash.ToLowerInvariant()
    $ffmpegZip = Join-Path $PSScriptRoot 'internal\thumb\embedded\windows_amd64\ffmpeg.zip'
    if (-not (Test-Path $ffmpegZip)) { Die "缺少内嵌 ffmpeg 归档: $ffmpegZip" }
    $env:CGO_ENABLED = '1'
    $env:CC = 'gcc'
    Push-Location $PSScriptRoot
    try {
        & $go build -ldflags "-X syc-file/pkg/synccore.CoreArchiveSHA256=$coreSha" -trimpath -o (Join-Path $InstallDir 'syc-file.exe') ./cmd
        if ($LASTEXITCODE -ne 0) { Die "go build 失败 ($LASTEXITCODE)" }
    } finally { Pop-Location }
    Ok 'syc-file.exe 构建完成'
} else {
    Step '跳过构建'
    if (-not (Test-Path (Join-Path $InstallDir 'syc-file.exe'))) { Die '安装目录没有 syc-file.exe，不能 -SkipBuild' }
}

# ── 3. 配置生成 ─────────────────────────────────────────────
Step '生成配置 config\config.yaml'
$sourceLines = $null
if ($ConfigPath) {
    if (-not (Test-Path $ConfigPath)) { Die "找不到 -ConfigPath: $ConfigPath" }
    $sourceLines = @(Get-Content -LiteralPath $ConfigPath -Encoding UTF8)
    Info "配置源: 本地 $ConfigPath"
} else {
    Info "配置源: ssh ${ProdHost}:/mnt/data/project/new_server/config/config.yaml"
    $remote = & ssh -o BatchMode=yes -o ConnectTimeout=8 $ProdHost 'cat /mnt/data/project/new_server/config/config.yaml' 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $remote) { Die '读取生产配置失败（ssh vm 不可用），可用 -ConfigPath 指定本地配置' }
    $sourceLines = @($remote)
}
if ($sourceLines.Count -lt 10) { Die '配置源内容异常（太短）' }

$out = New-Object 'System.Collections.Generic.List[string]'
$section = ''
$inAllowedPaths = $false
$vpnSkip = $false
$serverModeSeen = $false
$supervisorDone = $false
$supervisorLines = @(
    'supervisor:',
    ('  enabled: ' + $(if ($NoSupervisor) { 'false' } else { 'true' })),
    '  log_dir: .\log\supervisor',
    '  processes:',
    '    - name: frpc-ddns',
    '      enabled: true',
    ("      command: '" + $FrpcExe + "'"),
    '      args: ["-c", "deploy/frp/frpc.toml"]',
    '      skip_if_running: true',
    '      restart_delay_seconds: 10',
    '    - name: frpc-tokyo',
    '      enabled: true',
    ("      command: '" + $FrpcExe + "'"),
    '      args: ["-c", "deploy/frp/frpc_akile.toml"]',
    '      skip_if_running: true',
    '      restart_delay_seconds: 10',
    '    - name: frpc-newyork',
    '      enabled: true',
    ("      command: '" + $FrpcExe + "'"),
    '      args: ["-c", "deploy/frp/frpc_ny.toml"]',
    '      skip_if_running: true',
    '      restart_delay_seconds: 10',
    '    - name: ddns-go',
    '      enabled: true',
    ("      command: '" + $DdnsExe + "'"),
    '      args: ["-c", "deploy/ddns/ddns_go_config.yaml"]',
    '      skip_if_running: true',
    '      restart_delay_seconds: 30',
    '    - name: cloudflared',
    '      enabled: true',
    ("      command: '" + $CloudflaredExe + "'"),
    '      args: ["--no-autoupdate", "tunnel", "run", "--token-file", "C:/ProgramData/cloudflared/token"]',
    '      skip_if_running: true',
    '      restart_delay_seconds: 10',
    '    - name: frpc-ali',
    '      enabled: true',
    ("      command: '" + $FrpcExe + "'"),
    '      args: ["-c", "deploy/frp/dsh.toml"]',
    '      skip_if_running: true',
    '      restart_delay_seconds: 10'
)

$i = 0
while ($i -lt $sourceLines.Count) {
    $line = $sourceLines[$i]
    if ($line -match '^([A-Za-z_][A-Za-z0-9_]*):\s*$') {
        $section = $matches[1]
        $inAllowedPaths = $false
        $vpnSkip = $false
        $i++
        if ($section -eq 'vpn') { $vpnSkip = $true; continue }
        if ($section -eq 'supervisor') {
            foreach ($l in $supervisorLines) { $out.Add($l) }
            $supervisorDone = $true
            while ($i -lt $sourceLines.Count -and $sourceLines[$i] -notmatch '^[A-Za-z_][A-Za-z0-9_]*:\s*$') { $i++ }
            continue
        }
        $out.Add($line)
        continue
    }
    if ($vpnSkip) { $i++; continue }

    switch ($section) {
        'db' {
            if ($line -match '^  host:') { $line = '  host: 127.0.0.1' }
            elseif ($line -match '^  port:') { $line = '  port: 3306' }
            elseif ($line -match '^  name:') { $line = '  name: syncfile' }
        }
        'redis' {
            if ($line -match '^  host:') { $line = '  host: 127.0.0.1' }
            elseif ($line -match '^  port:') { $line = '  port: 6379' }
            elseif ($line -match '^  password:') { $line = '  password: ""' }
        }
        'log' {
            if ($line -match '^  path:') { $line = '  path: .\log' }
        }
        'server' {
            if ($line -match '^  port:') { $line = "  port: $Port" }
            elseif ($line -match '^  name:') { $line = "  name: `"$NodeName`"" }
            elseif ($line -match '^  mode:') { $line = '  mode: dev'; $serverModeSeen = $true }
        }
        'file' {
            if ($line -match '^  allowed_paths:') {
                $out.Add('  allowed_paths:')
                foreach ($p in @('D:', 'E:', 'F:', 'G:')) { $out.Add("    - `"$p`"") }
                $inAllowedPaths = $true
                $i++
                continue
            } elseif ($inAllowedPaths -and $line -match '^    - ') {
                $i++
                continue
            } elseif ($inAllowedPaths) {
                $inAllowedPaths = $false
            }
            if ($line -match '^    base_path:') { $line = '    base_path: "file_sync"' }
        }
        'share' {
            if ($line -match '^  temp_path:') { $line = '  temp_path: "F:/file_sync/share_temp/"' }
        }
        'quick_share' {
            if ($line -match '^  base_path:') { $line = '  base_path: "F:/file_sync/quickshare/"' }
        }
        'sync' {
            if ($line -match '^  sync_catalogue:') { $line = '  sync_catalogue: "F:/file_sync/sync/"' }
        }
    }
    $out.Add($line)
    $i++
}
if (-not $supervisorDone) { foreach ($l in $supervisorLines) { $out.Add($l) } }
# 去重：允许块只保留一份（源里/替换逻辑可能产生重复的空 allowed_paths 头）
$dedup = New-Object 'System.Collections.Generic.List[string]'
$seenAllowed = $false
foreach ($l in $out) {
    if ($l -match '^  allowed_paths:\s*$') {
        if ($seenAllowed) { continue }
        $seenAllowed = $true
    }
    $dedup.Add($l)
}
$out = $dedup
if (-not $serverModeSeen) {
    for ($k = 0; $k -lt $out.Count; $k++) {
        if ($out[$k] -match '^server:\s*$') { $out.Insert($k + 1, '  mode: dev'); break }
    }
}
$config = ($out -join "`r`n") + "`r`n"
if ($config -notmatch '(?m)^db:' -or $config -notmatch '(?m)^redis:' -or $config -notmatch '(?m)^auth:') { Die '生成配置缺少 db/redis/auth 段' }

$configPathOut = Join-Path $InstallDir 'config\config.yaml'
if (Test-Path $configPathOut) {
    Copy-Item $configPathOut (Join-Path $InstallDir ('config\config.yaml.bak-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))) -Force
}
[IO.File]::WriteAllText($configPathOut, $config, (New-Object Text.UTF8Encoding($false)))
Ok "已生成 $configPathOut（db/redis -> 127.0.0.1，密钥保留未打印）"

# ── 4. 运行时配置 + 工具 ─────────────────────────────────────
Step '复制 frpc/ddns-go 运行时配置'
$deploySrc = Join-Path $PSScriptRoot 'deploy'
$copied = 0
foreach ($rel in @('frp\frpc.toml', 'frp\frpc_akile.toml', 'frp\frpc_ny.toml', 'frp\dsh.toml', 'ddns\ddns_go_config.yaml')) {
    $src = Join-Path $deploySrc $rel
    if (Test-Path $src) {
        Copy-Item $src (Join-Path $InstallDir ('deploy\' + $rel)) -Force
        $copied++
    } else {
        Warn "缺少运行时配置 $src"
    }
}
Ok "已复制 $copied 个运行时配置"
if (-not $SkipTools) {
    Step '检查 frpc/ddns-go'
    if (-not (Test-Path $FrpcExe)) { Die "找不到 frpc.exe: $FrpcExe（可用 -FrpcExe 指定）" }
    if (-not (Test-Path $DdnsExe)) { Die "找不到 ddns-go.exe: $DdnsExe（可用 -DdnsExe 指定）" }
    Ok 'frpc.exe / ddns-go.exe 已就绪'
}

# ── 5. redis ────────────────────────────────────────────────
Step '确保 redis 运行'
$redisUp = $false
try { Start-Service redis -ErrorAction Stop; $redisUp = $true } catch { }
if (-not $redisUp -and (Get-NetTCPConnection -LocalPort 6379 -State Listen -ErrorAction SilentlyContinue)) { $redisUp = $true }
if (-not $redisUp) {
    $redisExe = 'E:\BtSoft\redis\redis-server.exe'
    if (Test-Path $redisExe) {
        Start-Process -FilePath $redisExe -ArgumentList 'redis.windows.conf' -WorkingDirectory 'E:\BtSoft\redis' -WindowStyle Hidden | Out-Null
        for ($n = 0; $n -lt 20; $n++) {
            Start-Sleep -Milliseconds 250
            if (Get-NetTCPConnection -LocalPort 6379 -State Listen -ErrorAction SilentlyContinue) { $redisUp = $true; break }
        }
    }
}
if ($redisUp) { Ok 'redis 监听 6379' } else { Die 'redis 未启动（管理员: Start-Service redis，或手动跑 E:\BtSoft\redis\redis-server.exe）' }

# ── 6. 端口冲突 ─────────────────────────────────────────────
Step "检查端口 $Port"
$listener = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($listener) {
    $proc = Get-Process -Id $listener.OwningProcess -ErrorAction SilentlyContinue
    $name = if ($proc) { $proc.ProcessName } else { 'unknown' }
    if ($proc -and $name -match '^syc-file') {
        if ($Force) {
            Stop-Process -Id $proc.Id -Force
            Start-Sleep -Seconds 1
            Ok "已停止旧后端 $name (pid $($proc.Id))"
        } else {
            Die "端口 $Port 被旧后端 $name (pid $($proc.Id)) 占用；加 -Force 自动停止"
        }
    } else {
        Die "端口 $Port 被 $name (pid $($proc.Id)) 占用，请先处理"
    }
} else {
    Ok "端口 $Port 空闲"
}

# ── 7. 启动 + 健康检查 ─────────────────────────────────────
Step '启动后端'
$exe = Join-Path $InstallDir 'syc-file.exe'
$proc = Start-Process -FilePath $exe -WorkingDirectory $InstallDir -WindowStyle Hidden -PassThru
$pingUrl = "http://127.0.0.1:$Port/v1/ping"
$healthy = $false
for ($n = 0; $n -lt 60; $n++) {
    Start-Sleep -Seconds 1
    if ($proc.HasExited) { break }
    try {
        $r = Invoke-RestMethod -Uri $pingUrl -TimeoutSec 2
        if ($r.code -eq 200) { $healthy = $true; break }
    } catch { }
}
if (-not $healthy) {
    Write-Host "`n启动失败，最近日志：" -ForegroundColor Red
    $logFile = Join-Path $InstallDir 'log\server.log'
    if (Test-Path $logFile) { Get-Content $logFile -Tail 40 }
    if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
    exit 1
}
Ok "后端已启动 pid=$($proc.Id)，$pingUrl 正常"
Write-Host ''
Write-Host '部署完成。' -ForegroundColor Green
Write-Host "  安装目录 : $InstallDir"
Write-Host "  端口     : $Port（nginx 已反代 localhost:$Port）"
Write-Host "  日志     : $InstallDir\log\server.log"
if ($NoSupervisor) { Write-Host '  supervisor: 未启用（-NoSupervisor）' -ForegroundColor Yellow }
Write-Host ''
Write-Host '下一步：'
Write-Host '  web      : powershell -NoProfile -File deploy.ps1 -Target web -Build'
Write-Host '  Android  : powershell -NoProfile -File deploy.ps1 -Target android'
Write-Host '  Harmony  : powershell -NoProfile -File deploy.ps1 -Target harmony'
