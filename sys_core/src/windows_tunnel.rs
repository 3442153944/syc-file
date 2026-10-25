//! Windows service bootstrap from a restricted, one-shot memory pipe.
//! The service command line contains only a pipe name; no secret file exists.
use std::{
    io::Write,
    path::Path,
    process::{Command, Stdio},
};

pub fn install_memory_tunnel(
    executable: &Path,
    interface: &str,
    public_key: &str,
    configuration: &str,
) -> Result<(), String> {
    if interface.is_empty()
        || interface.len() > 15
        || !interface
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    {
        return Err("Invalid tunnel interface".into());
    }
    if public_key.len() != 44
        || !public_key
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"+/=".contains(&b))
    {
        return Err("Invalid tunnel public key".into());
    }
    use std::os::windows::process::CommandExt;
    let powershell = std::env::var_os("SystemRoot")
        .map(std::path::PathBuf::from)
        .ok_or("Missing SystemRoot")?
        .join("System32/WindowsPowerShell/v1.0/powershell.exe");
    let mut child = Command::new(powershell)
        .args([
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            include_str!("windows_memory_tunnel.ps1"),
        ])
        .env("FILESYNC_MEMORY_WG_EXE", executable)
        .env("FILESYNC_MEMORY_IFACE", interface)
        .env("FILESYNC_MEMORY_PUBLIC_KEY", public_key)
        .creation_flags(0x0800_0000)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| e.to_string())?;
    child
        .stdin
        .take()
        .ok_or("Missing config pipe")?
        .write_all(configuration.as_bytes())
        .map_err(|_| "Unable to send memory configuration")?;
    let result = child.wait_with_output().map_err(|e| e.to_string())?;
    if !result.status.success() {
        return Err(format!(
            "Windows 内存隧道启动失败（需要管理员权限）：{}",
            String::from_utf8_lossy(&result.stderr).trim()
        ));
    }
    Ok(())
}
