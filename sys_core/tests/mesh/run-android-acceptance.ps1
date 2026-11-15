param(
    [Parameter(Mandatory = $true)][string]$TestPassword,
    [switch]$Cloud,
    [switch]$WindowsPeer,
    [switch]$Isolated,
    [switch]$Refresh,
    [switch]$ShowScreen,
    [string]$OutputPath = (Join-Path $PSScriptRoot '../../target/mesh-test/nat-android-result.txt')
)
# Authorized disposable emulator / isolated backend only. No credential is written.
$ErrorActionPreference = 'Stop'
$adb = 'C:/Users/sunyuanling/AppData/Local/Android/Sdk/platform-tools/adb.exe'
$base = 'http://192.168.31.100:8991'
$package = if ($Isolated) { 'com.sunyuanling.filesync.meshtest' } else { 'com.sunyuanling.filesync' }
$iface = if ($Isolated) { 'filesync-test' } else { 'filesync' }
$device = 'filesync-nat-verifier'
$loginBody = @{username='admin';password=$TestPassword;device_id=$device} | ConvertTo-Json -Compress
$login = Invoke-RestMethod "$base/v1/user/login" -Method Post -ContentType application/json -Body $loginBody -Headers @{'Device-Id'=$device}
if ($login.code -ne 200 -or !$login.data.token) { throw 'Isolated test login failed' }
$headers = @{Token=$login.data.token;'Device-Id'=$device}
$info = [System.Diagnostics.ProcessStartInfo]::new($adb)
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
$info.RedirectStandardOutput = $true
$info.RedirectStandardError = $true
$arguments = @('-s','192.168.31.182:5557','shell','am','instrument','-w','-r',
    '-e','class','com.sunyuanling.filesync.MeshDeviceTest',
    '-e','testBase',$base,'-e','testProxy','https://8.137.77.99:12000',
    '-e','testPassword',$TestPassword,'-e','expectClash','true')
if ($Cloud) { $arguments += @('-e','testMeshViaProxy','true') }
if ($Refresh) { $arguments += @('-e','testEndpointRefresh','true') }
if ($ShowScreen) { $arguments += @('-e','showMeshScreen','true','-e','holdMeshMs','60000') }
if ($WindowsPeer) { $arguments += @('-e','testPeerUrl','http://172.1.0.4:18082/probe.txt','-e','holdMeshMs','30000') }
$arguments += "$package.test/androidx.test.runner.AndroidJUnitRunner"
# Windows PowerShell 5.1 / .NET Framework has no ProcessStartInfo.ArgumentList; build a quoted command line instead.
$info.Arguments = ($arguments | ForEach-Object { if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ } }) -join ' '
$process = [System.Diagnostics.Process]::Start($info)
$stdout = $process.StandardOutput.ReadToEndAsync()
$stderr = $process.StandardError.ReadToEndAsync()
$previous = ''
$deadline = [DateTime]::UtcNow.AddSeconds(130)
while (!$process.HasExited) {
    if ([DateTime]::UtcNow -gt $deadline) { throw 'Device acceptance timed out; inspect owned filesync interface before retry' }
    $result = Invoke-RestMethod "$base/v1/vpn/devices/my" -Headers $headers
    if ($result.code -ne 200) { throw 'Cannot read public test peer; check device-bound authentication' }
    # Windows PowerShell 5.1 turns native stderr into a terminating error under ErrorActionPreference=Stop;
    # the polling below is expected to hit "No such device" until the test brings the interface up.
    $previousEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $public = (& $adb -s 192.168.31.182:5557 shell "su -c '/data/user/0/$package/code_cache/bin/wg show $iface public-key'" 2>$null | Select-Object -First 1)
    $ErrorActionPreference = $previousEap
    if (!$public -or $public.Trim() -notmatch '^[A-Za-z0-9+/]{43}=$') { Start-Sleep -Seconds 1; continue }
    $peer = $result.data | Where-Object pubkey -eq $public.Trim() | Select-Object -First 1
    if ($peer.pubkey -and $peer.pubkey -ne $previous) {
        if ($peer.pubkey -notmatch '^[A-Za-z0-9+/]{43}=$' -or $peer.address -notmatch '^172\.1\.0\.\d{1,3}$') { throw 'Unexpected test peer identity' }
        $previousEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        & ssh -o BatchMode=yes vm "docker exec filesync-mesh-test-gw wg set mesh-test peer '$($peer.pubkey)' allowed-ips '$($peer.address)/32' persistent-keepalive 5"
        $sshExit = $LASTEXITCODE
        $ErrorActionPreference = $previousEap
        if ($sshExit -ne 0) { throw 'Cannot synchronize isolated gateway public peer' }
        $previous = $peer.pubkey
        if ($WindowsPeer) {
            $peerPath = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../target/mesh-test/android-peer.json'))
            [System.IO.File]::WriteAllText($peerPath + '.tmp', (@{pubkey=$peer.pubkey;address=$peer.address;port=$(if ($Isolated) {51823} else {51821})} | ConvertTo-Json -Compress))
            Move-Item -LiteralPath ($peerPath + '.tmp') -Destination $peerPath -Force
        }
    }
    Start-Sleep -Seconds 1
}
$output = $stdout.GetAwaiter().GetResult() + $stderr.GetAwaiter().GetResult()
# Defensive redaction if a future runner echoes invocation parameters on failure.
$output = $output.Replace($TestPassword, '[redacted]')
[System.IO.File]::WriteAllText([System.IO.Path]::GetFullPath($OutputPath), $output)
Write-Output $output
if ($output -notmatch 'OK \(1 test\)') { throw 'Android device acceptance failed; see result file' }
