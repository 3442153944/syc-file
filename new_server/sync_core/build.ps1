# Legacy entry point; current Go code links the canonical sys_core artifact.
$ErrorActionPreference = 'Stop'
$core = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../sys_core'))
& (Join-Path $core 'build.ps1') @args
if ($LASTEXITCODE -ne 0) { throw "Shared core build failed ($LASTEXITCODE)" }
$lib = Join-Path $PSScriptRoot 'lib'
New-Item -ItemType Directory -Force -Path $lib | Out-Null
Copy-Item -LiteralPath (Join-Path $core 'lib/libsys_core.a') -Destination (Join-Path $lib 'libsync_core.a') -Force
Write-Host "==> legacy compatibility copy: $(Join-Path $lib 'libsync_core.a')"
