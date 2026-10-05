// app_shell/tray.rs
// 职责：系统托盘图标 + 右键菜单。
use crate::app_shell::quick_paste::open_quick_paste_window;
use crate::app_shell::window::show_main_window;

/// 建托盘图标 + 右键菜单（显示主界面 / 快速分享 / 退出）。主窗口点 × 会销毁窗口但不退出进程
/// （见 Builder::on_window_event 与 run 里的 ExitRequested），左键托盘图标（Windows / macOS）
/// 或菜单里的「显示主界面」（所有平台）按需重建窗口。
/// 真正退出只有这里的「退出」菜单项会调 app.exit(0)。
pub fn setup_tray(app: &tauri::App) -> tauri::Result<()> {
    use tauri::menu::{MenuBuilder, MenuItemBuilder};
    use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};

    // 「显示主界面」必须有：Linux 下托盘的点击事件不会触发（tauri 文档：Linux 不支持
    // TrayIconEvent::Click，只有菜单可用），下面的「左键单击找回主窗口」在 Linux 上永远不会执行，
    // 关掉窗口后就没有任何办法从托盘把它叫回来。菜单项在所有平台都有效。
    let show_item = MenuItemBuilder::with_id("tray_show", "显示主界面").build(app)?;
    let quick_share_item = MenuItemBuilder::with_id("tray_quick_share", "快速分享").build(app)?;
    // Linux 不挂原生顶部菜单栏（见 lib.rs），「工具 → 日志窗口」「网络 → 节点设置」改从托盘进入
    let logs_item = MenuItemBuilder::with_id("tray_logs", "日志窗口").build(app)?;
    let net_item = MenuItemBuilder::with_id("tray_net_settings", "网络节点设置…").build(app)?;
    let quit_item = MenuItemBuilder::with_id("tray_quit", "退出").build(app)?;
    let tray_menu = MenuBuilder::new(app)
        .item(&show_item)
        .item(&quick_share_item)
        .separator()
        .item(&logs_item)
        .item(&net_item)
        .separator()
        .item(&quit_item)
        .build()?;

    TrayIconBuilder::new()
        .icon(app.default_window_icon().cloned().ok_or_else(|| {
            tauri::Error::AssetNotFound("缺少默认窗口图标，无法创建托盘图标".into())
        })?)
        .menu(&tray_menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id().as_ref() {
            "tray_quit" => app.exit(0),
            "tray_show" => {
                show_main_window(app);
            }
            "tray_quick_share" => open_quick_paste_window(app),
            "tray_logs" => crate::app_shell::log_window::open_log_window(app),
            "tray_net_settings" => crate::app_shell::window::open_network_settings(app),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            // 左键单击托盘图标：把主窗口找回来（右键菜单已经处理了唤起快速分享/退出）
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                // 主窗口关闭时已被销毁，这里负责按需重建
                show_main_window(tray.app_handle());
            }
        })
        .build(app)?;

    Ok(())
}
