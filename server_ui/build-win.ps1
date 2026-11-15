#Requires -Version 5.1
<#
.SYNOPSIS
  构建「私有云服务器设置」桌面控制台（Windows / MinGW + Qt6）。

.DESCRIPTION
  自动定位 Qt6 MinGW kit、MinGW 工具链与 Ninja，用 CMake + Ninja 构建 Release 版。
  -Package 额外用 windeployqt 把 Qt 运行库打进 dist/，得到一个可以直接拷走运行的目录。

.PARAMETER QtDir
  Qt kit 目录（如 C:\Qt\6.9.3\mingw_64）。省略时在 C:\Qt、%USERPROFILE%\Qt 下找最高版本。

.PARAMETER BuildDir
  构建目录，默认 <本脚本目录>\build。

.PARAMETER DistDir
  -Package 的输出目录，默认 <本脚本目录>\dist。

.PARAMETER Clean
  构建前删除构建目录（拒绝删除项目目录之外的路径）。

.PARAMETER Package
  构建后运行 windeployqt，把 Qt DLL/插件打进 DistDir。

.PARAMETER Installer
  在 Package 的基础上编译单个 MSI 安装包到 SetupDir（默认 release\）。
  安装向导可选安装路径；由 Windows Installer（msiexec）释放 exe + Qt 依赖、
  创建开始菜单快捷方式与登录自启项（不受安全软件对未签名 exe 的注册表拦截影响）。

.PARAMETER SetupDir
  安装包输出目录，默认 <本脚本目录>\release。

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File server_ui\build-win.ps1 -Clean -Installer
#>
[CmdletBinding()]
param(
    [string]$QtDir = '',
    [string]$BuildDir = '',
    [string]$DistDir = '',
    [string]$SetupDir = '',
    [switch]$Clean,
    [switch]$Package,
    [switch]$Installer
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
if ($Installer) { $Package = $true }

function Write-Step([string]$text) {
    Write-Host ''
    Write-Host "==> $text" -ForegroundColor Cyan
}

function Fail([string]$text) {
    Write-Host "错误: $text" -ForegroundColor Red
    exit 1
}

function Resolve-FullPath([string]$path, [string]$base) {
    if ([System.IO.Path]::IsPathRooted($path)) {
        return [System.IO.Path]::GetFullPath($path)
    }
    return [System.IO.Path]::GetFullPath((Join-Path $base $path))
}

function Find-QtKit([string]$explicit) {
    if ($explicit) {
        $full = Resolve-FullPath $explicit $root
        if (-not (Test-Path (Join-Path $full 'lib\cmake\Qt6\Qt6Config.cmake'))) {
            Fail "不是有效的 Qt6 kit（缺少 lib\cmake\Qt6\Qt6Config.cmake）: $full"
        }
        return $full
    }

    $found = @()
    foreach ($base in @('C:\Qt', (Join-Path $env:USERPROFILE 'Qt'))) {
        if (-not (Test-Path $base)) { continue }
        $versions = Get-ChildItem $base -Directory -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -match '^\d+(\.\d+)+$' } |
            Sort-Object { [version]$_.Name } -Descending
        foreach ($ver in $versions) {
            $kit = Join-Path $ver.FullName 'mingw_64'
            if (Test-Path (Join-Path $kit 'lib\cmake\Qt6\Qt6Config.cmake')) {
                $found += $kit
            }
        }
    }
    if ($found.Count -eq 0) {
        Fail '找不到 Qt6 MinGW kit。请用 -QtDir 指定，例如 C:\Qt\6.9.3\mingw_64'
    }
    return $found[0]
}

function Find-MingwBin([string]$qtDir) {
    $candidates = @()
    $qtRoot = Split-Path -Parent (Split-Path -Parent $qtDir)  # C:\Qt\6.9.3\mingw_64 -> C:\Qt
    $toolsDir = Join-Path $qtRoot 'Tools'
    if (Test-Path $toolsDir) {
        $candidates += Get-ChildItem $toolsDir -Directory -Filter 'mingw*' -ErrorAction SilentlyContinue |
            Sort-Object Name -Descending | ForEach-Object { Join-Path $_.FullName 'bin' }
    }
    $candidates += Get-ChildItem 'C:\Qt\Tools' -Directory -Filter 'mingw*' -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | ForEach-Object { Join-Path $_.FullName 'bin' }

    foreach ($bin in $candidates) {
        if (Test-Path (Join-Path $bin 'g++.exe')) { return $bin }
    }
    $gpp = Get-Command g++.exe -ErrorAction SilentlyContinue
    if ($gpp) { return (Split-Path -Parent $gpp.Source) }
    Fail '找不到 MinGW g++。请用 Qt Maintenance Tool 安装 MinGW，或把 g++ 放进 PATH'
}

function Find-Ninja {
    $candidates = @('C:\Qt\Tools\Ninja\ninja.exe')
    foreach ($path in $candidates) {
        if (Test-Path $path) { return $path }
    }
    $ninja = Get-Command ninja.exe -ErrorAction SilentlyContinue
    if ($ninja) { return $ninja.Source }
    Fail '找不到 ninja（Qt 安装器里的 Tools\Ninja 或 PATH）'
}

$qt = Find-QtKit $QtDir
$mingwBin = Find-MingwBin $qt
$ninja = Find-Ninja
$cmakeCmd = (Get-Command cmake.exe -ErrorAction SilentlyContinue)
if (-not $cmakeCmd) { Fail '找不到 cmake，请先安装 CMake 3.21+ 并加入 PATH' }

if (-not $BuildDir) { $BuildDir = Join-Path $root 'build' }
$buildFull = Resolve-FullPath $BuildDir $root

if ($Clean -and (Test-Path $buildFull)) {
    $rootFull = [System.IO.Path]::GetFullPath($root)
    if (-not $buildFull.StartsWith($rootFull + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
        Fail "拒绝清理项目目录之外的路径: $buildFull"
    }
    Write-Step "清理构建目录 $buildFull"
    Remove-Item -Recurse -Force $buildFull
}

# 让构建期/运行期都能找到 Qt 与 MinGW 的 DLL
$env:Path = "$(Join-Path $qt 'bin');$mingwBin;$(Split-Path -Parent $ninja);$env:Path"

Write-Step "Qt      : $qt"
Write-Host "    MinGW   : $mingwBin"
Write-Host "    Ninja   : $ninja"
Write-Host "    CMake   : $($cmakeCmd.Source)"
Write-Host "    Build   : $buildFull"

$cmakeArgs = @(
    '-S', $root,
    '-B', $buildFull,
    '-G', 'Ninja',
    '-DCMAKE_BUILD_TYPE=Release',
    "-DCMAKE_PREFIX_PATH=$qt",
    "-DCMAKE_CXX_COMPILER=$(Join-Path $mingwBin 'g++.exe')",
    "-DCMAKE_C_COMPILER=$(Join-Path $mingwBin 'gcc.exe')",
    "-DCMAKE_MAKE_PROGRAM=$ninja"
)

Write-Step 'CMake 配置'
& $cmakeCmd.Source @cmakeArgs
if ($LASTEXITCODE -ne 0) { Fail "CMake 配置失败（exit=$LASTEXITCODE）" }

Write-Step '编译'
& $cmakeCmd.Source --build $buildFull --parallel
if ($LASTEXITCODE -ne 0) { Fail "编译失败（exit=$LASTEXITCODE）" }

$exe = Join-Path $buildFull 'syc-file-gui.exe'
if (-not (Test-Path $exe)) { Fail "编译完成但找不到产物: $exe" }

$distFull = ''
$setupPath = ''
if ($Package) {
    if (-not $DistDir) { $DistDir = Join-Path $root 'dist' }
    $distFull = Resolve-FullPath $DistDir $root
    if ($distFull.Length -lt 4 -or $distFull -eq [System.IO.Path]::GetPathRoot($distFull)) {
        Fail "拒绝使用这个输出目录: $distFull"
    }
    if (Test-Path $distFull) { Remove-Item -Recurse -Force $distFull }
    New-Item -ItemType Directory -Force -Path $distFull | Out-Null
    Copy-Item $exe $distFull

    $windeployqt = Join-Path $qt 'bin\windeployqt.exe'
    if (-not (Test-Path $windeployqt)) { Fail "找不到 windeployqt: $windeployqt" }
    Write-Step "打包 Qt 运行库到 $distFull"
    & $windeployqt --release --no-translations --compiler-runtime --dir $distFull (Join-Path $distFull 'syc-file-gui.exe')
    if ($LASTEXITCODE -ne 0) { Fail "windeployqt 失败（exit=$LASTEXITCODE）" }
    Remove-Item (Join-Path $distFull 'opengl32sw.dll'), (Join-Path $distFull 'D3Dcompiler_47.dll') -Force -ErrorAction SilentlyContinue
}

if ($Installer) {
    $version = '0.1.0'
    $cmakeText = Get-Content (Join-Path $root 'CMakeLists.txt') -Raw
    if ($cmakeText -match 'project\(\s*server_ui\s+VERSION\s+([0-9]+\.[0-9]+\.[0-9]+)') {
        $version = $Matches[1]
    }

    if (-not $SetupDir) { $SetupDir = Join-Path $root 'release' }
    $setupFull = Resolve-FullPath $SetupDir $root
    New-Item -ItemType Directory -Force -Path $setupFull | Out-Null
    $setupPath = Join-Path $setupFull ("私有云服务器设置-$version.msi")

    Write-Step '生成单文件 MSI 安装包（Windows Installer）'
    $msiScript = Join-Path $root 'installer\make-msi.ps1'
    $msiArgs = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $msiScript,
        '-SourceDir', $distFull,
        '-Version', $version,
        '-OutFile', $setupPath,
        '-IconFile', (Join-Path $root 'resources\appicon.ico')
    )
    & powershell.exe @msiArgs
    if ($LASTEXITCODE -ne 0) { Fail "MSI 生成失败（exit=$LASTEXITCODE）" }
    if (-not (Test-Path $setupPath)) { Fail "安装包没有生成: $setupPath" }
}

Write-Step '完成'
Write-Host "  可执行文件: $exe"
if ($distFull) {
    Write-Host "  Qt 运行目录: $distFull（windeployqt 已就位）"
}
if ($setupPath) {
    $size = [math]::Round((Get-Item $setupPath).Length / 1MB, 1)
    Write-Host "  安装包    : $setupPath（$size MB，单文件 MSI）"
    Write-Host '              双击安装：可选安装路径，msiexec 释放 Qt 依赖并建快捷方式/自启'
}
Write-Host '  运行      : syc-file-gui.exe --config <config.yaml>'
Write-Host '  默认配置  : <程序目录>\config\config.yaml'