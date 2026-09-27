// app_shell/log_window.rs
// 职责：独立日志窗口——打开/聚焦、前端日志转发、补齐历史日志。
use crate::logger;
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

/// 打开（或聚焦）专用日志窗口，label = "logs"，渲染前端 LogViewer。
pub fn open_log_window(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("logs") {
        let _ = w.set_focus();
        return;
    }
    match WebviewWindowBuilder::new(app, "logs", WebviewUrl::App("index.html".into()))
        .title("云梯 - 日志")
        .inner_size(960.0, 560.0)
        .build()
    {
        Ok(_) => logger::info("app", "已打开日志窗口"),
        Err(e) => logger::error("app", format!("打开日志窗口失败: {}", e)),
    }
}

#[tauri::command]
pub fn open_log_window_cmd(app: tauri::AppHandle) {
    open_log_window(&app);
}

/// 前端 console.* 批量转发进统一日志（写文件 + 推日志窗口）。见前端 utils/consoleBridge.ts。
#[tauri::command]
pub fn log_from_frontend(entries: Vec<logger::FrontendLog>) {
    for e in entries {
        logger::frontend(e);
    }
}

/// 日志窗口打开时调用：取内存里最近的日志补齐，否则窗口打开前的日志一条都看不到。
#[tauri::command]
pub fn get_recent_logs() -> Vec<logger::LogLine> {
    logger::recent()
}
