<#
.SYNOPSIS
  云梯 HarmonyOS 统一构建入口：先把 sys_core 打包进 prebuilts，再（可选）用 DevEco hvigor 构建 HAP。

.DESCRIPTION
  步骤 1（总是执行）：调用 products/sunyuanling/build.ps1，按 3 个 OHOS target
  （aarch64/armv7/x86_64-unknown-linux-ohos）编译 sys_core release 静态库，拷贝到
  products/sunyuanling/src/main/cpp/prebuilts/<abi>/libfilecore.a，校验产物存在并打印 size + SHA256。

  步骤 2（传 -Hap）：定位 DevEco Studio 的 hvigor CLI（env DEVECO_HOME，缺省
  E:\Program Files\DevEco Studio），在 harmony/ 根执行：
    hvigorw --mode module -p product=default -p module=sunyuanling@default -p buildMode=debug assembleHap --no-daemon

  已知项目问题（实跑确认）：HAP 编译/打包（PackageHap）全部通过，只有 SignHap 失败：
    00303074 Configuration Error: The bundleName in app.json5/hvigorfile.ts does not match
    the bundleName in the generated SigningConfigs
  （app.json5 bundleName = com.example.fileSyc，harmony/build-profile.json5 里的签名 profile 不匹配）
  因此默认 -Hap 先按签名流程构建；确认仅 SignHap 失败时打印明确提示，并自动降级一次
  unsigned 构建（hvigor -p properties.enableSignTask=false），产出 sunyuanling-default-unsigned.hap，
  但退出码仍为 2（不掩盖签名失败事实）。显式 -Unsigned 直接跳过签名、退出码 0。
  unsigned HAP 不能直接装机；需先在 DevEco IDE 修复签名配置再出可安装包。

.PARAMETER CoreOnly
  只打包 sys_core（不传 -Hap 时的默认行为）。与 -Hap/-Unsigned 互斥。
.PARAMETER Hap
  打包 sys_core 后继续用 hvigor 构建 HAP。
.PARAMETER Unsigned
  跳过签名任务（隐含 -Hap），用于签名配置未就绪时验证 HAP 编译打包。
.PARAMETER Offline
  cargo 构建加 --offline（依赖已在本地 cargo 缓存时可用）。

.EXAMPLE
  .\build.ps1                     # 默认：只打包 sys_core（3 ABI → prebuilts）
  .\build.ps1 -Hap -Unsigned      # 打包 sys_core + unsigned HAP（本机当前可成功）
  .\build.ps1 -Hap                # 签名 HAP（预计签名失败→自动降级 unsigned，退出码 2）
  .\build.ps1 -Offline            # 复用本地 cargo 缓存离线打包 sys_core
#>
[CmdletBinding()]
param(
    [switch]$CoreOnly,
    [switch]$Hap,
    [switch]$Unsigned,
    [switch]$Offline
)

$ErrorActionPreference = 'Stop'
$scriptRoot = $PSScriptRoot
$productDir = Join-Path $scriptRoot 'products\sunyuanling'
$productBuild = Join-Path $productDir 'build.ps1'
$prebuiltsDir = Join-Path $productDir 'src\main\cpp\prebuilts'
$abis = @('arm64-v8a', 'armeabi-v7a', 'x86_64')
$devecoDefault = 'E:\Program Files\DevEco Studio'
$moduleName = 'sunyuanling'
$productName = 'default'

if ($CoreOnly -and ($Hap -or $Unsigned)) {
    Write-Host '!! -CoreOnly 与 -Hap/-Unsigned 互斥：请二选一。' -ForegroundColor Red
    exit 1
}
if ($Unsigned -and -not $Hap) { $Hap = $true }   # -Unsigned 隐含 -Hap

function Write-Step([string]$Text) {
    Write-Host ''
    Write-Host "==> $Text" -ForegroundColor Cyan
}

function Show-HapArtifact {
    param($Hap, [string]$Kind)
    if (-not $Hap) { Write-Host '    （未在输出目录找到 .hap 文件）' -ForegroundColor Yellow; return }
    $hash = (Get-FileHash -Path $Hap.FullName -Algorithm SHA256).Hash
    Write-Host ("    [{0}] {1}" -f $Kind, $Hap.FullName) -ForegroundColor Green
    Write-Host ("           {0:N0} bytes  SHA256 {1}  mtime {2:yyyy-MM-dd HH:mm:ss}" -f $Hap.Length, $hash, $Hap.LastWriteTime)
}

# ---------------------------------------------------------------- step 1: sys_core

function Invoke-CoreBuild {
    Write-Step '步骤 1  打包 sys_core（3 个 OHOS target → products/sunyuanling/src/main/cpp/prebuilts）'
    if (-not (Test-Path $productBuild)) { throw "找不到子构建脚本：$productBuild" }
    $coreArgs = @{}
    if ($Offline) { $coreArgs['Offline'] = $true }
    $sw = [Diagnostics.Stopwatch]::StartNew()
    & $productBuild @coreArgs
    $sw.Stop()
    Write-Host ("==> sys_core 打包完成，用时 {0:N1}s" -f $sw.Elapsed.TotalSeconds) -ForegroundColor Green

    $results = @()
    foreach ($abi in $abis) {
        $lib = Join-Path $prebuiltsDir (Join-Path $abi 'libfilecore.a')
        if (-not (Test-Path $lib)) { throw "缺少 prebuilts 产物：$lib" }
        $item = Get-Item $lib
        if ($item.Length -le 0) { throw "prebuilts 产物为空：$lib" }
        $results += [pscustomobject]@{ Abi = $abi; Path = $lib; Bytes = $item.Length
                                       Sha256 = (Get-FileHash -Path $lib -Algorithm SHA256).Hash; MTime = $item.LastWriteTime }
    }
    Write-Host ''
    Write-Host '==> prebuilts 校验（size + SHA256）：' -ForegroundColor Green
    foreach ($r in $results) {
        Write-Host ("    {0,-12} {1:N0} bytes  SHA256 {2}" -f $r.Abi, $r.Bytes, $r.Sha256)
        Write-Host ("                 {0}" -f $r.Path) -ForegroundColor DarkGray
    }
    return $results
}

# ------------------------------------------------------------------- step 2: HAP

function Resolve-DevEco {
    $candidates = @()
    if ($env:DEVECO_HOME) { $candidates += $env:DEVECO_HOME }
    $candidates += $devecoDefault
    foreach ($c in $candidates) {
        if (-not $c) { continue }
        if (Test-Path (Join-Path $c 'tools\hvigor\bin\hvigorw.bat')) { return (Resolve-Path $c).Path }
    }
    throw ("找不到 DevEco Studio（需要 tools\hvigor\bin\hvigorw.bat）。请设置环境变量 DEVECO_HOME 指向安装目录，例如：`n" +
           "  `$env:DEVECO_HOME='$devecoDefault'")
}

function Set-HvigorEnvironment {
    param([string]$DevEco)
    $nodeHome = Join-Path $DevEco 'tools\node'
    $nodeExe = Join-Path $nodeHome 'node.exe'
    if (-not (Test-Path $nodeExe)) { throw "找不到 DevEco 自带 node：$nodeExe（hvigorw 需要它）" }
    $sdkHome = Join-Path $DevEco 'sdk'
    if (-not (Test-Path $sdkHome)) { throw "找不到 DevEco SDK 目录：$sdkHome" }
    $env:NODE_HOME = $nodeHome
    if (-not $env:DEVECO_SDK_HOME -or -not (Test-Path $env:DEVECO_SDK_HOME)) { $env:DEVECO_SDK_HOME = $sdkHome }
    $pathParts = @($nodeHome, (Join-Path $DevEco 'tools\hvigor\bin')) + ($env:Path -split ';')
    $env:Path = (@($pathParts | Where-Object { $_ }) | Select-Object -Unique) -join ';'
    Write-Host "==> DevEco Studio : $DevEco"
    Write-Host "    NODE_HOME     : $env:NODE_HOME"
    Write-Host "    DEVECO_SDK_HOME: $env:DEVECO_SDK_HOME"
}

function Invoke-Hvigor {
    param([string]$Hvigorw, [bool]$SkipSign)
    $hvigorArgs = @('--mode', 'module', '-p', "product=$productName",
                    '-p', "module=$moduleName@default", '-p', 'buildMode=debug')
    if ($SkipSign) { $hvigorArgs += @('-p', 'properties.enableSignTask=false') }
    $hvigorArgs += @('assembleHap', '--no-daemon')
    Write-Host ''
    Write-Host "==> 工作目录: $scriptRoot"
    Write-Host ("==> 命令: hvigorw {0}" -f ($hvigorArgs -join ' '))
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $origEap = $ErrorActionPreference
    # hvigor 把 ERROR 写到 stderr；Windows PowerShell 5.1 下 EAP=Stop 会把它当终止错误，先放宽。
    $ErrorActionPreference = 'Continue'
    Push-Location $scriptRoot
    try {
        $raw = & $Hvigorw @hvigorArgs 2>&1 | ForEach-Object { "$_" }
        $code = $LASTEXITCODE
    } finally {
        Pop-Location
        $ErrorActionPreference = $origEap
        $sw.Stop()
    }
    # 去掉 ANSI 颜色码与 5.1 把空 stderr 段渲染出的类型名噪声行
    $clean = @($raw | ForEach-Object { $_ -replace ("$([char]27)\[[0-9;]*m"), '' } |
               Where-Object { $_ -ne 'System.Management.Automation.RemoteException' })
    $clean | ForEach-Object { Write-Host $_ }
    $color = if ($code -eq 0) { 'Green' } else { 'Red' }
    Write-Host ("==> hvigor 退出码 {0}，用时 {1:N1}s" -f $code, $sw.Elapsed.TotalSeconds) -ForegroundColor $color
    return [pscustomobject]@{ ExitCode = $code; Text = ($clean -join "`n"); Seconds = $sw.Elapsed.TotalSeconds }
}

function Test-SignOnlyFailure {
    param([string]$Text)
    $failedTasks = @([regex]::Matches($Text, 'Failed :([^.\s]+)') | ForEach-Object { $_.Groups[1].Value })
    if ($failedTasks.Count -eq 0) { return $false }
    $nonSign = @($failedTasks | Where-Object { $_ -notmatch 'SignHap' })
    return ($nonSign.Count -eq 0 -and $Text -match 'SignHap')
}

function Write-FailureExcerpt {
    param($Result)
    Write-Host ''
    Write-Host ("    hvigor 失败摘要（退出码 {0}）：" -f $Result.ExitCode) -ForegroundColor DarkYellow
    $excerpt = @($Result.Text -split "`n" | Where-Object { $_ -match 'ERROR|00303074|bundleName|SigningConfigs|BUILD FAILED|Failed :' } | Select-Object -First 25)
    if ($excerpt.Count -eq 0) { $excerpt = @($Result.Text -split "`n" | Select-Object -Last 15) }
    foreach ($line in $excerpt) { Write-Host ("      {0}" -f $line.TrimEnd()) -ForegroundColor DarkYellow }
    $logPath = Join-Path $env:TEMP ("hvigor-fail-" + (Get-Date -Format 'yyyyMMdd-HHmmss') + ".log")
    Set-Content -Path $logPath -Value $Result.Text -Encoding UTF8
    Write-Host ("    完整日志：{0}" -f $logPath)
}

function Find-LatestHap {
    param([switch]$UnsignedPreferred)
    $hapRoot = Join-Path $productDir 'build'
    if (-not (Test-Path $hapRoot)) { return $null }
    $haps = @(Get-ChildItem -Path $hapRoot -Recurse -File -Filter '*.hap' -ErrorAction SilentlyContinue)
    if ($haps.Count -eq 0) { return $null }
    if ($UnsignedPreferred) {
        $preferred = @($haps | Where-Object { $_.Name -match 'unsigned' })
    } else {
        $preferred = @($haps | Where-Object { $_.Name -match 'signed' -and $_.Name -notmatch 'unsigned' })
    }
    if ($preferred.Count -eq 0) { $preferred = $haps }
    return ($preferred | Sort-Object LastWriteTime -Descending | Select-Object -First 1)
}

function Invoke-HapBuild {
    Write-Step '步骤 2  用 DevEco hvigor 构建 HAP（assembleHap）'
    $deveco = Resolve-DevEco
    Set-HvigorEnvironment -DevEco $deveco
    $hvigorw = Join-Path $deveco 'tools\hvigor\bin\hvigorw.bat'

    if ($Unsigned) {
        $r = Invoke-Hvigor -Hvigorw $hvigorw -SkipSign $true
        if ($r.ExitCode -ne 0) {
            Write-FailureExcerpt -Result $r
            Write-Host ''
            Write-Host '!! unsigned HAP 构建失败（不是签名问题，请按上面的编译/打包错误排查）。' -ForegroundColor Red
            return 1
        }
        $hap = Find-LatestHap -UnsignedPreferred
        Write-Host ''
        Write-Host '==> HAP 构建成功（unsigned：跳过签名任务）' -ForegroundColor Green
        Show-HapArtifact -Hap $hap -Kind 'unsigned'
        Write-Host '    注意：unsigned HAP 不能直接安装/上真机；装机前需要在 DevEco IDE 里修复签名配置。' -ForegroundColor Yellow
        Write-Host '    签名修复：DevEco Studio → File → Project Structure → Signing Configs → 自动签名/Fix（bundleName 需与 app.json5 的 com.example.fileSyc 匹配）。'
        return 0
    }

    $r = Invoke-Hvigor -Hvigorw $hvigorw -SkipSign $false
    if ($r.ExitCode -eq 0) {
        $hap = Find-LatestHap
        Write-Host ''
        Write-Host '==> HAP 构建成功（已签名）' -ForegroundColor Green
        Show-HapArtifact -Hap $hap -Kind 'signed'
        return 0
    }

    if (-not (Test-SignOnlyFailure -Text $r.Text)) {
        Write-FailureExcerpt -Result $r
        Write-Host ''
        Write-Host '!! HAP 构建失败（非签名阶段），请按上面的错误日志排查。' -ForegroundColor Red
        return 1
    }

    # 只有 SignHap 失败 = 项目签名配置与 bundleName 不匹配，编译打包本身是好的。
    Write-Host ''
    Write-Host '!! 核心打包成功，HAP 编译通过但签名失败（项目签名配置问题）' -ForegroundColor Yellow
    Write-Host '   hvigor 报错 00303074：app.json5/hvigorfile.ts 的 bundleName 与生成的 SigningConfigs 不匹配。'
    Write-Host '   本项目 app.json5 bundleName = com.example.fileSyc，而 harmony/build-profile.json5 的签名 profile 与之不符。'
    Write-Host '   可行降级：'
    Write-Host '     a) .\build.ps1 -Hap -Unsigned  跳过签名构建 unsigned HAP（本脚本会自动重试，见下）'
    Write-Host '     b) .\build.ps1 -CoreOnly       只打包 sys_core（本次已完成）'
    Write-Host '     c) DevEco Studio → Project Structure → Signing Configs → 自动签名/Fix 后再 Run'
    Write-Host ''
    Write-Host '==> 自动降级：跳过签名重新构建 unsigned HAP …' -ForegroundColor Cyan
    $r2 = Invoke-Hvigor -Hvigorw $hvigorw -SkipSign $true
    if ($r2.ExitCode -ne 0) {
        Write-FailureExcerpt -Result $r2
        Write-Host ''
        Write-Host '!! unsigned 降级构建也失败。' -ForegroundColor Red
        return 1
    }
    $hap = Find-LatestHap -UnsignedPreferred
    Write-Host ''
    Write-Host '==> unsigned HAP 已生成（HAP 编译/打包通过；签名失败的事实未被掩盖）' -ForegroundColor Green
    Show-HapArtifact -Hap $hap -Kind 'unsigned'
    Write-Host '    退出码 2 = 签名未通过；需要退出码 0 时请显式使用 .\build.ps1 -Hap -Unsigned。' -ForegroundColor Yellow
    return 2
}

# ------------------------------------------------------------------------ main

$totalSw = [Diagnostics.Stopwatch]::StartNew()
Write-Host '=== 云梯 HarmonyOS 构建（harmony/build.ps1）===' -ForegroundColor Cyan
$modeText = if ($Hap) { if ($Unsigned) { 'sys_core 打包 + unsigned HAP' } else { 'sys_core 打包 + 签名 HAP（失败自动降级 unsigned）' } }
            else { '仅 sys_core 打包（默认）' }
Write-Host "    模式: $modeText   cargo: $(if ($Offline) { '--offline' } else { 'online' })"

$exitCode = 0
try {
    $null = Invoke-CoreBuild
    if ($Hap) {
        $exitCode = Invoke-HapBuild
    } else {
        Write-Host ''
        Write-Host '==> 默认模式：仅打包 sys_core（等价 -CoreOnly），未构建 HAP。' -ForegroundColor Green
        Write-Host '    如需 HAP：.\build.ps1 -Hap              （签名流程，预计签名失败后自动降级）'
        Write-Host '               .\build.ps1 -Hap -Unsigned   （跳过签名）'
    }
} catch {
    Write-Host ''
    Write-Host ("!! 构建失败：{0}" -f $_.Exception.Message) -ForegroundColor Red
    $exitCode = 1
}

$totalSw.Stop()
$finalColor = if ($exitCode -eq 0) { 'Green' } elseif ($exitCode -eq 2) { 'Yellow' } else { 'Red' }
Write-Host ''
Write-Host ("=== 完成：退出码 {0}，总耗时 {1:N1}s ===" -f $exitCode, $totalSw.Elapsed.TotalSeconds) -ForegroundColor $finalColor
exit $exitCode
