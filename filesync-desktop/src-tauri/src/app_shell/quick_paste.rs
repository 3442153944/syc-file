// app_shell/quick_paste.rs
// 职责：粘贴快传——悬浮窗 + 全局快捷键。
use crate::config::SharedSyncConfig;
use crate::hotkey_codec;
use crate::logger;
use tauri::{Emitter, Manager, State, WebviewUrl, WebviewWindowBuilder};

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
        let _ = w.set_focus();
        return;
    }
    match WebviewWindowBuilder::new(app, "quick-paste", WebviewUrl::App("index.html".into()))
        .title("粘贴快传")
        .inner_size(380.0, 300.0)
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
/// 删临时文件。进度经 "quick-share-progress" 事件推给调用窗口。
/// 返回分享记录（与旧单发快传接口同一形状），前端直接当 CreateShareLinkData 用。
#[tauri::command]
pub async fn quick_share_upload(
    name: String,
    bytes: Vec<u8>,
    window: tauri::Window,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
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
        return Err(format!("写入临时文件失败: {}", e));
    }
    let result = run_quick_share(&tmp, &window, &config).await;
    if let Some(dir) = tmp.parent() {
        if let Err(e) = std::fs::remove_dir_all(dir) {
            logger::warn(
                "quick-share",
                format!("删除临时文件失败: {} ({})", dir.display(), e),
            );
        }
    }
    result
}

/// 从原始文件路径做粘贴快传（零拷贝路径）：剪贴板里是资源管理器复制的文件时，
/// Windows 剪贴板只保存路径引用（CF_HDROP）而 WebView2 给的 File 是它物化的临时副本——
/// 大文件读字节等于先整拷一遍再过 IPC 序列化，GB 级必然崩。本命令直接读原文件上传。
#[tauri::command]
pub async fn quick_share_upload_path(
    path: String,
    window: tauri::Window,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let p = std::path::PathBuf::from(&path);
    let meta = std::fs::metadata(&p).map_err(|e| format!("读取剪贴板文件失败: {e}"))?;
    if !meta.is_file() {
        return Err("剪贴板指向的不是普通文件".into());
    }
    if meta.len() == 0 {
        return Err("内容为空".into());
    }
    run_quick_share(&p, &window, &config).await
}

/// 快传公共执行段：多路分片上传（quick_share 模式），进度经事件推送。
async fn run_quick_share(
    file: &std::path::Path,
    window: &tauri::Window,
    config: &State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    use crate::chunked_uploader::{quick_share_upload as upload_inner, ProgressFn, UploadOptions};
    use std::sync::Arc;
    let (client, device_id) = {
        let cfg = config.read();
        (
            crate::commands::support::make_client(&cfg)?,
            cfg.device_id.clone(),
        )
    };
    let options = UploadOptions::new(device_id).with_quick_share();
    let window = window.clone();
    let on_progress: ProgressFn = Arc::new(move |sent, total| {
        let _ = window.emit(
            "quick-share-progress",
            serde_json::json!({ "sent": sent, "total": total }),
        );
    });
    let data = upload_inner(&client, file, "", &options, on_progress, config).await?;
    serde_json::to_value(&data).map_err(|e| format!("序列化分享结果失败: {}", e))
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
