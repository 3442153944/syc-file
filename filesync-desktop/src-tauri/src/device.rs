// device.rs
// 职责：生成并返回稳定的机器唯一标识（device_id）。
use sha2::{Digest, Sha256};

/// 机器名 + 用户名 → sha256 前 16 hex 字符，进程内稳定，重启不变
pub fn generate_device_id() -> String {
    let raw = format!("{}-{}", hostname(), username());
    let hash = Sha256::digest(raw.as_bytes());
    hex::encode(&hash[..8])
}

pub fn hostname() -> String {
    std::env::var("COMPUTERNAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .unwrap_or_else(|_| "unknown-host".into())
}

fn username() -> String {
    std::env::var("USERNAME")
        .or_else(|_| std::env::var("USER"))
        .unwrap_or_else(|_| "unknown-user".into())
}
