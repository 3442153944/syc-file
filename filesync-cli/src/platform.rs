use anyhow::{bail, Context, Result};
use std::{
    fs,
    path::{Path, PathBuf},
    process::Command,
};

pub fn set_mode(path: &Path, mode: u32) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(mode))
    }
    #[cfg(not(unix))]
    {
        let _ = (path, mode);
        Ok(())
    }
}

#[cfg(windows)]
fn wireguard() -> Result<PathBuf> {
    let base = std::env::current_exe()?
        .parent()
        .context("CLI 路径无父目录")?
        .to_path_buf();
    [
        base.join("wireguard.exe"),
        base.join("wireguard/wireguard.exe"),
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../filesync-desktop/src-tauri/resources/wireguard/wireguard.exe"),
    ]
    .into_iter()
    .find(|p| p.is_file())
    .context("未找到自带 wireguard.exe，请将它放在 syc.exe 同目录或 wireguard/ 子目录")
}

#[cfg(windows)]
pub fn up(iface: &str, config: &str) -> Result<()> {
    validate_iface(iface)?;
    let dir =
        PathBuf::from(std::env::var("ProgramData").unwrap_or_else(|_| "C:\\ProgramData".into()))
            .join("syc");
    fs::create_dir_all(&dir)?;
    let path = dir.join(format!("{iface}.conf"));
    fs::write(&path, config)?;
    let output = Command::new(wireguard()?)
        .arg("/installtunnelservice")
        .arg(&path)
        .output()?;
    if !output.status.success() {
        bail!("安装隧道失败（需要管理员权限）: {}", decode(&output.stderr));
    }
    println!("隧道 {iface} 已安装");
    Ok(())
}

#[cfg(windows)]
pub fn down(iface: &str) -> Result<()> {
    validate_iface(iface)?;
    let output = Command::new(wireguard()?)
        .args(["/uninstalltunnelservice", iface])
        .output()?;
    if !output.status.success() {
        bail!("停止隧道失败（需要管理员权限）: {}", decode(&output.stderr));
    }
    println!("隧道 {iface} 已关闭");
    Ok(())
}

#[cfg(windows)]
fn validate_iface(iface: &str) -> Result<()> {
    if iface != "syc0" {
        bail!("Windows CLI 仅操作本软件的 syc0 隧道");
    }
    Ok(())
}

pub fn decode(bytes: &[u8]) -> String {
    #[cfg(windows)]
    {
        encoding_rs::GBK.decode(bytes).0.into_owned()
    }
    #[cfg(not(windows))]
    {
        String::from_utf8_lossy(bytes).into_owned()
    }
}
