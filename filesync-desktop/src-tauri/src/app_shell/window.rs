// app_shell/window.rs
// 职责：主窗口生命周期。
//
// 主窗口点 × 会真正销毁 WebView，而不是隐藏：同步工具绝大部分时间挂在托盘里，
// 隐藏的 WebView2（主控 + GPU + 渲染进程）照样占着几百 MB。同步引擎、WS、文件监听、
// 全局快捷键都在 Rust 里，不依赖窗口；传输进度也已收进 transfers.rs。需要时再重建窗口。
//
// 唯一的例外是**在 WebView 里跑的上传**（粘贴快传走的是页面里的 XHR）：此时销毁会把
// 上传一起掐断。所以关窗时若还有这种上传，先隐藏，等它们跑完再自动销毁。
use crate::app_paths;
use crate::logger;
use crate::transfers;
use serde::{Deserialize, Serialize};
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;
use tauri::{Emitter, LogicalPosition, LogicalSize, Manager, WebviewWindowBuilder};

/// 关窗时因为有 WebView 内上传而延后销毁的标记
pub static DESTROY_MAIN_WHEN_IDLE: AtomicBool = AtomicBool::new(false);

/// 主窗口关闭时缓存的位置/大小（逻辑像素，与 tauri.conf.json 的 width/height 同一单位）。
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
struct WindowGeometry {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

fn load_geometry() -> Option<WindowGeometry> {
    let text = std::fs::read_to_string(app_paths::window_state_file()).ok()?;
    serde_json::from_str(&text).ok()
}

/// 关窗前记录当前位置/大小，供下次重建/启动时复原。最小化时报出来的尺寸不可靠，跳过
/// （维持上一次缓存的值，好过被最小化状态污染）。
pub fn save_geometry(window: &tauri::Window) {
    if window.is_minimized().unwrap_or(false) {
        return;
    }
    let Ok(scale) = window.scale_factor() else { return };
    let Ok(pos) = window.outer_position() else { return };
    let Ok(size) = window.inner_size() else { return };
    if size.width == 0 || size.height == 0 {
        return;
    }
    let logical_pos = pos.to_logical::<f64>(scale);
    let logical_size = size.to_logical::<f64>(scale);
    let geo = WindowGeometry {
        x: logical_pos.x,
        y: logical_pos.y,
        width: logical_size.width,
        height: logical_size.height,
    };
    if let Ok(text) = serde_json::to_string(&geo) {
        let _ = std::fs::write(app_paths::window_state_file(), text);
    }
}

/// 给已经建好的窗口（启动时由 tauri.conf.json 自动建出的主窗口）补上缓存的位置/大小。
pub fn apply_saved_geometry(window: &tauri::WebviewWindow) {
    let Some(geo) = load_geometry() else { return };
    let _ = window.set_position(LogicalPosition::new(geo.x, geo.y));
    let _ = window.set_size(LogicalSize::new(geo.width, geo.height));
}

/// 「网络节点设置…」要在一个刚新建的主窗口里弹出时，事件发过去页面还没加载好会丢，
/// 改为留个标记，页面挂载后自己来取（见 take_pending_network_settings）。
static PENDING_NETWORK_SETTINGS: AtomicBool = AtomicBool::new(false);

/// 找回主窗口：存在就显示并聚焦，已被销毁就按 tauri.conf 里的配置重建。
/// 返回 (窗口, 是否新建)。
pub fn show_main_window(app: &tauri::AppHandle) -> Option<(tauri::WebviewWindow, bool)> {
    // 用户把窗口叫回来了，之前"等上传结束再销毁"的计划作废
    DESTROY_MAIN_WHEN_IDLE.store(false, Ordering::SeqCst);

    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
        return Some((w, false));
    }

    // tauri.conf.json 的 app.windows[0] 就是主窗口（未写 label 时默认 "main"），
    // 按同一份配置重建，尺寸/标题等与首次启动一致
    let cfg = app
        .config()
        .app
        .windows
        .iter()
        .find(|w| w.label == "main")
        .cloned();
    let Some(cfg) = cfg else {
        logger::error("app", "tauri.conf.json 中找不到 label 为 main 的窗口配置，无法重建主窗口");
        return None;
    };
    let builder = match WebviewWindowBuilder::from_config(app, &cfg) {
        Ok(b) => b,
        Err(e) => {
            logger::error("app", format!("重建主窗口失败: {}", e));
            return None;
        }
    };
    // 有上次关闭时缓存的位置/大小就用它，覆盖 tauri.conf.json 里写死的默认值
    let builder = match load_geometry() {
        Some(geo) => builder
            .position(geo.x, geo.y)
            .inner_size(geo.width, geo.height),
        None => builder,
    };
    match builder.build() {
        Ok(w) => {
            let _ = w.set_focus();
            logger::info("app", "主窗口已重建");
            Some((w, true))
        }
        Err(e) => {
            logger::error("app", format!("重建主窗口失败: {}", e));
            None
        }
    }
}

/// 若主窗口已隐藏且在等待上传结束，而现在已经没有依赖它的上传了，就销毁它。
pub fn destroy_main_if_idle(app: &tauri::AppHandle) {
    if !DESTROY_MAIN_WHEN_IDLE.load(Ordering::SeqCst) || transfers::has_active_uploads_owned_by("main") {
        return;
    }
    let app = app.clone();
    // 稍等再销毁：这一步通常发生在前端 transfer_upload_finish 调用里，
    // 让这次 invoke 的响应先回到页面，避免页面收尾逻辑执行到一半
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(Duration::from_millis(500)).await;
        if !DESTROY_MAIN_WHEN_IDLE.swap(false, Ordering::SeqCst) {
            return; // 期间用户又把窗口叫回来了
        }
        if let Some(w) = app.get_webview_window("main") {
            if !w.is_visible().unwrap_or(true) {
                logger::info("app", "WebView 内的上传已结束，销毁已隐藏的主窗口");
                let _ = w.destroy();
            }
        }
    });
}

/// 菜单里点「网络节点设置…」：把主窗口叫出来并让前端弹出设置面板。
pub fn open_network_settings(app: &tauri::AppHandle) {
    match show_main_window(app) {
        // 窗口本来就在：页面已加载，直接发事件
        Some((w, false)) => {
            let _ = w.emit("open-network-settings", ());
        }
        // 刚新建：页面还没起来，事件会丢，留标记让页面挂载后来取
        Some((_, true)) => {
            PENDING_NETWORK_SETTINGS.store(true, Ordering::SeqCst);
        }
        None => {}
    }
}

/// 页面挂载时调用：是否有待打开的网络节点设置面板（取完即清）。
#[tauri::command]
pub fn take_pending_network_settings() -> bool {
    PENDING_NETWORK_SETTINGS.swap(false, Ordering::SeqCst)
}
