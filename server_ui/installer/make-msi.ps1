#Requires -Version 5.1
<#
.SYNOPSIS
  把 server_ui 的 dist 目录打成单个 MSI 安装包（Windows Installer）。

.DESCRIPTION
  用 WiX 5 生成 MSI：安装向导可选安装路径，msiexec 负责释放 exe + Qt 依赖、
  创建开始菜单快捷方式、HKCU 登录自启项，并注册「应用和功能」卸载信息。
  相比自解压 exe，msiexec 是系统信任的安装服务，安全软件（如火绒）不会拦截
  写注册表/开始菜单的操作。

  由 build-win.ps1 -Installer 调用；也可单独使用。

.PARAMETER SourceDir
  已用 windeployqt 打好运行库的目录（必须包含 syc-file-gui.exe）。

.PARAMETER Version
  三段版本号，如 0.1.0（MSI 内部会补成 0.1.0.0）。

.PARAMETER OutFile
  输出 .msi 路径。

.PARAMETER IconFile
  应用图标（默认 resources\appicon.ico）。

.PARAMETER Culture
  安装向导语言，默认 zh-CN。

.EXAMPLE
  .\installer\make-msi.ps1 -SourceDir .\dist -Version 0.1.0 -OutFile .\release\app.msi
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SourceDir,
    [string]$Version = '0.1.0',
    [Parameter(Mandatory = $true)][string]$OutFile,
    [string]$IconFile = '',
    [string]$Culture = 'zh-CN'
)

$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$root = Split-Path -Parent $here

function Step([string]$text) {
    Write-Host ''
    Write-Host "==> $text" -ForegroundColor Cyan
}

function Fail([string]$text) {
    Write-Host "错误: $text" -ForegroundColor Red
    exit 1
}

function Escape-Xml([string]$text) {
    return [System.Security.SecurityElement]::Escape($text)
}

$SourceDir = [System.IO.Path]::GetFullPath($SourceDir)
$exe = Join-Path $SourceDir 'syc-file-gui.exe'
if (-not (Test-Path $exe)) { Fail "SourceDir 里没有 syc-file-gui.exe: $SourceDir" }

if (-not $IconFile) { $IconFile = Join-Path $root 'resources\appicon.ico' }
if (-not (Test-Path $IconFile)) { Fail "找不到图标: $IconFile" }

if ($Version -notmatch '^\d+\.\d+\.\d+$') { Fail "版本号必须是三段: $Version" }
$version4 = "$Version.0"

# ---------- 找 WiX ----------
$wix = Join-Path $env:USERPROFILE '.dotnet\tools\wix.exe'
if (-not (Test-Path $wix)) {
    $cmd = Get-Command wix.exe -ErrorAction SilentlyContinue
    if ($cmd) { $wix = $cmd.Source }
}
if (-not (Test-Path $wix)) {
    Fail @'
找不到 wix（WiX Toolset 5）。先执行：
  dotnet tool install --global wix --version 5.*
  wix extension add -g WixToolset.UI.wixext/5.0.2
'@
}
$wixVersion = (& $wix --version 2>&1 | Select-Object -First 1)
if ($wixVersion -notmatch '^5\.') {
    Write-Host "警告: 当前 wix 是 $wixVersion，本脚本按 WiX 5 验证（v6/v7 需要接受 OSMF EULA）" -ForegroundColor Yellow
}

$extensions = (& $wix extension list -g 2>&1 | Out-String)
if ($extensions -notmatch 'WixToolset\.UI\.wixext\s+5\.') {
    Step '安装 WiX UI 扩展（5.0.2）'
    & $wix extension add -g 'WixToolset.UI.wixext/5.0.2'
    if ($LASTEXITCODE -ne 0) { Fail '安装 WiX UI 扩展失败' }
}

# ---------- 生成中文许可页 RTF ----------
$licenseText = @'
私有云服务器设置 软件许可与使用说明

本软件按“现状”提供，不附带任何明示或暗示的担保。
请在遵守当地法律法规的前提下使用本软件，使用产生的数据与后果由使用者自行承担。

本安装程序只会在你选择的目录中释放程序文件，并创建开始菜单快捷方式与登录自启项；
卸载时只删除这些由安装程序释放的内容，不会删除同目录下的其他文件（例如后端配置与日志）。

点击“我接受”继续安装。
'@

function ConvertTo-Rtf([string]$text) {
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append('{\rtf1\ansi\deff0{\fonttbl{\f0 Microsoft YaHei;}}\viewkind4\uc1\pard\f0\fs20 ')
    foreach ($ch in $text.ToCharArray()) {
        $code = [int]$ch
        if ($ch -eq "`r") { continue }
        if ($ch -eq "`n") { [void]$sb.Append('\par '); continue }
        if ($code -ge 32 -and $code -lt 127) {
            if ($ch -eq '\' -or $ch -eq '{' -or $ch -eq '}') {
                [void]$sb.Append('\')
                [void]$sb.Append($ch)
            } else {
                [void]$sb.Append($ch)
            }
        } else {
            $u = $code
            if ($u -gt 32767) { $u -= 65536 }
            [void]$sb.Append("\u$u?")
        }
    }
    [void]$sb.Append('}')
    return $sb.ToString()
}

$work = Join-Path $root 'build\installer\wix-build'
if (Test-Path $work) { Remove-Item $work -Recurse -Force }
New-Item -ItemType Directory -Force -Path $work | Out-Null
$licensePath = Join-Path $work 'license.rtf'
[System.IO.File]::WriteAllText($licensePath, (ConvertTo-Rtf $licenseText), (New-Object System.Text.ASCIIEncoding))

# ---------- 目录 / 组件 ----------
$files = Get-ChildItem $SourceDir -Recurse -File | Sort-Object FullName
$byDir = @{}
foreach ($f in $files) {
    $rel = $f.DirectoryName.Substring($SourceDir.Length).TrimStart([System.IO.Path]::DirectorySeparatorChar)
    if (-not $byDir.ContainsKey($rel)) { $byDir[$rel] = New-Object System.Collections.Generic.List[System.IO.FileInfo] }
    $byDir[$rel].Add($f)
}

$allDirs = @('')
$allDirs += ($byDir.Keys | Where-Object { $_ -ne '' } | Sort-Object)
$dirIds = @{ '' = 'INSTALLFOLDER' }
$dirIndex = 0
foreach ($d in ($allDirs | Where-Object { $_ -ne '' })) {
    $dirIndex++
    $dirIds[$d] = "D$dirIndex"
}

function Get-ChildDirs([string]$parent) {
    $result = @()
    foreach ($d in $allDirs) {
        if ($d -eq '') { continue }
        $p = Split-Path -Parent $d
        if ($null -eq $p) { $p = '' }
        if ($p -eq $parent) { $result += $d }
    }
    return $result
}

$dirXml = New-Object System.Text.StringBuilder
function Add-DirXml([string]$parent, [string]$indent) {
    foreach ($child in (Get-ChildDirs $parent)) {
        $name = Split-Path -Leaf $child
        $id = $dirIds[$child]
        [void]$dirXml.AppendLine("$indent<Directory Id=`"$id`" Name=`"$(Escape-Xml $name)`">")
        Add-DirXml $child ($indent + '  ')
        [void]$dirXml.AppendLine("$indent</Directory>")
    }
}
Add-DirXml '' '          '

$groups = New-Object System.Collections.Generic.List[string]
$refs = New-Object System.Collections.Generic.List[string]
$componentIndex = 0
foreach ($rel in $allDirs) {
    if (-not $byDir.ContainsKey($rel)) { continue }
    $dirId = $dirIds[$rel]
    $groupId = if ($rel -eq '') { 'CG_Root' } else { 'CG_' + $dirId }
    [void]$groups.Add("    <ComponentGroup Id=`"$groupId`" Directory=`"$dirId`">")
    foreach ($f in $byDir[$rel]) {
        $componentIndex++
        $relFile = $f.FullName.Substring($SourceDir.Length).TrimStart([System.IO.Path]::DirectorySeparatorChar)
        [void]$groups.Add("      <Component Id=`"C$componentIndex`" Guid=`"*`">")
        [void]$groups.Add("        <File Id=`"F$componentIndex`" Source=`"`$(var.SourceDir)\$(Escape-Xml $relFile)`" />")
        [void]$groups.Add('      </Component>')
    }
    [void]$groups.Add('    </ComponentGroup>')
    [void]$refs.Add("      <ComponentGroupRef Id=`"$groupId`" />")
}

# 卸载时删除开始菜单空目录；Run 键随组件卸载自动删除
[void]$groups.Add(@'
    <ComponentGroup Id="CG_Shortcuts" Directory="INSTALLFOLDER">
      <Component Id="C_Shortcuts" Guid="*">
        <Shortcut Id="StartMenuShortcut" Directory="AppProgramMenuDir" Name="私有云服务器设置"
                  Target="[INSTALLFOLDER]syc-file-gui.exe" WorkingDirectory="INSTALLFOLDER" Advertise="no" />
        <RemoveFolder Id="RemoveAppProgramMenuDir" Directory="AppProgramMenuDir" On="uninstall" />
        <RegistryValue Root="HKCU" Key="Software\Microsoft\Windows\CurrentVersion\Run"
                       Name="私有云服务器设置" Type="string"
                       Value="&quot;[INSTALLFOLDER]syc-file-gui.exe&quot;" KeyPath="yes" />
      </Component>
    </ComponentGroup>
'@)
[void]$refs.Add('      <ComponentGroupRef Id="CG_Shortcuts" />')

# 固定 UpgradeCode：升级安装能识别旧版本（不要改）
$upgradeCode = '7E4C2F6A-9B3D-4E8A-A1C5-2D6F8B0E4F17'

$wxs = @"
<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs"
     xmlns:ui="http://wixtoolset.org/schemas/v4/wxs/ui">
  <Package Name="私有云服务器设置"
           Manufacturer="FileSync"
           Version="$version4"
           Language="2052"
           UpgradeCode="$upgradeCode"
           Scope="perUser"
           Compressed="yes">

    <MajorUpgrade DowngradeErrorMessage="已安装更高版本，请先在“应用和功能”中卸载。" />
    <MediaTemplate EmbedCab="yes" />

    <Icon Id="AppIcon" SourceFile="`$(var.IconFile)" />
    <Property Id="ARPPRODUCTICON" Value="AppIcon" />
    <Property Id="ARPNOREPAIR" Value="1" />
    <Property Id="ALLUSERS" Value="2" />
    <Property Id="MSIINSTALLPERUSER" Value="1" />
    <Property Id="WIXUI_INSTALLDIR" Value="INSTALLFOLDER" />
    <WixVariable Id="WixUILicenseRtf" Value="`$(var.LicenseRtf)" />
    <ui:WixUI Id="WixUI_InstallDir" />

    <StandardDirectory Id="LocalAppDataFolder">
      <Directory Id="ProgramsFolder" Name="Programs">
        <Directory Id="INSTALLFOLDER" Name="私有云服务器设置">
$($dirXml.ToString())        </Directory>
      </Directory>
    </StandardDirectory>
    <StandardDirectory Id="ProgramMenuFolder">
      <Directory Id="AppProgramMenuDir" Name="私有云服务器设置" />
    </StandardDirectory>

$($groups -join "`r`n")

    <Feature Id="Main" Title="私有云服务器设置" Level="1">
$($refs -join "`r`n")
    </Feature>
  </Package>
</Wix>
"@

$wxsPath = Join-Path $work 'app.wxs'
[System.IO.File]::WriteAllText($wxsPath, $wxs, (New-Object System.Text.UTF8Encoding($false)))

# ---------- 编译 ----------
$outFull = [System.IO.Path]::GetFullPath($OutFile)
$outDir = Split-Path -Parent $outFull
if (-not (Test-Path $outDir)) { New-Item -ItemType Directory -Force -Path $outDir | Out-Null }
if (Test-Path $outFull) { Remove-Item $outFull -Force }

Step "编译 MSI（WiX $wixVersion, culture $Culture）"
& $wix build $wxsPath `
    -ext WixToolset.UI.wixext `
    -culture $Culture `
    -arch x64 `
    -d "SourceDir=$SourceDir" `
    -d "IconFile=$IconFile" `
    -d "LicenseRtf=$licensePath" `
    -pdb (Join-Path $work 'app.wixpdb') `
    -o $outFull
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $outFull)) {
    Fail "WiX 编译失败（exit=$LASTEXITCODE）"
}

$size = [math]::Round((Get-Item $outFull).Length / 1MB, 1)
Write-Host "  MSI: $outFull（$size MB）" -ForegroundColor Green
Write-Host "  安装: 双击后可选安装路径；msiexec /i `"$outFull`" INSTALLDIR=<目录>`n" -ForegroundColor DarkGray