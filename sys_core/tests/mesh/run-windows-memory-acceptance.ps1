$ErrorActionPreference='Continue'
$directory=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../target/mesh-test'))
$executable=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../target/debug/examples/windows_memory_tunnel.exe'))
$output=& $executable 2>&1 | Out-String
$code=$LASTEXITCODE
[System.IO.File]::WriteAllText((Join-Path $directory 'windows-shared-memory-result.txt'),$output+"`nExitCode=$code`n")
exit $code
