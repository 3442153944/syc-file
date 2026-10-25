# Windows Gradle entry point with a valid, short Unix-domain socket directory.
# Usage: .\gradle-local.ps1 :app:assembleDebug --offline --console=plain
$ErrorActionPreference = "Stop"
$gradleArguments = @($args)
if ($gradleArguments.Count -eq 0) {
    $gradleArguments = @(':app:assembleDebug')
}

$socketTemp = Join-Path $PSScriptRoot '.gradle\uds'
New-Item -ItemType Directory -Force -Path $socketTemp | Out-Null
$socketTemp = (Resolve-Path -LiteralPath $socketTemp).Path.Replace('\', '/')

$previousJavaOptions = $env:JAVA_TOOL_OPTIONS
$previousGradleHome = $env:GRADLE_USER_HOME
$gradleExitCode = 1
try {
    # JAVA_TOOL_OPTIONS reaches the wrapper, daemon and worker JVMs.
    $socketOption = '-Djdk.net.unixdomain.tmpdir="' + $socketTemp + '"'
    $env:JAVA_TOOL_OPTIONS = (@($previousJavaOptions, $socketOption) |
        Where-Object { -not [string]::IsNullOrWhiteSpace($_) }) -join ' '

    # Some launcher environments report Java user.home as C:\ instead of USERPROFILE.
    if ([string]::IsNullOrWhiteSpace($env:GRADLE_USER_HOME)) {
        $env:GRADLE_USER_HOME = Join-Path $env:USERPROFILE '.gradle'
    }

    Push-Location $PSScriptRoot
    try {
        & (Join-Path $PSScriptRoot 'gradlew.bat') @gradleArguments
        $gradleExitCode = $LASTEXITCODE
    } finally {
        Pop-Location
    }
} finally {
    $env:JAVA_TOOL_OPTIONS = $previousJavaOptions
    $env:GRADLE_USER_HOME = $previousGradleHome
}
exit $gradleExitCode
