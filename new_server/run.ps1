# new_server launcher.
#
# This is now a cgo project (pkg/sync_core statically links sync_core/lib/libsync_core.a),
# so CGO_ENABLED=1 and mingw-w64 gcc on PATH are REQUIRED. Otherwise you get:
#   "build constraints exclude all Go files in .../pkg/sync_core"
#
# Usage (run from the new_server dir):
#   ./run.ps1                              build and run
#   ./run.ps1 -Build                       only build the binary (syc-file.exe), do not run
#   ./run.ps1 -FfmpegSource D:\Player      copy ffmpeg from the given directory or archive

param(
    [switch]$Build,
    [string]$FfmpegSource = ""
)

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
if ($FfmpegSource) {
    $env:FFMPEG_SOURCE = $FfmpegSource
}

# 1) Refresh PATH so the winget-installed mingw gcc is visible (new shells may lag)
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
            [Environment]::GetEnvironmentVariable("Path", "User")

if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    throw "gcc not found. Install mingw-w64: winget install --id BrechtSanders.WinLibs.POSIX.MSVCRT"
}

# 2) cgo switches
$env:CGO_ENABLED = "1"
$env:CC = "gcc"

# 3) Locate go (may not be on default PATH)
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = "C:\Program Files\Go\bin\go.exe" }
if (-not (Test-Path $go)) { throw "go executable not found" }

Push-Location $root
try {
    if ($Build) {
        & $go run ./cmd/dev -build
    } else {
        & $go run ./cmd/dev
    }
    if ($LASTEXITCODE -ne 0) { throw "development launcher failed ($LASTEXITCODE)" }
} finally {
    Pop-Location
}
