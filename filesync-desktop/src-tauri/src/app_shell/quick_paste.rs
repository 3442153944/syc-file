// app_shell/quick_paste.rs
// 职责：粘贴快传——悬浮窗 + 全局快捷键。
use crate::config::SharedSyncConfig;
use crate::hotkey_codec;
use crate::logger;
use tauri::{Manager, State, WebviewUrl, WebviewWindowBuilder};
use std::collections::HashMap;
use std::sync::{Arc, OnceLock};
use parking_lot::Mutex;
use tokio::sync::watch;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Control { Running, Paused, Cancelled }
static CONTROLS: OnceLock<Mutex<HashMap<String, watch::Sender<Control>>>> = OnceLock::new();
fn controls() -> &'static Mutex<HashMap<String, watch::Sender<Control>>> {
    CONTROLS.get_or_init(|| Mutex::new(HashMap::new()))
}

/// 暂停时保留同一个 future / 服务端会话；取消时 drop future，不再产生新网络请求。
async fn controlled_upload<F: std::future::Future>(upload: F, mut rx: watch::Receiver<Control>) -> Option<F::Output> {
    tokio::pin!(upload);
    loop {
        let control = *rx.borrow_and_update();
        if control == Control::Cancelled { return None; }
        if control == Control::Paused {
            if rx.changed().await.is_err() { return None; }
            continue;
        }
        tokio::select! {
            biased;
            changed = rx.changed() => { if changed.is_err() { return None; } }
            result = &mut upload => { return Some(result); }
        }
    }
}

/// 打开（或聚焦）粘贴快传悬浮窗，label = "quick-paste"，渲染前端 QuickPaste.vue（见
/// App.vue 按 label 的硬分支，和 "logs" 窗口同款处理，不走 vue-router）。
///
/// 用带原生标题栏的普通窗口（decorations 保持默认 true），不是无边框弹窗：无边框窗口
/// 自己实现关闭/最小化/拖动这些事，windows/linux 各个窗口管理器表现还不一致，容易出
/// "点了没反应"这种问题；用系统原生标题栏，关闭/最小化/拖动全部是操作系统自己处理，
/// Windows 和 Linux（不管什么桌面环境/窗口管理器）都是一样的原生行为，不用自己糊一套。
/// 只保留置顶 + 不进任务栏——按下快捷键唤起，用完随手关掉，不是一个常驻的应用窗口。
/// 允许拖边框缩放（最大 800x600），但不给最大化按钮：这本来就是个小工具窗口，不需要
/// 占满屏幕，但拖大一点看长文件名/长链接还是有用的。
pub fn open_quick_paste_window(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("quick-paste") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
        return;
    }
    match WebviewWindowBuilder::new(app, "quick-paste", WebviewUrl::App("index.html".into()))
        .title("粘贴快传")
        .inner_size(520.0, 480.0)
        .resizable(true)
        .maximizable(false)
        .max_inner_size(800.0, 600.0)
        .always_on_top(true)
        .skip_taskbar(true)
        .center()
        .build()
    {
        Ok(_) => logger::info("app", "已打开粘贴快传悬浮窗"),
        Err(e) => logger::error("app", format!("打开粘贴快传悬浮窗失败: {}", e)),
    }
}

/// 注册（或替换）全局唤起快捷键：先注销掉之前可能注册过的，再注册新的。
/// 空字符串视为"不注册"（用户还没配置过，或者显式清空）。`hotkey` 是前端录制后编码出来的
/// 十六进制字符串（见 hotkey_codec.rs），不是 "Ctrl+Shift+V" 这种平台相关的文本。
pub fn register_quick_paste_shortcut(app: &tauri::AppHandle, hotkey: &str) -> Result<(), String> {
    use tauri_plugin_global_shortcut::GlobalShortcutExt;
    let gs = app.global_shortcut();
    let _ = gs.unregister_all();
    if hotkey.trim().is_empty() {
        return Ok(());
    }
    let shortcut = hotkey_codec::decode_hotkey(hotkey)?;
    gs.register(shortcut)
        .map_err(|e| format!("注册快捷键失败: {}", e))?;
    logger::info("app", format!("粘贴快传快捷键已注册: {}", hotkey));
    Ok(())
}

/// 保存快捷键（本地持久化）并立即让它生效，供用户中心设置页保存时调用，不用重启应用。
#[tauri::command]
pub fn set_quick_paste_hotkey(
    hotkey: String,
    app: tauri::AppHandle,
    config: State<SharedSyncConfig>,
) -> Result<(), String> {
    register_quick_paste_shortcut(&app, &hotkey)?;
    let mut cfg = config.write();
    cfg.quick_share_hotkey = Some(hotkey);
    cfg.save();
    Ok(())
}

/// 粘贴快传上传（Tauri 侧）：内容写临时文件 → 走多路分片上传核心（quick_share 模式，
/// h1.1 多连接 + DRR 调度，绕开 WebView2 h2 单连接 64KB 流控窗口的 ~880KB/s 限速）→
/// 删临时文件。立即返回任务 ID，执行与状态均由 Rust 持有，关窗不影响上传。
#[tauri::command]
pub async fn quick_share_upload(
    name: String,
    bytes: Vec<u8>,
    app: tauri::AppHandle,
    config: State<'_, SharedSyncConfig>,
) -> Result<String, String> {
    if bytes.is_empty() {
        return Err("内容为空".into());
    }
    // 临时文件放 <uuid 目录>/<原始文件名>：分片上传的 init 以路径末段作为展示/分享文件名，
    // 原始名（含扩展名）必须留住；uuid 目录保证同名并发不打架。
    let base = std::path::Path::new(&name)
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .filter(|n| !n.is_empty())
        .unwrap_or_else(|| "pasted-file".into());
    let tmp = std::env::temp_dir()
        .join(format!("syc-quick-share-{}", uuid::Uuid::new_v4()))
        .join(base);
    if let Err(e) = std::fs::create_dir_all(tmp.parent().expect("临时目录")) {
        return Err(format!("创建临时目录失败: {}", e));
    }
    if let Err(e) = std::fs::write(&tmp, &bytes) {
        let _ = std::fs::remove_dir_all(tmp.parent().expect("临时目录"));
        return Err(format!("写入临时文件失败: {}", e));
    }
    match start_quick_share(tmp.clone(), true, app, config.inner().clone()) {
        Ok(id) => Ok(id),
        Err(e) => {
            let _ = std::fs::remove_dir_all(tmp.parent().expect("临时目录"));
            Err(e)
        }
    }
}

/// 从原始文件路径做粘贴快传（零拷贝路径）：剪贴板里是资源管理器复制的文件时，
/// Windows 剪贴板只保存路径引用（CF_HDROP）而 WebView2 给的 File 是它物化的临时副本——
/// 大文件读字节等于先整拷一遍再过 IPC 序列化，GB 级必然崩。本命令直接读原文件上传。
#[tauri::command]
pub async fn quick_share_upload_path(
    path: String,
    app: tauri::AppHandle,
    config: State<'_, SharedSyncConfig>,
) -> Result<String, String> {
    let p = std::path::PathBuf::from(&path);
    let meta = std::fs::metadata(&p).map_err(|e| format!("读取剪贴板文件失败: {e}"))?;
    if !meta.is_file() {
        return Err("剪贴板指向的不是普通文件".into());
    }
    if meta.len() == 0 {
        return Err("内容为空".into());
    }
    start_quick_share(p, false, app, config.inner().clone())
}

/// 暂停停止派发新请求，已在途分片允许结束；取消丢弃整个上传 future（JoinSet 随之 abort）。
#[tauri::command]
pub fn control_quick_share(id: String, action: String) -> Result<(), String> {
    let state = match action.as_str() {
        "pause" => Control::Paused,
        "resume" => Control::Running,
        "cancel" => Control::Cancelled,
        _ => return Err("未知快传操作".into()),
    };
    let map = controls().lock();
    let sender = map.get(&id).ok_or("任务已结束")?;
    if *sender.borrow() == Control::Cancelled { return Err("任务已取消".into()); }
    sender.send(state).map_err(|_| "任务已结束".to_string())?;
    crate::transfers::quick_share_status(&id, match state {
        Control::Running => "uploading", Control::Paused => "paused", Control::Cancelled => "cancelled",
    });
    Ok(())
}

fn start_quick_share(
    file: std::path::PathBuf, temporary: bool, app: tauri::AppHandle, config: SharedSyncConfig,
) -> Result<String, String> {
    use crate::chunked_uploader::{quick_share_upload as upload_inner, ProgressFn, UploadOptions};
    let (client, device_id, base_url) = {
        let cfg = config.read();
        (
            crate::commands::support::make_client(&cfg)?,
            cfg.device_id.clone(),
            cfg.server_url.trim_end_matches('/').to_string(),
        )
    };
    let total = file.metadata().map_err(|e| e.to_string())?.len() as i64;
    let name = file.file_name().ok_or("无效文件名")?.to_string_lossy();
    let id = crate::transfers::upload_begin(&name, "quick-share", total, None);
    let (tx, rx) = watch::channel(Control::Running);
    controls().lock().insert(id.clone(), tx);
    let options = UploadOptions::new(device_id).with_quick_share();
    let progress_id = id.clone();
    let on_progress: ProgressFn = Arc::new(move |sent, total| {
        crate::transfers::upload_progress(&progress_id, sent, total);
    });
    let task_id = id.clone();
    tauri::async_runtime::spawn(async move {
        let result = controlled_upload(
            upload_inner(&client, &file, "", &options, on_progress, &config), rx,
        ).await; // 先 drop 上传 future，再清理临时文件。
        // 与控制命令串行提交终态，避免完成与取消竞态覆盖彼此。
        {
            let mut map = controls().lock();
            let cancelled = map.get(&task_id).map(|s| *s.borrow() == Control::Cancelled).unwrap_or(true);
            match result {
                Some(Ok(data)) if !cancelled => {
                    let url = format!("{}{}", base_url, data.url_path);
                    crate::transfers::quick_share_result(&task_id, url.clone());
                    let clipboard = app.state::<crate::clipboard_sync::SharedClipboardState>();
                    if let Err(e) = crate::clipboard_sync::apply_to_clipboard(&clipboard, &url) {
                        logger::warn("quick-share", e);
                    }
                    crate::transfers::upload_finish(&task_id, None);
                }
                Some(Err(e)) if !cancelled => crate::transfers::upload_finish(&task_id, Some(e)),
                _ => crate::transfers::quick_share_status(&task_id, "cancelled"),
            }
            map.remove(&task_id);
        }
        if temporary {
            if let Some(dir) = file.parent() {
                if let Err(e) = std::fs::remove_dir_all(dir) {
                    logger::warn("quick-share", format!("清理临时文件失败: {e}"));
                }
            }
        }
    });
    Ok(id)
}

/// 读取当前剪贴板的文件路径列表（Windows CF_HDROP）。剪贴板里是复制的文件时返回
/// 原始绝对路径（可含多个）；是图片/文本等内容时返回空数组。拿不到（非 Windows、
/// 剪贴板被占用）也返回空，调用方据此回退字节路径。
#[tauri::command]
pub fn clipboard_file_paths() -> Vec<String> {
    clipboard_paths_impl()
}

#[cfg(windows)]
fn clipboard_paths_impl() -> Vec<String> {
    use windows_sys::Win32::System::DataExchange::{
        CloseClipboard, GetClipboardData, OpenClipboard,
    };
    use windows_sys::Win32::UI::Shell::DragQueryFileW;
    const CF_HDROP: u32 = 15;
    unsafe {
        if OpenClipboard(std::ptr::null_mut()) == 0 {
            return vec![];
        }
        let hdrop = GetClipboardData(CF_HDROP);
        if hdrop.is_null() {
            CloseClipboard();
            return vec![];
        }
        let count = DragQueryFileW(hdrop as _, 0xFFFFFFFF, std::ptr::null_mut(), 0);
        let mut out = Vec::new();
        for i in 0..count {
            let len = DragQueryFileW(hdrop as _, i, std::ptr::null_mut(), 0);
            if len == 0 {
                continue;
            }
            let mut buf = vec![0u16; len as usize + 1];
            let got = DragQueryFileW(hdrop as _, i, buf.as_mut_ptr(), buf.len() as u32);
            if got > 0 {
                let s = String::from_utf16_lossy(&buf[..got as usize]);
                if !s.is_empty() {
                    out.push(s);
                }
            }
        }
        CloseClipboard();
        out
    }
}

#[cfg(not(windows))]
fn clipboard_paths_impl() -> Vec<String> {
    vec![]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn paused_upload_does_not_poll_until_resumed() {
        let (tx, rx) = watch::channel(Control::Paused);
        let (started_tx, started_rx) = tokio::sync::oneshot::channel();
        let task = tokio::spawn(controlled_upload(async move {
            let _ = started_tx.send(());
            42
        }, rx));
        tokio::task::yield_now().await;
        assert!(!task.is_finished());
        tx.send(Control::Running).unwrap();
        started_rx.await.unwrap();
        assert_eq!(task.await.unwrap(), Some(42));
    }

    #[tokio::test]
    async fn cancelling_paused_upload_drops_future_without_starting_it() {
        let (tx, rx) = watch::channel(Control::Paused);
        let (started_tx, started_rx) = tokio::sync::oneshot::channel::<()>();
        let task = tokio::spawn(controlled_upload(async move {
            let _ = started_tx.send(());
            std::future::pending::<()>().await
        }, rx));
        tx.send(Control::Cancelled).unwrap();
        assert_eq!(task.await.unwrap(), None);
        assert!(started_rx.await.is_err());
    }

    #[tokio::test]
    async fn cancelling_running_upload_aborts_owned_inflight_tasks() {
        let (tx, rx) = watch::channel(Control::Running);
        let (started_tx, started_rx) = tokio::sync::oneshot::channel();
        let (child_tx, child_rx) = tokio::sync::oneshot::channel::<()>();
        let task = tokio::spawn(controlled_upload(async move {
            let mut tasks = tokio::task::JoinSet::new();
            tasks.spawn(async move {
                let _guard = child_tx;
                std::future::pending::<()>().await;
            });
            let _ = started_tx.send(());
            std::future::pending::<()>().await;
            drop(tasks);
        }, rx));
        started_rx.await.unwrap();
        tx.send(Control::Cancelled).unwrap();
        assert_eq!(task.await.unwrap(), None);
        assert!(child_rx.await.is_err(), "JoinSet 中的请求必须随取消被 abort");
    }
}
