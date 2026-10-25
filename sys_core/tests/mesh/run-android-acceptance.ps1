param(
    [Parameter(Mandatory = $true)][string]$TestPassword,
    [switch]$Cloud,
    [switch]$WindowsPeer,
    [string]$OutputPath = (Join-Path $PSScriptRoot '../../target/mesh-test/nat-android-result.txt')
)
# Authorized disposable emulator / isolated backend only. No credential is written.
$ErrorActionPreference = 'Stop'
$adb = 'C:/Users/sunyuanling/AppData/Local/Android/Sdk/platform-tools/adb.exe'
$base = 'http://192.168.31.100:8992'
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
if ($WindowsPeer) { $arguments += @('-e','testPeerUrl','http://172.1.0.4:18082/probe.txt','-e','holdMeshMs','30000') }
$arguments += 'com.sunyuanling.filesync.test/androidx.test.runner.AndroidJUnitRunner'
foreach ($argument in $arguments) { $info.ArgumentList.Add($argument) }
$process = [System.Diagnostics.Process]::Start($info)
$stdout = $process.StandardOutput.ReadToEndAsync()
$stderr = $process.StandardError.ReadToEndAsync()
$previous = ''
$deadline = [DateTime]::UtcNow.AddSeconds(130)
while (!$process.HasExited) {
    if ([DateTime]::UtcNow -gt $deadline) { throw 'Device acceptance timed out; inspect owned filesync interface before retry' }
    $result = Invoke-RestMethod "$base/v1/vpn/devices/my" -Headers $headers
    if ($result.code -ne 200) { throw 'Cannot read public test peer; check device-bound authentication' }
    $peer = $result.data | Where-Object device_id -eq '78a4307cb3ce978b' | Select-Object -First 1
    if ($peer.pubkey -and $peer.pubkey -ne $previous) {
        if ($peer.pubkey -notmatch '^[A-Za-z0-9+/]{43}=$' -or $peer.address -ne '172.1.0.3') { throw 'Unexpected test peer identity' }
        & ssh -o BatchMode=yes vm "docker exec filesync-mesh-test-gw wg set mesh-test peer '$($peer.pubkey)' allowed-ips 172.1.0.3/32 persistent-keepalive 5"
        if ($LASTEXITCODE -ne 0) { throw 'Cannot synchronize isolated gateway public peer' }
        $previous = $peer.pubkey
        if ($WindowsPeer) {
            $peerPath = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../target/mesh-test/android-peer.json'))
            [System.IO.File]::WriteAllText($peerPath + '.tmp', (@{pubkey=$peer.pubkey;address=$peer.address} | ConvertTo-Json -Compress))
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
