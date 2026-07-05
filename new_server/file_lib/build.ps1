# Build the filecore Rust core lib and package the artifact into file_lib/lib/ for Go cgo.
#
# Prereqs: Rust (cargo) + x86_64-pc-windows-gnu target + mingw-w64 gcc (WinLibs MSVCRT).
# Usage: run this from the file_lib dir:  ./build.ps1

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$target = "x86_64-pc-windows-gnu"

# Ensure mingw gcc is on PATH (winget writes to user PATH; current shell may not have refreshed)
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
            [Environment]::GetEnvironmentVariable("Path", "User")

Write-Host "==> cargo build --release --target $target" -ForegroundColor Cyan
Push-Location $root
try {
    cargo build --release --target $target
    if ($LASTEXITCODE -ne 0) { throw "cargo build failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

$artifact = Join-Path $root "target\$target\release\libfilecore.a"
if (-not (Test-Path $artifact)) { throw "artifact not found: $artifact" }

$libDir = Join-Path $root "lib"
New-Item -ItemType Directory -Force -Path $libDir | Out-Null
Copy-Item $artifact (Join-Path $libDir "libfilecore.a") -Force

Write-Host "==> packaged: $(Join-Path $libDir 'libfilecore.a')" -ForegroundColor Green

# Print the native system libs the Rust staticlib needs, to keep cgo LDFLAGS in sync
Write-Host "==> native-static-libs (cgo LDFLAGS must cover these):" -ForegroundColor Cyan
$prev = $env:RUSTFLAGS
$env:RUSTFLAGS = "--print native-static-libs"
Push-Location $root
try {
    cargo rustc --release --target $target --lib 2>&1 |
        Select-String "native-static-libs:" | ForEach-Object { $_.Line }
} finally {
    Pop-Location
    $env:RUSTFLAGS = $prev
}
