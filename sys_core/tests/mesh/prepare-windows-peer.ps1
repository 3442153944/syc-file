param([Parameter(Mandatory=$true)][string]$TestPassword)
$ErrorActionPreference='Stop'
$directory=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../target/mesh-test'))
$config=Join-Path $directory 'fs-mesh-test.conf'
if (Test-Path -LiteralPath $config) { throw 'Owned test config already exists; inspect before replacing' }
if (Get-Service 'WireGuardTunnel$fs-mesh-test' -ErrorAction SilentlyContinue) { throw 'Test service already exists' }
$wg=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../../filesync-desktop/src-tauri/resources/wireguard/wg.exe'))
$device='filesync-windows-mesh-20261025'
$base='http://192.168.31.100:8991'
$login=Invoke-RestMethod "$base/v1/user/login" -Method Post -ContentType application/json -Headers @{'Device-Id'=$device} -Body (@{username='admin';password=$TestPassword;device_id=$device}|ConvertTo-Json -Compress)
if ($login.code -ne 200) { throw 'Test login failed' }
$headers=@{Token=$login.data.token;'Device-Id'=$device}
$private=(& $wg genkey).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Generate test key failed' }
$public=($private | & $wg pubkey).Trim()
$identity=Invoke-RestMethod "$base/v1/vpn/auto-enroll" -Method Post -ContentType application/json -Headers $headers -Body (@{pubkey=$public}|ConvertTo-Json -Compress)
if ($identity.code -ne 200 -or $identity.data.address -ne '172.1.0.4') { throw 'Unexpected test identity/address' }
$heartbeat=Invoke-RestMethod "$base/v1/vpn/heartbeat" -Method Post -ContentType application/json -Headers $headers -Body (@{device_id=$device;endpoint='';lan_endpoints=@('192.168.31.100:51823')}|ConvertTo-Json -Compress)
if ($heartbeat.code -ne 200) { throw 'Cannot publish test LAN candidate' }
# Create empty file, restrict ACL, only then write the generated test private key.
New-Item -ItemType File -Path $config | Out-Null
$acl=Get-Acl -LiteralPath $config
$acl.SetAccessRuleProtection($true,$false)
foreach($sid in @([Security.Principal.WindowsIdentity]::GetCurrent().User.Value,'S-1-5-18','S-1-5-32-544')) {
    $rule=[Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),'FullControl','Allow')
    $acl.AddAccessRule($rule)
}
Set-Acl -LiteralPath $config -AclObject $acl
[System.IO.File]::WriteAllText($config,"[Interface]`nPrivateKey = $private`nAddress = 172.1.0.4/32`nListenPort = 51823`nMTU = 1280`n")
$private=$null
foreach($name in @('windows-stop.txt','windows-ready.txt','windows-error.txt','windows-cleaned.txt')) {
    $path=Join-Path $directory $name
    if(Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
}
@{pubkey=$public;address='172.1.0.4'} | ConvertTo-Json -Compress | Set-Content -LiteralPath (Join-Path $directory 'windows-public.json')
$script=Join-Path $PSScriptRoot 'windows-peer-test.ps1'
Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',$script,'-TestDirectory',$directory) -Verb RunAs -WindowStyle Hidden
Write-Output 'Prepared isolated Windows test peer; awaiting owned service readiness (UAC may be shown).'
