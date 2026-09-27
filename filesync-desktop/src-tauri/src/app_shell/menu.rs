// app_shell/menu.rs
// 职责：顶部原生菜单栏。
//
// 菜单栏是唯一「任何界面都在」的入口——包括还没登录的登录页，所以节点切换放在这里：
// 打不开服务器的时候用户照样能换个节点再登录。
// 节点表会变（增删改、自动灾备切过去），所以菜单是每次重建的，不是建一次就完事。
use crate::logger;
use crate::net;

pub fn build_app_menu(app: &tauri::AppHandle) -> tauri::Result<tauri::menu::Menu<tauri::Wry>> {
    use tauri::menu::{CheckMenuItemBuilder, MenuBuilder, MenuItemBuilder, SubmenuBuilder};

    let open_logs = MenuItemBuilder::with_id("open_logs", "打开日志窗口")
        .accelerator("CmdOrCtrl+Alt+T")
        .build(app)?;
    let tools = SubmenuBuilder::new(app, "工具").item(&open_logs).build()?;

    let status = net::status();
    let mut network = SubmenuBuilder::new(app, "网络");
    for node in &status.nodes {
        // 标题带上最近一次探测结果，用户不点进设置也知道哪条路通
        let suffix = match status.health.iter().find(|h| h.id == node.id) {
            Some(h) if h.reachable => format!("（{} ms）", h.latency_ms),
            Some(h) if h.message.is_empty() => "（不可达）".to_string(),
            Some(h) => format!("（{}）", h.message),
            None => "（未检测）".to_string(),
        };
        let item = CheckMenuItemBuilder::with_id(
            format!("node:{}", node.id),
            format!("{} {}", node.name, suffix),
        )
        .checked(node.id == status.active_id)
        .build(app)?;
        network = network.item(&item);
    }
    let auto = CheckMenuItemBuilder::with_id("net_auto", "自动灾备切换")
        .checked(status.auto_failover)
        .build(app)?;
    let probe = MenuItemBuilder::with_id("net_probe", "立即检测所有节点").build(app)?;
    let settings = MenuItemBuilder::with_id("net_settings", "网络节点设置…").build(app)?;
    let network = network
        .separator()
        .item(&auto)
        .item(&probe)
        .separator()
        .item(&settings)
        .build()?;

    MenuBuilder::new(app).item(&tools).item(&network).build()
}

/// 节点表/健康状态变了之后重建菜单（勾选项、延迟数字都要跟着变）。
///
/// 菜单是原生控件，Windows 上必须在主线程上建和改，而调用方多半是 Tauri 命令或
/// 后台探测任务（都在别的线程），所以统一丢回主线程执行。
/// 失败只记日志：菜单没刷新不影响功能，不值得把命令整体报错。
pub fn refresh_app_menu(app: &tauri::AppHandle) {
    let handle = app.clone();
    let post = app.run_on_main_thread(move || match build_app_menu(&handle) {
        Ok(menu) => {
            if let Err(e) = handle.set_menu(menu) {
                logger::warn("menu", format!("刷新菜单失败: {}", e));
            }
        }
        Err(e) => logger::warn("menu", format!("构建菜单失败: {}", e)),
    });
    if let Err(e) = post {
        logger::warn("menu", format!("刷新菜单调度失败: {}", e));
    }
}
