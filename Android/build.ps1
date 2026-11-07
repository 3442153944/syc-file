<#
.SYNOPSIS
  Android build entry: package the sys_core JNI binaries first, then run Gradle.

.DESCRIPTION
  gradle-local.ps1 alone is not enough for APK work: sync_core_jni (../../sys_core) must
  be cross-compiled into app/src/main/jniLibs/<abi>/libsync_core_jni.so first. This script
  refreshes PATH, locates/validates the NDK, checks cargo/cargo-ndk and rustup targets,
  runs "cargo ndk ... build --release [--offline]", verifies every libsync_core_jni.so
  (size + SHA256), then runs gradle-local.ps1 unless -NativeOnly.
  Options: -Tasks (gradle tasks, default :app:assembleDebug, array or comma separated),
  -NativeOnly (JNI only, stop before Gradle), -Online (allow network; default offline),
  -Targets (ABIs, default arm64-v8a,x86_64); extra positional args are passed to Gradle.
  Example: powershell -NoProfile -File Android/build.ps1 -NativeOnly
           powershell -NoProfile -File Android/build.ps1 -Tasks ':app:testDebugUnitTest,:app:assembleDebug' --offline
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0, ValueFromRemainingArguments = $true)]
    [string[]]$GradleArguments,
    [Parameter()]
    [string[]]$Tasks = @(':app:assembleDebug'),
    [Parameter()]
    [switch]$NativeOnly,
    [Parameter()]
    [switch]$Online,
    [Parameter()]
    [string[]]$Targets = @('arm64-v8a', 'x86_64')
)
$ErrorActionPreference = 'Stop'
$AndroidRoot = $PSScriptRoot
$RepoRoot = Split-Path -Parent $AndroidRoot
$JniProjectDir = Join-Path $AndroidRoot 'sync_core_jni'
$JniLibsDir = Join-Path $AndroidRoot 'app\src\main\jniLibs'
$GradleLocal = Join-Path $AndroidRoot 'gradle-local.ps1'
$KnownNdkPaths = @('F:\download\android-ndk-r27d')
$AbiToRustTarget = @{
    'arm64-v8a' = 'aarch64-linux-android'; 'x86_64' = 'x86_64-linux-android'
    'armeabi-v7a' = 'armv7-linux-androideabi'; 'x86' = 'i686-linux-android'
    'riscv64' = 'riscv64-linux-android'
}

function Write-Step([string]$Message) { Write-Host "==> $Message" -ForegroundColor Cyan }
function Write-Detail([string]$Message) { Write-Host "    $Message" }
function Write-Result([string]$Message) { Write-Host "==> $Message" -ForegroundColor Green }
function Fail([string]$Message) { throw $Message }
# Native probes must not turn stderr text into terminating errors on PS 5.1.
function Invoke-Probe([string]$FilePath, [string[]]$Arguments = @()) {
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $FilePath @Arguments 2>&1 | Out-String
        return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = $output.Trim() }
    } catch {
        return [pscustomobject]@{ ExitCode = 127; Output = $_.Exception.Message }
    } finally {
        $ErrorActionPreference = $previous
    }
}
function Update-BuildPath {
    foreach ($source in @([Environment]::GetEnvironmentVariable('Path', 'Machine'),
            [Environment]::GetEnvironmentVariable('Path', 'User'))) {
        if (-not [string]::IsNullOrWhiteSpace($source)) { $env:Path = "$env:Path;$source" }
    }
    $cargoBin = Join-Path $env:USERPROFILE '.cargo\bin'
    if ((Test-Path -LiteralPath $cargoBin) -and ($env:Path -notlike "*$cargoBin*")) { $env:Path += ";$cargoBin" }
}
function Read-NdkVersion([string]$NdkRoot) {
    $properties = Join-Path $NdkRoot 'source.properties'
    if (Test-Path -LiteralPath $properties) {
        foreach ($line in (Get-Content -LiteralPath $properties -ErrorAction SilentlyContinue)) {
            if ($line -match '^\s*Pkg\.Revision\s*=\s*(\S+)\s*$') { return $Matches[1] }
        }
    }
    return (Split-Path -Leaf $NdkRoot)
}

# Return the prebuilt host directory name (e.g. windows-x86_64) or $null.
function Find-NdkToolchainHost([string]$NdkRoot) {
    $prebuilt = Join-Path $NdkRoot 'toolchains\llvm\prebuilt'
    if (-not (Test-Path -LiteralPath $prebuilt)) { return $null }
    foreach ($hostName in @('windows-x86_64', 'windows')) {
        if (Test-Path -LiteralPath (Join-Path $prebuilt "$hostName\bin\clang.exe")) { return $hostName }
    }
    foreach ($dir in @(Get-ChildItem -LiteralPath $prebuilt -Directory -ErrorAction SilentlyContinue)) {
        if (Test-Path -LiteralPath (Join-Path $dir.FullName 'bin\clang.exe')) { return $dir.Name }
    }
    return $null
}
function Get-SdkRoots {
    $roots = New-Object 'System.Collections.Generic.List[string]'
    $localProperties = Join-Path $AndroidRoot 'local.properties'
    if (Test-Path -LiteralPath $localProperties) {
        foreach ($line in (Get-Content -LiteralPath $localProperties -ErrorAction SilentlyContinue)) {
            if ($line -match '^\s*sdk\.dir\s*=\s*(.+?)\s*$') {
                # local.properties escapes the drive colon: sdk.dir=C\:\\path
                $sdk = $Matches[1] -replace '\\\\', '\' -replace '\\:', ':'
                if (-not [string]::IsNullOrWhiteSpace($sdk)) { $roots.Add($sdk) }
            }
        }
    }
    foreach ($envName in @('ANDROID_HOME', 'ANDROID_SDK_ROOT')) {
        $value = [Environment]::GetEnvironmentVariable($envName)
        if (-not [string]::IsNullOrWhiteSpace($value)) { $roots.Add($value) }
    }
    if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) { $roots.Add((Join-Path $env:LOCALAPPDATA 'Android\Sdk')) }
    return $roots
}

function Resolve-Ndk {
    $candidates = New-Object 'System.Collections.Generic.List[object]'
    foreach ($envName in @('ANDROID_NDK_HOME', 'ANDROID_NDK_ROOT')) {
        $value = [Environment]::GetEnvironmentVariable($envName)
        if (-not [string]::IsNullOrWhiteSpace($value)) {
            $candidates.Add([pscustomobject]@{ Source = $envName; Path = $value })
        }
    }
    foreach ($knownPath in $KnownNdkPaths) { $candidates.Add([pscustomobject]@{ Source = 'known path'; Path = $knownPath }) }
    foreach ($sdkRoot in (Get-SdkRoots)) {
        $ndkDir = Join-Path $sdkRoot 'ndk'
        if (-not (Test-Path -LiteralPath $ndkDir)) { continue }
        $versions = New-Object 'System.Collections.Generic.List[object]'
        foreach ($dir in @(Get-ChildItem -LiteralPath $ndkDir -Directory -ErrorAction SilentlyContinue)) {
            $display = Read-NdkVersion $dir.FullName
            $parsed = [version]'0.0'
            try { $parsed = [version]($display -replace '[^0-9\.].*$', '') } catch { }
            $versions.Add([pscustomobject]@{ Path = $dir.FullName; Version = $parsed })
        }
        foreach ($item in @($versions | Sort-Object -Property Version -Descending)) {
            $candidates.Add([pscustomobject]@{ Source = "sdk.ndk under $sdkRoot"; Path = $item.Path })
        }
    }
    foreach ($candidate in $candidates) {
        if (-not (Test-Path -LiteralPath $candidate.Path)) { continue }
        $toolchainHost = Find-NdkToolchainHost $candidate.Path
        if ($toolchainHost) {
            return [pscustomobject]@{
                Path = (Resolve-Path -LiteralPath $candidate.Path).Path
                Host = $toolchainHost
                Version = (Read-NdkVersion $candidate.Path)
                Source = $candidate.Source
            }
        }
        Write-Warning "NDK candidate has no usable LLVM toolchain, skipped: $($candidate.Path) (source: $($candidate.Source))"
    }
    $tried = ($candidates | ForEach-Object { "  - $($_.Source): $($_.Path)" }) -join "`n"
    Fail ("Android NDK not found or incomplete. Tried:`n$tried`nInstall Android NDK (r27 or newer) and set ANDROID_NDK_HOME, e.g. " +
        "`$env:ANDROID_NDK_HOME = 'F:\download\android-ndk-r27d', or install it with Android Studio SDK Manager so it appears under <sdk>\ndk\<version>.")
}

function Assert-CargoTools {
    $cargo = Get-Command cargo -CommandType Application -ErrorAction SilentlyContinue
    if (-not $cargo) {
        Fail ("cargo was not found on PATH.`nInstall Rust with rustup (https://rustup.rs) or refresh PATH, then retry.`n" +
            "Expected: $env:USERPROFILE\.cargo\bin\cargo.exe")
    }
    $cargoVersion = Invoke-Probe $cargo.Source @('--version')
    if ($cargoVersion.ExitCode -ne 0) { Fail "cargo --version failed: $($cargoVersion.Output)" }
    Write-Detail $cargoVersion.Output
    $ndkPlugin = Invoke-Probe $cargo.Source @('ndk', '--version')
    if ($ndkPlugin.ExitCode -ne 0) {
        Fail ("cargo-ndk is not installed (cargo ndk --version failed).`nInstall it: cargo install cargo-ndk`nDetails: $($ndkPlugin.Output)")
    }
    Write-Detail $ndkPlugin.Output
}

function Assert-RustTargets {
    $rustTargets = New-Object 'System.Collections.Generic.List[string]'
    foreach ($abi in $Targets) {
        $rustTarget = $AbiToRustTarget[$abi]
        if (-not $rustTarget) {
            Fail "Unsupported Android ABI in -Targets: '$abi'. Supported: $($AbiToRustTarget.Keys -join ', ')"
        }
        if (-not $rustTargets.Contains($rustTarget)) { $rustTargets.Add($rustTarget) }
    }
    $rustup = Get-Command rustup -CommandType Application -ErrorAction SilentlyContinue
    if (-not $rustup) {
        Fail ("rustup was not found on PATH.`nInstall Rust with rustup (https://rustup.rs), then: rustup target add $($rustTargets -join ' ')")
    }
    $probe = Invoke-Probe $rustup.Source @('target', 'list', '--installed')
    if ($probe.ExitCode -ne 0) { Fail "rustup target list failed ($($probe.ExitCode)): $($probe.Output)" }
    $installed = @($probe.Output -split '\r?\n' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    $missing = @($rustTargets | Where-Object { $installed -notcontains $_ })
    if ($missing.Count -gt 0) {
        if (-not $Online) {
            Fail ("Missing rustup target(s) for Android: $($missing -join ', ')`nInstall them (needs network): " +
                "rustup target add $($missing -join ' ')`nor rerun this script with -Online.")
        }
        Write-Step "Installing missing rustup target(s): $($missing -join ', ')"
        & $rustup.Source target add @missing
        if ($LASTEXITCODE -ne 0) { Fail "rustup target add failed ($LASTEXITCODE): $($missing -join ' ')" }
    }
    Write-Detail "rustup targets ok: $($rustTargets -join ', ')"
}

function Invoke-NativeBuild {
    $cargo = (Get-Command cargo -CommandType Application).Source
    $arguments = @('ndk')
    foreach ($abi in $Targets) { $arguments += @('-t', $abi) }
    $arguments += @('-o', $JniLibsDir, 'build', '--release')
    if (-not $Online) { $arguments += '--offline' }
    New-Item -ItemType Directory -Force -Path $JniLibsDir | Out-Null
    Write-Step "cargo $($arguments -join ' ')"
    Write-Detail "cwd: $JniProjectDir"
    Push-Location $JniProjectDir
    try {
        & $cargo @arguments
        $exitCode = $LASTEXITCODE
    } finally {
        Pop-Location
    }
    if ($exitCode -ne 0) {
        Fail ("cargo ndk build failed (exit $exitCode). Fix the Rust errors printed above; the crate is " +
            "$JniProjectDir and depends on $RepoRoot\sys_core.")
    }
}

function Assert-JniArtifacts {
    Write-Step 'JNI artifacts'
    $missing = New-Object 'System.Collections.Generic.List[string]'
    foreach ($abi in $Targets) {
        $artifact = Join-Path (Join-Path $JniLibsDir $abi) 'libsync_core_jni.so'
        if (-not (Test-Path -LiteralPath $artifact)) { $missing.Add($artifact); continue }
        $file = Get-Item -LiteralPath $artifact
        $hash = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash.ToLowerInvariant()
        Write-Detail ('{0}  {1:N0} bytes  SHA256 {2}  mtime {3}' -f $file.FullName, $file.Length, $hash,
            $file.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'))
    }
    if ($missing.Count -gt 0) { Fail ("Missing JNI artifact(s) after cargo ndk build:`n  " + ($missing -join "`n  ")) }
}
function Show-ApkArtifacts {
    $apkRoot = Join-Path $AndroidRoot 'app\build\outputs\apk'
    if (-not (Test-Path -LiteralPath $apkRoot)) { return }
    $apks = @(Get-ChildItem -LiteralPath $apkRoot -Recurse -Filter '*.apk' -File -ErrorAction SilentlyContinue |
        Sort-Object -Property LastWriteTime -Descending)
    if ($apks.Count -eq 0) { return }
    Write-Step 'APK artifacts'
    foreach ($apk in @($apks | Select-Object -First 4)) {
        Write-Detail ('{0}  {1:N0} bytes  mtime {2}' -f $apk.FullName, $apk.Length, $apk.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'))
    }
}

function Invoke-GradleBuild {
    if (-not (Test-Path -LiteralPath $GradleLocal)) { Fail "Missing Gradle entry point: $GradleLocal" }
    if (-not (Test-Path -LiteralPath (Join-Path $AndroidRoot 'gradlew.bat'))) {
        Fail "Missing Gradle wrapper: $(Join-Path $AndroidRoot 'gradlew.bat')"
    }
    $gradleArgs = New-Object 'System.Collections.Generic.List[string]'
    foreach ($task in $Tasks) { $gradleArgs.Add($task) }
    if ($GradleArguments) { foreach ($item in $GradleArguments) { $gradleArgs.Add($item) } }
    # gradle-local.ps1 calls exit; run it in its own process to keep the exit code isolated.
    $hostExe = (Get-Process -Id $PID).Path
    Write-Step "gradle $($gradleArgs -join ' ')"
    Write-Detail "via $GradleLocal"
    & $hostExe -NoProfile -ExecutionPolicy Bypass -File $GradleLocal @gradleArgs
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) { Fail "Gradle build failed (exit $exitCode). Fix the Gradle errors printed above." }
    Show-ApkArtifacts
}

function Invoke-AndroidBuild {
    # Accept arrays and comma separated strings (both plain and "powershell -File" calling styles).
    $script:Tasks = @(@($Tasks) | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    $script:Targets = @(@($Targets) | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    if ($Tasks.Count -eq 0) { $Tasks = @(':app:assembleDebug') }
    if ($Targets.Count -eq 0) { Fail '-Targets resolved to an empty list.' }

    Update-BuildPath
    Write-Step 'Android NDK'
    $ndk = Resolve-Ndk
    $env:ANDROID_NDK_HOME = $ndk.Path
    if ([string]::IsNullOrWhiteSpace($env:ANDROID_NDK_ROOT)) { $env:ANDROID_NDK_ROOT = $ndk.Path }
    Write-Detail "version $($ndk.Version) (source: $($ndk.Source))"
    Write-Detail "path: $($ndk.Path)"
    Write-Detail "llvm host: $($ndk.Host)"

    Write-Step 'Rust toolchain'
    Assert-CargoTools
    Assert-RustTargets

    Invoke-NativeBuild
    Assert-JniArtifacts
    if ($NativeOnly) {
        Write-Result "Done (native only): sys_core JNI packaged into $JniLibsDir"
        return
    }
    Invoke-GradleBuild
    Write-Result 'Done: sys_core JNI packaged and Gradle tasks finished.'
}

try {
    Invoke-AndroidBuild
} catch {
    Write-Host ''
    Write-Host "BUILD FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
exit 0
