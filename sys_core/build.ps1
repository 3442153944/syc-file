# Canonical Windows/amd64 C ABI build for Go/MinGW. Extra arguments go to Cargo.
$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$target = 'x86_64-pc-windows-gnu'
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    $env:Path += ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
}
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { throw 'MinGW GCC is required for the Go C ABI build' }
$targetDirectory = if ($env:CARGO_TARGET_DIR) { $env:CARGO_TARGET_DIR } else { Join-Path $root 'target' }
if (-not [IO.Path]::IsPathRooted($targetDirectory)) { $targetDirectory = Join-Path $root $targetDirectory }
Write-Host "==> shared sys_core: $root ($target)"
& cargo build --manifest-path (Join-Path $root 'Cargo.toml') --release --lib --locked --target $target --target-dir $targetDirectory @args
if ($LASTEXITCODE -ne 0) { throw "Shared core build failed ($LASTEXITCODE)" }
$artifact = Join-Path $targetDirectory "$target/release/libsys_core.a"
if (-not (Test-Path -LiteralPath $artifact)) { throw "Missing shared core artifact: $artifact" }
$libDirectory = Join-Path $root 'lib'
New-Item -ItemType Directory -Force -Path $libDirectory | Out-Null
$temporary = Join-Path $libDirectory ('.libsys_core-' + [guid]::NewGuid() + '.tmp')
try {
    Copy-Item -LiteralPath $artifact -Destination $temporary
    Move-Item -LiteralPath $temporary -Destination (Join-Path $libDirectory 'libsys_core.a') -Force
} finally {
    if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary }
}
Write-Host "==> shared C ABI: $(Join-Path $libDirectory 'libsys_core.a')"
