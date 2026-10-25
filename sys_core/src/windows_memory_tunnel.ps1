# Configuration arrives on stdin. Never write it to a file, environment or log.
$ErrorActionPreference='Stop'
[Console]::InputEncoding=[System.Text.UTF8Encoding]::new($false)
$wireguard=$env:FILESYNC_MEMORY_WG_EXE
$iface=$env:FILESYNC_MEMORY_IFACE
$expected=$env:FILESYNC_MEMORY_PUBLIC_KEY
$wg=Join-Path (Split-Path -Parent $wireguard) 'wg.exe'
$service='WireGuardTunnel$'+$iface
$pipe=$null
$owned=$false
$stage='prepare'
try {
    if(Get-Service $service -ErrorAction SilentlyContinue){throw 'Tunnel already exists'}
    $config=[Console]::In.ReadToEnd()
    $name='filesync-memory-'+[guid]::NewGuid().ToString('N')+'\'+$iface+'.conf'
    $security=[System.IO.Pipes.PipeSecurity]::new()
    $security.SetAccessRuleProtection($true,$false)
    foreach($sid in @('S-1-5-18','S-1-5-32-544',[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value)) {
        $security.AddAccessRule([System.IO.Pipes.PipeAccessRule]::new([System.Security.Principal.SecurityIdentifier]::new($sid),[System.IO.Pipes.PipeAccessRights]::FullControl,[System.Security.AccessControl.AccessControlType]::Allow))
    }
    $pipe=[System.IO.Pipes.NamedPipeServerStream]::new($name,[System.IO.Pipes.PipeDirection]::Out,1,[System.IO.Pipes.PipeTransmissionMode]::Byte,[System.IO.Pipes.PipeOptions]::Asynchronous,65536,65536,$security)
    $connected=$pipe.WaitForConnectionAsync()
    $stage='install'
    $install=Start-Process -FilePath $wireguard -ArgumentList @('/installtunnelservice',('\\.\pipe\'+$name)) -PassThru -WindowStyle Hidden
    $owned=$true
    if(!$connected.Wait(20000)){throw 'Service did not connect to memory pipe'}
    $bytes=[System.Text.Encoding]::UTF8.GetBytes($config)
    $config=$null
    $pipe.Write($bytes,0,$bytes.Length)
    $pipe.Dispose(); $pipe=$null; [Array]::Clear($bytes,0,$bytes.Length)
    if(!$install.WaitForExit(20000)){throw 'Tunnel installer timed out'}
    if($install.ExitCode -ne 0){throw 'Tunnel installation failed'}
    $stage='service-mode'
    & sc.exe config $service start= demand | Out-Null
    if($LASTEXITCODE -ne 0){throw 'Cannot disable unattended boot of ephemeral identity'}
    $stage='verify-public-identity'
    $ready=$false
    for($i=0;$i -lt 40;$i++) {
        $ErrorActionPreference='Continue'
        $actual=(& $wg show $iface public-key 2>$null)
        $ErrorActionPreference='Stop'
        if($actual -and $actual.Trim() -eq $expected){$ready=$true;break}
        Start-Sleep -Milliseconds 250
    }
    if(!$ready){throw 'Tunnel public identity mismatch'}
    Write-Output 'Memory tunnel ready'
} catch {
    # Only public stage names; exceptions from external parsers could echo keys.
    if($owned){Start-Process -FilePath $wireguard -ArgumentList @('/uninstalltunnelservice',$iface) -WindowStyle Hidden -Wait}
    [Console]::Error.WriteLine('Memory tunnel failed at '+$stage)
    exit 1
} finally {
    if($pipe){$pipe.Dispose()}
    $config=$null
}
