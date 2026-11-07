# Build the shared sys_core for HarmonyOS OHOS targets and drop into cpp/prebuilts/.
#
# Prereqs (one-time):
#   1) DevEco Studio SDK installed (provides OHOS clang/llvm-ar/sysroot).
#   2) rustup target add aarch64-unknown-linux-ohos armv7-unknown-linux-ohos x86_64-unknown-linux-ohos
#
# Usage: run from this script's directory: ./build.ps1
#        ./build.ps1 -Offline        # cargo build --offline（依赖已在本地 cargo 缓存时可用）
#        ./build.ps1 -Targets x86_64 # 只构建部分 ABI（默认 3 个全构建）
#
# 通常从仓库级统一入口调用：harmony/build.ps1（默认行为与输出路径保持不变）。
#
# Output: harmony/products/sunyuanling/src/main/cpp/prebuilts/<abi>/libfilecore.a
# The static core is linked into the NAPI libfilecore.so by src/main/cpp/CMakeLists.txt.

param(
    [switch]$Offline,                     # cargo 加 --offline
    [string[]]$Targets = @('arm64-v8a', 'armeabi-v7a', 'x86_64')  # ABI 子集
)

$ErrorActionPreference = "Stop"
$scriptRoot = $PSScriptRoot
$coreDir = Join-Path $scriptRoot "..\..\..\sys_core" | Resolve-Path
$prebuilts = Join-Path $scriptRoot "src\main\cpp\prebuilts"

$ndkRoot = "E:\Program Files\DevEco Studio\sdk\default\openharmony\native"
$clang = Join-Path $ndkRoot "llvm\bin\clang.exe"
$clangxx = Join-Path $ndkRoot "llvm\bin\clang++.exe"
$llvmAr = Join-Path $ndkRoot "llvm\bin\llvm-ar.exe"

if (-not (Test-Path $clang)) { throw "OHOS clang not found at $clang — install DevEco Studio or adjust ndkRoot in this script" }

$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
            [Environment]::GetEnvironmentVariable("Path", "User")

$allTargets = @(
    @{ rust = "aarch64-unknown-linux-ohos"; abi = "arm64-v8a" },
    @{ rust = "armv7-unknown-linux-ohos";   abi = "armeabi-v7a" },
    @{ rust = "x86_64-unknown-linux-ohos";  abi = "x86_64" }
)
$selectedTargets = @($allTargets | Where-Object { $Targets -contains $_.abi })
if ($selectedTargets.Count -eq 0) { throw "no valid target selected; choose from: $(($allTargets | ForEach-Object { $_.abi }) -join ', ')" }

Push-Location $coreDir
try {
    foreach ($t in $selectedTargets) {
        $rustTarget = $t.rust
        $abi = $t.abi
        $cargoArgs = @('build', '--release', '--features', 'upload', '--target', $rustTarget)
        if ($Offline) { $cargoArgs += '--offline' }
        Write-Host ("==> cargo {0}  ({1})" -f ($cargoArgs -join ' '), $abi) -ForegroundColor Cyan
        $envKey = $rustTarget -replace "-", "_"
        $envCC = "CC_$envKey"
        $envCXX = "CXX_$envKey"
        $envAR = "AR_$envKey"
        Set-Item -Path "Env:$envCC" -Value $clang
        Set-Item -Path "Env:$envCXX" -Value $clangxx
        Set-Item -Path "Env:$envAR" -Value $llvmAr

        # cargo 的进度/诊断都走 stderr：先临时放宽 EAP，再把 stderr 合并回显。
        # （Windows PowerShell 5.1 下 EAP=Stop 会把 cargo 的每一行 stderr 当成终止错误）
        $origEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            & cargo @cargoArgs 2>&1 | ForEach-Object { Write-Host "$_" }
            $code = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $origEap
        }
        if ($code -ne 0) { throw "cargo build failed for $rustTarget (exit $code)" }

        $src = Join-Path $coreDir "target\$rustTarget\release\libsys_core.a"
        if (-not (Test-Path $src)) { throw "expected output not found: $src" }
        $dstDir = Join-Path $prebuilts $abi
        if (-not (Test-Path $dstDir)) { New-Item -ItemType Directory -Path $dstDir -Force | Out-Null }
        $dst = Join-Path $dstDir "libfilecore.a"
        Copy-Item -Path $src -Destination $dst -Force
        $sz = (Get-Item $dst).Length
        Write-Host ("    -> {0}  ({1:N0} bytes)" -f $dst, $sz) -ForegroundColor Green
    }
} finally {
    Pop-Location
}

Write-Host "==> done. cpp/prebuilts layout:" -ForegroundColor Green
Get-ChildItem -Recurse $prebuilts -Filter "libfilecore.a" | ForEach-Object {
    Write-Host ("    {0}  {1:N0} bytes" -f $_.FullName, $_.Length)
}
