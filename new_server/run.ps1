# new_server launcher.
#
# This is now a cgo project (pkg/filecore statically links file_lib/lib/libfilecore.a),
# so CGO_ENABLED=1 and mingw-w64 gcc on PATH are REQUIRED. Otherwise you get:
#   "build constraints exclude all Go files in .../pkg/filecore"
#
# Usage (run from the new_server dir):
#   ./run.ps1            build and run
#   ./run.ps1 -Build     only build the binary (syc-file.exe), do not run

param([switch]$Build)

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot

# 1) Refresh PATH so the winget-installed mingw gcc is visible (new shells may lag)
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
            [Environment]::GetEnvironmentVariable("Path", "User")

if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    throw "gcc not found. Install mingw-w64: winget install --id BrechtSanders.WinLibs.POSIX.MSVCRT"
}

# 2) cgo switches
$env:CGO_ENABLED = "1"
$env:CC = "gcc"

# 3) Ensure the Rust core lib is built
$lib = Join-Path $root "file_lib\lib\libfilecore.a"
if (-not (Test-Path $lib)) {
    Write-Host "==> libfilecore.a missing, building file_lib" -ForegroundColor Yellow
    & (Join-Path $root "file_lib\build.ps1")
}

# 4) Locate go (may not be on default PATH)
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = "C:\Program Files\Go\bin\go.exe" }
if (-not (Test-Path $go)) { throw "go executable not found" }

Push-Location $root
try {
    if ($Build) {
        Write-Host "==> go build -> syc-file.exe" -ForegroundColor Cyan
        & $go build -o syc-file.exe ./cmd
        if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }
        Write-Host "==> built $(Join-Path $root 'syc-file.exe')" -ForegroundColor Green
    } else {
        Write-Host "==> go run ./cmd" -ForegroundColor Cyan
        & $go run ./cmd
    }
} finally {
    Pop-Location
}
