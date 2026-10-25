param([Parameter(Mandatory=$true)][string]$TestDirectory)
$ErrorActionPreference = 'Stop'
$iface = 'fs-mesh-test'
$wireguard = 'C:\Program Files\WireGuard\wireguard.exe'
$wg = Join-Path $PSScriptRoot '..\..\..\filesync-desktop\src-tauri\resources\wireguard\wg.exe'
$config = Join-Path $TestDirectory "$iface.conf"
$ready = Join-Path $TestDirectory 'windows-ready.txt'
$installed = $false
if (Get-Service 'WireGuardTunnel$fs-mesh-test' -ErrorAction SilentlyContinue) { throw 'Test service already exists' }
try {
    $result = Start-Process -FilePath $wireguard -ArgumentList @('/installtunnelservice', $config) -WindowStyle Hidden -Wait -PassThru
    if ($result.ExitCode -ne 0) { throw "Test tunnel install failed: $($result.ExitCode)" }
    $installed = $true
    for ($i=0; $i -lt 30; $i++) {
        if (Get-NetIPAddress -InterfaceAlias $iface -AddressFamily IPv4 -ErrorAction SilentlyContinue) { break }
        Start-Sleep -Milliseconds 300
    }
    New-NetFirewallRule -Name 'FileSyncMeshTest20261025UDP' -DisplayName 'FileSync temporary mesh test UDP' -Direction Inbound -Action Allow -Protocol UDP -LocalPort 51823 -RemoteAddress 192.168.31.182 -Profile Any | Out-Null
    Set-Content -LiteralPath $ready -Value 'ready'
    $lastPeer = ''
    $deadline = (Get-Date).AddMinutes(3)
    while ((Get-Date) -lt $deadline -and !(Test-Path (Join-Path $TestDirectory 'windows-stop.txt'))) {
        $path = Join-Path $TestDirectory 'android-peer.json'
        if (Test-Path $path) {
            $peer = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
            if ($peer.pubkey -ne $lastPeer) {
                if ($peer.pubkey -notmatch '^[A-Za-z0-9+/]{43}=$' -or $peer.address -notmatch '^172\.1\.0\.\d{1,3}$') { throw 'Invalid public test peer' }
                $port = if ($peer.port) { [int]$peer.port } else { 51821 }
                if ($port -notin @(51821,51823)) { throw 'Unexpected test peer port' }
                if ($lastPeer) { & $wg set $iface peer $lastPeer remove }
                Remove-NetFirewallRule -Name 'FileSyncMeshTest20261025TCP' -ErrorAction SilentlyContinue
                New-NetFirewallRule -Name 'FileSyncMeshTest20261025TCP' -DisplayName 'FileSync temporary mesh test TCP' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 18082 -LocalAddress 172.1.0.4 -RemoteAddress $peer.address -InterfaceAlias $iface -Profile Any | Out-Null
                if (!(Get-NetRoute -DestinationPrefix "$($peer.address)/32" -InterfaceAlias $iface -ErrorAction SilentlyContinue)) {
                    New-NetRoute -DestinationPrefix "$($peer.address)/32" -InterfaceAlias $iface -NextHop '0.0.0.0' -PolicyStore ActiveStore | Out-Null
                }
                & $wg set $iface peer $peer.pubkey allowed-ips "$($peer.address)/32" endpoint "192.168.31.182:$port" persistent-keepalive 5
                if ($LASTEXITCODE -ne 0) { throw 'Test peer update failed' }
                $lastPeer = $peer.pubkey
            }
            @{endpoints=(& $wg show $iface endpoints);handshakes=(& $wg show $iface latest-handshakes);transfer=(& $wg show $iface transfer)} |
                ConvertTo-Json -Compress | Set-Content (Join-Path $TestDirectory 'windows-status.json')
        }
        Start-Sleep -Seconds 1
    }
} catch {
    $_.Exception.Message | Set-Content (Join-Path $TestDirectory 'windows-error.txt')
    throw
} finally {
    foreach($name in @('FileSyncMeshTest20261025UDP','FileSyncMeshTest20261025TCP')) { Remove-NetFirewallRule -Name $name -ErrorAction SilentlyContinue }
    if ($installed) { Start-Process -FilePath $wireguard -ArgumentList @('/uninstalltunnelservice',$iface) -WindowStyle Hidden -Wait }
    if ($installed) { Remove-Item -LiteralPath $config -ErrorAction SilentlyContinue }
    Set-Content (Join-Path $TestDirectory 'windows-cleaned.txt') 'cleaned'
}
