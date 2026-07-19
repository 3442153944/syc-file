# Build libfilecore.a for HarmonyOS OHOS targets and drop into cpp/prebuilts/.
#
# Prereqs (one-time):
#   1) DevEco Studio SDK installed (provides OHOS clang/llvm-ar/sysroot).
#   2) rustup target add aarch64-unknown-linux-ohos armv7-unknown-linux-ohos x86_64-unknown-linux-ohos
#
# Usage: run from this script's directory:  ./build.ps1
#
# Output: harmony/products/sunyuanling/src/main/cpp/prebuilts/<abi>/libfilecore.a
#
# Mirrors Android/filecore_jni/build.ps1 but for HarmonyOS NAPI targets.
# The .a is linked into the NAPI .so by ../cpp/CMakeLists.txt; ArkTS calls
# via `import filecore from 'libfilecore.so'` (see ../cpp/types/libfilecore/Index.d.ts).

$ErrorActionPreference = "Stop"
$scriptRoot = $PSScriptRoot
$fileLibDir = Join-Path $scriptRoot "..\..\..\new_server\file_lib" | Resolve-Path
$prebuilts = Join-Path $scriptRoot "src\main\cpp\prebuilts"

# OHOS NDK paths (DevEco Studio default install)
$ndkRoot = "E:\Program Files\DevEco Studio\sdk\default\openharmony\native"
$clang = Join-Path $ndkRoot "llvm\bin\clang.exe"
$clangxx = Join-Path $ndkRoot "llvm\bin\clang++.exe"
$llvmAr = Join-Path $ndkRoot "llvm\bin\llvm-ar.exe"

if (-not (Test-Path $clang)) { throw "OHOS clang not found at $clang — install DevEco Studio or adjust ndkRoot in this script" }

# Refresh PATH (cargo may need to find linker tools)
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
            [Environment]::GetEnvironmentVariable("Path", "User")

# Cargo env: per-target CC/CXX/AR (Rust target names use underscores)
$targets = @(
    @{ rust = "aarch64-unknown-linux-ohos"; abi = "arm64-v8a" },
    @{ rust = "armv7-unknown-linux-ohos";   abi = "armeabi-v7a" },
    @{ rust = "x86_64-unknown-linux-ohos";  abi = "x86_64" }
)

Push-Location $fileLibDir
try {
    foreach ($t in $targets) {
        $rustTarget = $t.rust
        $abi = $t.abi
        Write-Host "==> cargo build --release --target $rustTarget  ($abi)" -ForegroundColor Cyan
        $envKey = $rustTarget -replace "-", "_"
        $envCC = "CC_$envKey"
        $envCXX = "CXX_$envKey"
        $envAR = "AR_$envKey"
        Set-Item -Path "Env:$envCC" -Value $clang
        Set-Item -Path "Env:$envCXX" -Value $clangxx
        Set-Item -Path "Env:$envAR" -Value $llvmAr

        # Cargo prints progress to stderr; redirect to stdout so PowerShell
        # NativeCommandError doesn't trip on it under ErrorActionPreference=Stop.
        $origPref = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        & cargo build --release --target $rustTarget 2>&1 | Out-Host
        $code = $LASTEXITCODE
        $ErrorActionPreference = $origPref
        if ($code -ne 0) { throw "cargo build failed for $rustTarget (exit $code)" }

        $src = Join-Path $fileLibDir "target\$rustTarget\release\libfilecore.a"
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
