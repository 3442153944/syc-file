// logger.rs
// 职责：全局日志。每条日志同时：
//   1) 通过 Tauri 事件 `app-log` 推给所有窗口（日志窗口实时显示）；
//   2) 追加写入 base/log/filesync.log（按大小轮转，对齐后端 lumberjack 策略）；
//   3) 可选输出到 stderr（开发期，默认关）。
//
// 级别过滤：低于 `current_level` 的日志直接丢弃。
// 轮转策略：filesync.log 达到 max_size MB → filesync.log.1 ← 旧的 .1 ← .2 … 删除第 max_backup+1 个。
//   与后端 lumberjack 一致（按大小而非按日期）。
use crate::app_paths;
use crate::config::LogConfig;
use parking_lot::Mutex;
use serde::Serialize;
use std::fs::{File, OpenOptions};
use std::io::Write;
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, AtomicU8, Ordering};
use std::sync::OnceLock;
use tauri::{AppHandle, Emitter};

static APP: OnceLock<AppHandle> = OnceLock::new();
static STATE: OnceLock<Mutex<Option<File>>> = OnceLock::new();
static LOG_PATH: OnceLock<PathBuf> = OnceLock::new();
static CURRENT_LEVEL: AtomicU8 = AtomicU8::new(Level::Info as u8);
static WRITE_FILE: AtomicU8 = AtomicU8::new(1);
static WRITE_CONSOLE: AtomicU8 = AtomicU8::new(0);
static MAX_BACKUP: AtomicU8 = AtomicU8::new(3);
static MAX_SIZE_BYTES: AtomicU64 = AtomicU64::new(100 * 1024 * 1024);

#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
#[repr(u8)]
enum Level {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

impl Level {
    fn from_str(s: &str) -> Option<Self> {
        match s.to_ascii_lowercase().as_str() {
            "debug" => Some(Level::Debug),
            "info" => Some(Level::Info),
            "warn" | "warning" => Some(Level::Warn),
            "error" => Some(Level::Error),
            _ => None,
        }
    }
    fn as_str(&self) -> &'static str {
        match self {
            Level::Debug => "DEBUG",
            Level::Info => "INFO",
            Level::Warn => "WARN",
            Level::Error => "ERROR",
        }
    }
    fn from_u8(v: u8) -> Self {
        match v {
            0 => Level::Debug,
            1 => Level::Info,
            2 => Level::Warn,
            3 => Level::Error,
            _ => Level::Info,
        }
    }
}

/// 推给前端的结构化日志行（camelCase 供 JS 直接用）。
#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
struct LogLine {
    ts: i64,
    level: String,
    source: String,
    message: String,
}

/// 在应用 setup 阶段调用：绑定 AppHandle、应用配置、打开/创建主日志文件。
pub fn init(app: AppHandle, cfg: &LogConfig) {
    let _ = APP.set(app);

    if let Some(lv) = Level::from_str(&cfg.level) {
        CURRENT_LEVEL.store(lv as u8, Ordering::Relaxed);
    }
    WRITE_FILE.store(if cfg.file { 1 } else { 0 }, Ordering::Relaxed);
    WRITE_CONSOLE.store(if cfg.console { 1 } else { 0 }, Ordering::Relaxed);
    MAX_BACKUP.store(cfg.max_backup.max(0) as u8, Ordering::Relaxed);
    MAX_SIZE_BYTES.store(cfg.max_size.saturating_mul(1024 * 1024), Ordering::Relaxed);

    let log_path = app_paths::log_dir().join("filesync.log");
    let _ = LOG_PATH.set(log_path.clone());

    let file = if cfg.file {
        OpenOptions::new()
            .create(true)
            .append(true)
            .open(&log_path)
            .ok()
    } else {
        None
    };
    let _ = STATE.set(Mutex::new(file));
}

fn log(level: Level, source: &str, message: &str) {
    // 级别过滤
    let current = Level::from_u8(CURRENT_LEVEL.load(Ordering::Relaxed));
    if (level as u8) < (current as u8) {
        return;
    }

    let now = chrono::Local::now();
    let ts_ms = now.timestamp_millis();
    let level_str = level.as_str();
    let line = format!(
        "{} [{:<5}] [{}] {}\n",
        now.format("%Y-%m-%d %H:%M:%S%.3f"),
        level_str,
        source,
        message
    );

    // 1) 写文件（同锁内完成「轮转检查 + 写」，避免竞争）
    if WRITE_FILE.load(Ordering::Relaxed) == 1 {
        if let Some(state) = STATE.get() {
            let mut guard = state.lock();
            // 轮转检查：当前文件大小超阈值则滚动
            let need_rotate = match guard.as_ref() {
                Some(f) => f
                    .metadata()
                    .map(|m| m.len() >= MAX_SIZE_BYTES.load(Ordering::Relaxed))
                    .unwrap_or(false),
                None => false,
            };
            if need_rotate {
                *guard = None; // drop 旧句柄，释放文件锁
                rotate_locked();
                // 重新打开主文件
                if let Some(p) = LOG_PATH.get() {
                    *guard = OpenOptions::new().create(true).append(true).open(p).ok();
                }
            }
            if let Some(f) = guard.as_mut() {
                let _ = f.write_all(line.as_bytes());
            }
        }
    }

    // 2) 控制台（开发期）
    if WRITE_CONSOLE.load(Ordering::Relaxed) == 1 {
        eprint!("{}", line);
    }

    // 3) 推事件（日志窗口监听 app-log）
    if let Some(app) = APP.get() {
        let _ = app.emit(
            "app-log",
            LogLine {
                ts: ts_ms,
                level: level_str.into(),
                source: source.into(),
                message: message.into(),
            },
        );
    }
}

/// 轮转：调用前必须已 drop 旧文件句柄。filesync.log → .1, .1 → .2, … 删除第 max_backup+1 个。
fn rotate_locked() {
    let path = match LOG_PATH.get() {
        Some(p) => p.clone(),
        None => return,
    };
    let max_backup = MAX_BACKUP.load(Ordering::Relaxed) as u32;
    if max_backup == 0 {
        // 不备份，直接截断主文件
        let _ = std::fs::remove_file(&path);
        return;
    }

    // 逆序重命名：.max → 删除, .max-1 → .max, ..., .1 → .2, filesync.log → .1
    // 先删第 max_backup 个（最老的备份）
    let oldest = backup_path(&path, max_backup);
    if oldest.exists() {
        let _ = std::fs::remove_file(&oldest);
    }
    // 从 max_backup-1 倒推到 1，依次 rename 到 +1
    for i in (1..max_backup).rev() {
        let from = backup_path(&path, i);
        let to = backup_path(&path, i + 1);
        if from.exists() {
            let _ = std::fs::rename(&from, &to);
        }
    }
    // 主文件 → .1
    if path.exists() {
        let to = backup_path(&path, 1);
        let _ = std::fs::rename(&path, &to);
    }
}

fn backup_path(base: &PathBuf, idx: u32) -> PathBuf {
    let mut p = base.clone();
    p.set_extension(format!("log.{}", idx));
    p
}

pub fn info<S: AsRef<str>>(source: &str, message: S) {
    log(Level::Info, source, message.as_ref());
}
pub fn warn<S: AsRef<str>>(source: &str, message: S) {
    log(Level::Warn, source, message.as_ref());
}
pub fn error<S: AsRef<str>>(source: &str, message: S) {
    log(Level::Error, source, message.as_ref());
}
pub fn debug<S: AsRef<str>>(source: &str, message: S) {
    log(Level::Debug, source, message.as_ref());
}
