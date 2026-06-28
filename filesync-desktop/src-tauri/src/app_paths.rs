// app_paths.rs
// 职责：统一计算桌面端的运行期目录。
// 基准目录 = 可执行文件所在目录；其下建 config/（含 config.yml、logs/）与 sync/。
use std::fs;
use std::path::PathBuf;

/// 可执行文件所在目录（开发期为 target/debug）。取不到时回退当前工作目录。
pub fn base_dir() -> PathBuf {
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from("."))
}

/// 配置目录 base/config，自动创建。
pub fn config_dir() -> PathBuf {
    let d = base_dir().join("config");
    let _ = fs::create_dir_all(&d);
    d
}

/// 默认同步根目录 base/sync，自动创建。
pub fn sync_dir() -> PathBuf {
    let d = base_dir().join("sync");
    let _ = fs::create_dir_all(&d);
    d
}

/// 配置文件 base/config/config.yml。
pub fn config_file() -> PathBuf {
    config_dir().join("config.yml")
}

/// 状态持久化文件（base-hash 等），base/config/state.json。
pub fn state_file() -> PathBuf {
    config_dir().join("state.json")
}

/// 日志目录 base/config/logs，自动创建。
pub fn log_dir() -> PathBuf {
    let d = config_dir().join("logs");
    let _ = fs::create_dir_all(&d);
    d
}

/// 当天日志文件 base/config/logs/filesync-YYYY-MM-DD.log。
pub fn log_file() -> PathBuf {
    let day = chrono::Local::now().format("%Y-%m-%d").to_string();
    log_dir().join(format!("filesync-{}.log", day))
}
