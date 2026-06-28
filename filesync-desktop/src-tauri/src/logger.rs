// logger.rs
// 职责：全局日志。每条日志同时：
//   1) 通过 Tauri 事件 `app-log` 推给所有窗口（日志窗口实时显示）；
//   2) 追加写入 config/logs/ 下的当天日志文件。
// 通过全局 OnceLock 持有 AppHandle 与文件句柄，故任意模块可直接 logger::info(...) 而无需传参。
use crate::app_paths;
use parking_lot::Mutex;
use serde::Serialize;
use std::fs::{File, OpenOptions};
use std::io::Write;
use std::sync::OnceLock;
use tauri::{AppHandle, Emitter};

static APP: OnceLock<AppHandle> = OnceLock::new();
static FILE: OnceLock<Mutex<File>> = OnceLock::new();

/// 推给前端的结构化日志行（camelCase 供 JS 直接用）。
#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
struct LogLine {
    ts: i64,
    level: String,
    source: String,
    message: String,
}

/// 在应用 setup 阶段调用，绑定 AppHandle 并打开当天日志文件。
pub fn init(app: AppHandle) {
    let _ = APP.set(app);
    if let Ok(f) = OpenOptions::new()
        .create(true)
        .append(true)
        .open(app_paths::log_file())
    {
        let _ = FILE.set(Mutex::new(f));
    }
}

fn log(level: &str, source: &str, message: &str) {
    let now = chrono::Local::now();

    // 1) 写文件
    if let Some(f) = FILE.get() {
        let line = format!(
            "{} [{:<5}] [{}] {}\n",
            now.format("%Y-%m-%d %H:%M:%S%.3f"),
            level,
            source,
            message
        );
        let mut g = f.lock();
        let _ = g.write_all(line.as_bytes());
    }

    // 2) 推事件（日志窗口监听 app-log）
    if let Some(app) = APP.get() {
        let _ = app.emit(
            "app-log",
            LogLine {
                ts: now.timestamp_millis(),
                level: level.into(),
                source: source.into(),
                message: message.into(),
            },
        );
    }
}

pub fn info<S: AsRef<str>>(source: &str, message: S) {
    log("INFO", source, message.as_ref());
}
pub fn warn<S: AsRef<str>>(source: &str, message: S) {
    log("WARN", source, message.as_ref());
}
pub fn error<S: AsRef<str>>(source: &str, message: S) {
    log("ERROR", source, message.as_ref());
}
pub fn debug<S: AsRef<str>>(source: &str, message: S) {
    log("DEBUG", source, message.as_ref());
}
