param([Parameter(Mandatory=$true)][string]$OutputPath)
$ErrorActionPreference='Stop'
trap { @{error=$_.Exception.Message;line=$_.InvocationInfo.ScriptLineNumber} | ConvertTo-Json | Set-Content -LiteralPath $OutputPath; exit 1 }
$wg='C:/Program Files/WireGuard/wg.exe'
$result=@{}
foreach($field in @('public-key','listen-port','endpoints','allowed-ips','latest-handshakes','transfer')) {
    $result[$field]=@(& $wg show syc0 $field)
}
$result.routes=@(Get-NetRoute -AddressFamily IPv4 | Where-Object { $_.DestinationPrefix -like '172.1.*' } | Select-Object DestinationPrefix,InterfaceAlias,NextHop,RouteMetric)
$result.addresses=@(Get-NetIPAddress -InterfaceAlias syc0 -AddressFamily IPv4 | Select-Object IPAddress,PrefixLength)
$result.ping4=@(ping.exe -n 3 -w 1500 -S 172.1.0.1 172.1.0.4)
$result.ping5=@(ping.exe -n 3 -w 1500 -S 172.1.0.1 172.1.0.5)
$result.firewall=@(Get-NetFirewallRule -PolicyStore ActiveStore | Where-Object DisplayName -match 'WireGuard|FileSync' | Select-Object Name,DisplayName,Enabled,Direction,Action)
$result | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
