// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

/// Linux：把 appmenu-gtk-module 从 GTK_MODULES 里去掉，必须在 GTK 初始化之前（所以放在 main 最开头）。
///
/// Ubuntu 桌面会话默认设了 GTK_MODULES=gail:atk-bridge:appmenu-gtk-module。appmenu-gtk-module 是给
/// Unity 风格「全局菜单」用的，GNOME 上没有这个东西；在 Wayland 下它对窗口调用
/// gdk_wayland_window_set_dbus_properties_libgtk_only 会触发 `GDK_IS_WAYLAND_WINDOW` 断言失败，
/// 并与 GTK 菜单栏配合时在 GTK 内部无限递归直至栈溢出（崩溃栈全部落在 Window::set_menu 之下）。
/// 对本应用没有任何用处，直接禁用。其它模块（gail / atk-bridge 辅助功能）原样保留。
#[cfg(target_os = "linux")]
fn strip_appmenu_gtk_module() {
    const BAD: &str = "appmenu-gtk-module";
    if let Ok(v) = std::env::var("GTK_MODULES") {
        let parts: Vec<&str> = v.split(':').filter(|m| !m.is_empty()).collect();
        if parts.contains(&BAD) {
            let kept: Vec<&str> = parts.into_iter().filter(|m| *m != BAD).collect();
            // 此刻进程里还没有别的线程，改环境变量是安全的
            std::env::set_var("GTK_MODULES", kept.join(":"));
        }
    }
}

fn main() {
    #[cfg(target_os = "linux")]
    strip_appmenu_gtk_module();
    filesync_desktop_lib::run()
}
