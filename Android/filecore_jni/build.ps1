# Build filecore_jni for Android and drop .so files into app/src/main/jniLibs/.
#
# Prereqs (one-time):
#   1) Android NDK (via Android Studio SDK Manager), and set ANDROID_NDK_HOME, e.g.
#      $env:ANDROID_NDK_HOME = "$env:LOCALAPPDATA\Android\Sdk\ndk\<version>"
#   2) rustup target add aarch64-linux-android x86_64-linux-android
#   3) cargo install cargo-ndk
#
# Usage: run from this dir:  ./build.ps1

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$out = Join-Path $root "..\app\src\main\jniLibs"
$env:ANDROID_NDK_HOME = "F:\download\android-ndk-r27d"

# Refresh PATH (cargo-ndk installed to user PATH may not be visible in fresh shells)
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
            [Environment]::GetEnvironmentVariable("Path", "User")

# Locate NDK if ANDROID_NDK_HOME is unset: pick newest under <sdk>/ndk
if (-not $env:ANDROID_NDK_HOME) {
    $sdk = Join-Path $env:LOCALAPPDATA "Android\Sdk\ndk"
    if (Test-Path $sdk) {
        $newest = Get-ChildItem $sdk -Directory | Sort-Object Name -Descending | Select-Object -First 1
        if ($newest) {
            $env:ANDROID_NDK_HOME = $newest.FullName
            Write-Host "==> ANDROID_NDK_HOME = $($env:ANDROID_NDK_HOME)" -ForegroundColor Yellow
        }
    }
}
if (-not $env:ANDROID_NDK_HOME) { throw "ANDROID_NDK_HOME not set and no NDK found under Android SDK" }

Write-Host "==> cargo ndk build (arm64-v8a, x86_64)" -ForegroundColor Cyan
Push-Location $root
try {
    cargo ndk -t arm64-v8a -t x86_64 -o $out build --release
    if ($LASTEXITCODE -ne 0) { throw "cargo ndk build failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

Write-Host "==> packaged into $out" -ForegroundColor Green
Get-ChildItem -Recurse $out -Filter "libfilecore_jni.so" | ForEach-Object {
    Write-Host ("    {0}  {1:N0} bytes" -f $_.FullName, $_.Length)
}
