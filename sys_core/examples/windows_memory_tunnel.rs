//! Disposable Windows acceptance of the same bootstrap used by the desktop.
#[cfg(windows)]
fn main() -> Result<(), String> {
    use std::{
        io::Write,
        path::PathBuf,
        process::{Command, Stdio},
    };
    let executable = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../filesync-desktop/src-tauri/resources/wireguard/wireguard.exe");
    let wg = executable.with_file_name("wg.exe");
    let iface = "fs-memory-test";
    let status = Command::new("sc.exe")
        .args(["query", "WireGuardTunnel$fs-memory-test"])
        .output()
        .map_err(|e| e.to_string())?;
    if status.status.success() {
        return Err("Temporary test service already exists".into());
    }
    let key = Command::new(&wg)
        .arg("genkey")
        .output()
        .map_err(|e| e.to_string())?;
    if !key.status.success() {
        return Err("Test key generation failed".into());
    }
    let mut public = Command::new(&wg)
        .arg("pubkey")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .map_err(|e| e.to_string())?;
    public
        .stdin
        .take()
        .unwrap()
        .write_all(&key.stdout)
        .map_err(|e| e.to_string())?;
    let public = public.wait_with_output().map_err(|e| e.to_string())?;
    if !public.status.success() {
        return Err("Test public key generation failed".into());
    }
    let public = String::from_utf8_lossy(&public.stdout).trim().to_owned();
    let config = format!(
        "[Interface]\nPrivateKey = {}\nAddress = 172.1.240.2/32\nListenPort = 51827\n",
        String::from_utf8_lossy(&key.stdout).trim()
    );
    let result = (|| {
        sys_core::windows_tunnel::install_memory_tunnel(&executable, iface, &public, &config)?;
        let peer_file = std::env::temp_dir().join("filesync-public-peer-memory-test.conf");
        // The public-only file contains no private key and is removed below.
        std::fs::write(&peer_file, "[Interface]\n[Peer]\nPublicKey = CQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAllowedIPs = 172.1.240.3/32\n")
            .map_err(|e| e.to_string())?;
        let update = Command::new(&wg)
            .args(["addconf", iface])
            .arg(&peer_file)
            .output();
        let _ = std::fs::remove_file(peer_file);
        if !update.map_err(|e| e.to_string())?.status.success() {
            return Err("Public peer update failed".into());
        }
        let identity = Command::new(&wg)
            .args(["show", iface, "public-key"])
            .output()
            .map_err(|e| e.to_string())?;
        let port = Command::new(&wg)
            .args(["show", iface, "listen-port"])
            .output()
            .map_err(|e| e.to_string())?;
        if String::from_utf8_lossy(&identity.stdout).trim() != public
            || String::from_utf8_lossy(&port.stdout).trim() != "51827"
        {
            return Err("Peer update changed interface identity or listen port".into());
        }
        println!("PASS: shared sys_core memory bootstrap and peer update preserve public identity/port; no key file");
        Ok(())
    })();
    let _ = Command::new(executable)
        .args(["/uninstalltunnelservice", iface])
        .output();
    result
}

#[cfg(not(windows))]
fn main() {
    eprintln!("Windows-only disposable acceptance");
}
