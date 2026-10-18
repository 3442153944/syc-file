param(
    [ValidateSet('windows', 'linux', 'all')]
    [string]$Target = 'windows',
    [string]$Source = $env:FFMPEG_SOURCE,
    [switch]$UpdateLock
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$lock = Join-Path $root 'internal/thumb/embedded/FFMPEG.lock'
if (-not (Test-Path -LiteralPath $lock)) {
    throw "缺少版本锁: $lock"
}

$lockData = @{}
foreach ($raw in ([IO.File]::ReadAllText($lock) -split "`r?`n")) {
    $line = $raw.Trim()
    if ($line -eq '' -or $line.StartsWith('#')) { continue }
    $i = $line.IndexOf('=')
    if ($i -gt 0) { $lockData[$line.Substring(0, $i)] = $line.Substring($i + 1) }
}
$version = $lockData['version']
if (-not $version) { throw "版本锁缺少 version 字段" }

$assets = @{
    windows = @{
        Dest    = 'internal/thumb/embedded/windows_amd64/ffmpeg.zip'
        HashKey = 'windows_amd64_sha256'
    }
    linux   = @{
        Dest    = 'internal/thumb/embedded/linux_amd64/ffmpeg.tar.xz'
        HashKey = 'linux_amd64_sha256'
    }
}

function Get-Sha256([string]$Path) {
    (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Set-LockSha([string]$Key, [string]$Value) {
    $lines = Get-Content -LiteralPath $lock
    $updated = $lines | ForEach-Object {
        if ($_ -match "^$([regex]::Escape($Key))=") { "$Key=$Value" } else { $_ }
    }
    [IO.File]::WriteAllText($lock, (($updated -join "`n") + "`n"), (New-Object System.Text.UTF8Encoding($false)))
}

function Test-Target([string]$Key) {
    $asset = $assets[$Key]
    $dest = Join-Path $root $asset.Dest
    $expected = $lockData[$asset.HashKey]
    if (-not $expected) { throw "版本锁缺少 $($asset.HashKey)" }
    if (-not (Test-Path -LiteralPath $dest)) {
        throw "缺少内嵌 ffmpeg 归档: $dest`n从仓库恢复: git checkout -- $($asset.Dest)"
    }
    $actual = Get-Sha256 $dest
    if ($actual -ne $expected) {
        throw "SHA256 不匹配: $dest`n  期望: $expected`n  实际: $actual`n从仓库恢复: git checkout -- $($asset.Dest)"
    }
    Write-Host "==> ffmpeg $Key 已固定: $version (sha256 $($actual.Substring(0, 12))...)"
}

function Find-First {
    param([string[]]$Paths)
    foreach ($item in $Paths) {
        if (Test-Path -LiteralPath $item) {
            return (Get-Item -LiteralPath $item)
        }
    }
    return $null
}

function New-WindowsZip {
    param([string]$SourceDir, [string]$Dest)
    $ffmpeg = Find-First @((Join-Path $SourceDir 'ffmpeg.exe'), (Join-Path $SourceDir 'bin\ffmpeg.exe'))
    $ffprobe = Find-First @((Join-Path $SourceDir 'ffprobe.exe'), (Join-Path $SourceDir 'bin\ffprobe.exe'))
    if (-not $ffmpeg -or -not $ffprobe) {
        throw "ffmpeg.exe / ffprobe.exe not found in source: $SourceDir"
    }

    $binDir = Split-Path -Parent $ffmpeg.FullName
    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ("syc-ffmpeg-" + [guid]::NewGuid().ToString('N'))
    $stageBin = Join-Path $stage 'bin'
    New-Item -ItemType Directory -Force -Path $stageBin | Out-Null
    try {
        Copy-Item -LiteralPath $ffmpeg.FullName -Destination $stageBin -Force
        Copy-Item -LiteralPath $ffprobe.FullName -Destination $stageBin -Force
        $seen = @{}
        foreach ($dir in @($binDir, $SourceDir)) {
            if (-not (Test-Path -LiteralPath $dir)) { continue }
            Get-ChildItem -LiteralPath $dir -Filter '*.dll' -File | ForEach-Object {
                if (-not $seen.ContainsKey($_.Name)) {
                    Copy-Item -LiteralPath $_.FullName -Destination $stageBin -Force
                    $seen[$_.Name] = $true
                }
            }
        }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $part = "$Dest.part"
        [System.IO.Compression.ZipFile]::CreateFromDirectory(
            $stage, $part, [System.IO.Compression.CompressionLevel]::Optimal, $false
        )
        Move-Item -LiteralPath $part -Destination $Dest -Force
    }
    finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function New-LinuxArchive {
    param([string]$SourceDir, [string]$Dest)
    $ffmpeg = Find-First @((Join-Path $SourceDir 'bin\ffmpeg'), (Join-Path $SourceDir 'ffmpeg'))
    $ffprobe = Find-First @((Join-Path $SourceDir 'bin\ffprobe'), (Join-Path $SourceDir 'ffprobe'))
    if (-not $ffmpeg -or -not $ffprobe) {
        throw "ffmpeg / ffprobe not found in source: $SourceDir"
    }

    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ("syc-ffmpeg-" + [guid]::NewGuid().ToString('N'))
    $stageBin = Join-Path $stage 'bin'
    New-Item -ItemType Directory -Force -Path $stageBin | Out-Null
    try {
        Copy-Item -LiteralPath $ffmpeg.FullName -Destination $stageBin -Force
        Copy-Item -LiteralPath $ffprobe.FullName -Destination $stageBin -Force
        $libDir = Join-Path $SourceDir 'lib'
        $hasLib = Test-Path -LiteralPath $libDir
        if ($hasLib) {
            $stageLib = Join-Path $stage 'lib'
            New-Item -ItemType Directory -Force -Path $stageLib | Out-Null
            Get-ChildItem -LiteralPath $libDir -Filter '*.so*' -File | ForEach-Object {
                Copy-Item -LiteralPath $_.FullName -Destination $stageLib -Force
            }
        }
        $part = "$Dest.part"
        $tarArgs = @('-cJf', $part, '-C', $stage, 'bin')
        if ($hasLib) { $tarArgs += 'lib' }
        & tar.exe @tarArgs
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to create Linux ffmpeg archive"
        }
        Move-Item -LiteralPath $part -Destination $Dest -Force
    }
    finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Install-FromSource {
    param([string]$SourcePath, [string]$Dest, [string]$Kind)
    $item = Get-Item -LiteralPath $SourcePath
    if ($item.PSIsContainer) {
        $archiveName = 'ffmpeg.tar.xz'
        if ($Kind -eq 'windows') { $archiveName = 'ffmpeg.zip' }
        $archive = Join-Path $item.FullName $archiveName
        if (Test-Path -LiteralPath $archive) {
            Copy-Item -LiteralPath $archive -Destination $Dest -Force
            return
        }
        if ($Kind -eq 'windows') {
            New-WindowsZip -SourceDir $item.FullName -Dest $Dest
        } else {
            New-LinuxArchive -SourceDir $item.FullName -Dest $Dest
        }
        return
    }

    $lower = $item.Name.ToLowerInvariant()
    if ($Kind -eq 'windows' -and $lower.EndsWith('.zip')) {
        Copy-Item -LiteralPath $item.FullName -Destination $Dest -Force
        return
    }
    if ($Kind -eq 'linux' -and $lower.EndsWith('.tar.xz')) {
        Copy-Item -LiteralPath $item.FullName -Destination $Dest -Force
        return
    }
    throw "Unsupported ffmpeg package/archive: $($item.FullName)"
}

$targets = if ($Target -eq 'all') { @('windows', 'linux') } else { @($Target) }
if ($Source -and $targets.Count -gt 1) {
    throw "指定 -Source 时请一次只处理一个平台"
}

if ($Source) {
    $key = $targets[0]
    $asset = $assets[$key]
    $dest = Join-Path $root $asset.Dest
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $dest) | Out-Null
    Write-Host "==> 用指定来源替换 ffmpeg $key`: $Source"
    Install-FromSource -SourcePath $Source -Dest $dest -Kind $key
    $actual = Get-Sha256 $dest
    Write-Host "==> 新归档 SHA256: $actual"
    if ($UpdateLock) {
        Set-LockSha -Key $asset.HashKey -Value $actual
        Write-Host "==> 已写入 FFMPEG.lock；如版本变化，请同时更新 version/built"
    }
    else {
        Write-Host "提示: 未更新 FFMPEG.lock；提交前请写入 $($asset.HashKey)=$actual 并核对 version" -ForegroundColor Yellow
    }
}
else {
    foreach ($key in $targets) {
        Test-Target -Key $key
    }
}