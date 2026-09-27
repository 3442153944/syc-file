// app_shell/quick_paste.rs
// 职责：粘贴快传——悬浮窗 + 全局快捷键。
use crate::config::SharedSyncConfig;
use crate::hotkey_codec;
use crate::logger;
use tauri::{Manager, State, WebviewUrl, WebviewWindowBuilder};

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
