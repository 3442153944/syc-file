// app_shell/tray.rs
// 职责：系统托盘图标 + 右键菜单。
use crate::app_shell::quick_paste::open_quick_paste_window;
use crate::app_shell::window::show_main_window;

/// 建托盘图标 + 右键菜单（快速分享 / 退出）。主窗口点 × 会销毁窗口但不退出进程
/// （见 Builder::on_window_event 与 run 里的 ExitRequested），左键托盘图标按需重建窗口。
/// 真正退出只有这里的「退出」菜单项会调 app.exit(0)。
pub fn setup_tray(app: &tauri::App) -> tauri::Result<()> {
    use tauri::menu::{MenuBuilder, MenuItemBuilder};
    use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};

    let quick_share_item = MenuItemBuilder::with_id("tray_quick_share", "快速分享").build(app)?;
    let quit_item = MenuItemBuilder::with_id("tray_quit", "退出").build(app)?;
    let tray_menu = MenuBuilder::new(app)
        .item(&quick_share_item)
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
            "tray_quick_share" => open_quick_paste_window(app),
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
