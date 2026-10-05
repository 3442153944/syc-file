mod api;
mod app_paths;
mod app_shell;
mod base_store;
mod catch_up;
mod chunked_uploader;
mod clipboard_sync;
mod commands;
mod config;
mod device;
mod hotkey_codec;
mod logger;
mod net;
mod sync_engine;
mod transfers;
mod upload_worker;
mod watcher;
mod ws_client;

use std::time::Duration;
use sync_engine::SharedSyncEngine;
use tauri::Manager;

// ── Tauri 入口 ────────────────────────────────────────────────────────────────
//
// 这里只做 Builder 组装（插件/托管状态/setup/菜单事件/窗口事件/command 注册），
// 具体实现全在各自的模块里：业务 command 按域分在 commands/ 和各引擎模块
// （net.rs/config.rs/sync_engine.rs/clipboard_sync.rs/watcher.rs/transfers.rs/
// ws_client.rs 各自带自己那份 command），纯 UI/生命周期脚手架（菜单栏/托盘/
// 主窗口销毁重建/日志窗口/粘贴快传悬浮窗）在 app_shell/。

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // 先创建共享配置，setup 与 manage 共用同一 Arc
    let shared_config = config::init_sync_config();
    let clipboard_state = clipboard_sync::init_clipboard_state();
    let builder = tauri::Builder::default();
    // 单实例必须是第一个注册的插件。再次启动（点 Dock 图标 / 应用菜单）时，新进程把参数交给
    // 已运行的实例后立即退出，已运行的这个负责把主窗口叫出来——关窗后窗口已被销毁、进程只剩托盘，
    // GNOME 会把「点图标」当成「再启动一次」，没有这层保护就会冒出第二个进程。
    // 仅 release：debug 与 release 标识符相同，dev 启用会被已安装的实例吃掉。
    #[cfg(not(debug_assertions))]
    let builder = builder.plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
        app_shell::window::show_main_window(app);
    }));
    builder
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(
            tauri_plugin_global_shortcut::Builder::new()
                .with_handler(|app, _shortcut, event| {
                    use tauri_plugin_global_shortcut::ShortcutState;
                    if event.state == ShortcutState::Pressed {
                        app_shell::quick_paste::open_quick_paste_window(app);
                    }
                })
                .build(),
        )
        .manage(watcher::init_watcher_state())
        .manage(shared_config.clone())
        .manage(sync_engine::init_sync_engine())
        .manage(clipboard_state.clone())
        .setup(move |app| {
            // 启动时 tauri.conf.json 自动建出的主窗口用的是配置里写死的默认位置/大小，
            // 补上一次关闭时缓存的值（没有缓存就保持默认，见 app_shell::window）
            if let Some(w) = app.get_webview_window("main") {
                app_shell::window::apply_saved_geometry(&w);
            }
            // 绑定全局日志（事件 + 文件 + 级别过滤 + 轮转），任意模块即可 logger::info(...)
            let log_cfg = shared_config.read().log.clone();
            logger::init(app.handle().clone(), &log_cfg);
            base_store::init(); // 载入 base_hash 基线
                                // 剪贴板监听：进程内一份，默认关闭（state.enabled=false），用户在设置里打开才真正干活
            clipboard_sync::start_clipboard_watcher(
                clipboard_state.clone(),
                shared_config.clone(),
                app.handle().clone(),
            );
            logger::info(
                "app",
                format!(
                    "应用启动，配置目录: {} | 日志级别: {}",
                    app_paths::config_dir().display(),
                    log_cfg.level
                ),
            );

            // 网络灾备：注册节点表 + 起低频巡检（默认 15 分钟确认一次链路存活）。
            // 必须在菜单之前——菜单要读节点列表来画切换项。
            net::init(app.handle().clone(), shared_config.clone());
            // 传输状态（上传/下载/同步活动）的唯一来源，窗口销毁重建后靠它恢复进度显示
            transfers::init(app.handle().clone());
            net::start_health_loop();

            // 顶部菜单栏：「工具」→ 日志窗口；「网络」→ 节点切换 / 检测 / 设置
            //
            // Linux 不挂原生菜单栏：GNOME + Wayland 下 Window::set_menu 会在 GTK 里无限递归直到栈溢出、
            // 进程 SIGABRT（appmenu-gtk-module 全局菜单模块 / GTK 菜单栏的已知冲突，崩溃栈全部落在
            // set_menu 下面）。这些功能在 Linux 上都有替代入口：节点切换 / 设置在页面顶栏的节点指示器和
            // 齿轮里，日志窗口和网络节点设置在托盘菜单与用户下拉菜单里。
            #[cfg(not(target_os = "linux"))]
            {
                let menu = app_shell::menu::build_app_menu(app.handle())?;
                app.set_menu(menu)?;
            }

            // 粘贴快传：本地已经知道快捷键（上次登录/verify 写回过）就直接注册，
            // 不用等这次再登录一遍才能用
            if let Some(hotkey) = shared_config.read().quick_share_hotkey.clone() {
                if let Err(e) =
                    app_shell::quick_paste::register_quick_paste_shortcut(app.handle(), &hotkey)
                {
                    logger::warn("app", format!("启动时注册粘贴快传快捷键失败: {}", e));
                }
            }

            // 系统托盘：主窗口点 × 不退出程序（见下面 on_window_event），缩到这个图标；
            // 右键菜单可以直接唤起粘贴快传悬浮窗，或者真正退出程序
            app_shell::tray::setup_tray(app)?;

            // 同步默认启用：延迟 1s 后自动启动（等 app 就绪 + token 可能已加载）
            //
            // 必须用 tauri::async_runtime::spawn，不能像之前那样「起个 OS 线程 + 现开一个
            // tokio::runtime::Runtime + block_on」：do_start_sync 内部 tokio::spawn 出去的
            // WS 循环/上传 worker/防抖 flush 循环都会挂在那个临时 Runtime 上，而 block_on
            // 返回后（其间已经没有 .await 点）该 Runtime 立刻被 drop——Tokio 会把还没排上
            // 队的任务直接杀掉，WS 循环往往连第一次 connect 都没跑起来就没了。可 SharedSyncEngine
            // 的 guard 已经同步置上，is_sync_running 从此一直报「运行中」，没人会再重试，
            // 现象就是「登录正常、引擎显示运行中，但 WS 永远连不上」。
            {
                let cfg = shared_config.clone();
                let eng = app.state::<SharedSyncEngine>().inner().clone();
                let handle = app.handle().clone();
                tauri::async_runtime::spawn(async move {
                    tokio::time::sleep(Duration::from_secs(1)).await;
                    if cfg.read().token.is_empty() || eng.lock().is_some() {
                        return;
                    }
                    if let Err(e) = sync_engine::do_start_sync(&cfg, &eng, &handle).await {
                        logger::warn("app", format!("自动启动同步失败（可手动启动）: {}", e));
                    }
                });
            }

            Ok(())
        })
        .on_menu_event(|app, event| {
            let id = event.id().as_ref().to_string();
            match id.as_str() {
                "open_logs" => app_shell::log_window::open_log_window(app),
                "net_settings" => app_shell::window::open_network_settings(app),
                "net_probe" => {
                    tauri::async_runtime::spawn(async {
                        net::probe_all().await;
                    });
                }
                "net_auto" => {
                    // 勾选项由用户点击翻转，这里把翻转后的值写回配置（重建菜单会按配置重新勾）
                    let cfg = app.state::<config::SharedSyncConfig>();
                    let next = {
                        let mut c = cfg.write();
                        c.auto_failover = !c.auto_failover;
                        c.auto_failover
                    };
                    net::on_nodes_changed(false);
                    logger::info(
                        "net",
                        format!("自动灾备切换已{}", if next { "开启" } else { "关闭" }),
                    );
                }
                _ => {
                    if let Some(node_id) = id.strip_prefix("node:") {
                        let node_id = node_id.to_string();
                        // 切换本身是同步的，但切完要重新探测一次新节点，所以扔到后台
                        tauri::async_runtime::spawn(async move {
                            match net::switch_to(&node_id, "菜单栏手动切换") {
                                Ok(_) => {
                                    net::check_active_and_failover("手动切换后确认").await;
                                }
                                Err(e) => logger::error("net", format!("切换节点失败: {}", e)),
                            }
                        });
                    }
                }
            }
        })
        .on_window_event(|window, event| {
            if window.label() != "main" {
                return; // 日志窗口/粘贴快传悬浮窗该关就关
            }
            match event {
                // 点 ×：默认直接关闭并销毁 WebView（释放 WebView2 的几百 MB），进程不退出
                // （最后一个窗口关闭时的退出请求在 run 的 ExitRequested 里拦下）。
                // 例外：还有在这个 WebView 里跑的上传（快传 XHR），销毁会掐断它 ——
                // 先隐藏，等上传结束后由 destroy_main_if_idle 销毁。
                tauri::WindowEvent::CloseRequested { api, .. } => {
                    // 不管这次会不会真的关掉，先把当前位置/大小记下来，下次重建/启动时复原
                    app_shell::window::save_geometry(window);
                    if transfers::has_active_uploads_owned_by("main") {
                        api.prevent_close();
                        let _ = window.hide();
                        app_shell::window::DESTROY_MAIN_WHEN_IDLE
                            .store(true, std::sync::atomic::Ordering::SeqCst);
                        logger::info("app", "主窗口有进行中的快传，先隐藏，上传结束后再释放");
                    }
                }
                // WebView 没了，页面卸载钩子不一定有机会执行：监控页的 WS 订阅要在这里退掉，
                // 否则服务端会一直按间隔推送没人看的指标
                tauri::WindowEvent::Destroyed => {
                    ws_client::set_monitor_subscription(0, false);
                    logger::info("app", "主窗口已销毁，常驻内存仅剩后台进程");
                }
                _ => {}
            }
        })
        .invoke_handler(tauri::generate_handler![
            // 基础监听
            watcher::add_watch,
            watcher::remove_watch,
            watcher::list_watches,
            // 配置
            config::set_sync_config,
            config::get_sync_config,
            config::get_device_id,
            // 日志
            app_shell::log_window::open_log_window_cmd,
            app_shell::log_window::log_from_frontend,
            app_shell::log_window::get_recent_logs,
            // 主窗口生命周期 / 传输状态
            app_shell::window::take_pending_network_settings,
            transfers::get_transfers,
            transfers::transfer_upload_begin,
            transfers::transfer_upload_progress,
            transfers::transfer_upload_finish,
            transfers::transfer_clear_finished,
            // 粘贴快传
            app_shell::quick_paste::set_quick_paste_hotkey,
            // 记住密码
            commands::credential::save_remembered_credential,
            commands::credential::get_remembered_credential,
            commands::credential::clear_remembered_credential,
            // 同步引擎
            sync_engine::add_folder_mapping,
            sync_engine::remove_folder_mapping,
            sync_engine::start_sync,
            sync_engine::stop_sync,
            sync_engine::is_sync_running,
            sync_engine::is_ws_connected,
            // 通用 API 代理（管理域等新接口统一走它）
            commands::support::api_request,
            // 网络节点 / 自动灾备
            net::get_network_status,
            net::probe_server_nodes,
            net::switch_server_node,
            net::save_server_node,
            net::remove_server_node,
            net::set_failover_settings,
            // 剪贴板同步
            clipboard_sync::get_clipboard_settings,
            clipboard_sync::set_clipboard_settings,
            clipboard_sync::push_clipboard_now,
            clipboard_sync::apply_clipboard_text,
            // 系统监控（WS 推送）
            ws_client::subscribe_monitor,
            ws_client::unsubscribe_monitor,
            ws_client::set_process_detail_boost,
            // 用户域
            commands::user::login,
            commands::user::restore_token,
            commands::user::verify,
            commands::user::register,
            commands::user::reset_password,
            commands::user::update_profile,
            commands::user::change_password,
            // 文件域
            commands::file::get_available_disks,
            commands::file::traverse_directory,
            commands::file::upload_file,
            commands::file::delete_file,
            commands::file::build_download_url,
            commands::file::build_thumbnail_url,
            commands::file::get_download_history,
            commands::file::delete_download_history,
            // 同步域
            commands::sync_domain::save_sync_folder,
            commands::sync_domain::get_sync_folder,
            commands::sync_domain::delete_sync_folder,
            commands::sync_domain::update_sync_folder,
            commands::sync_domain::list_pending_tasks,
            commands::sync_domain::list_conflicts,
            commands::sync_domain::resolve_conflict,
            commands::sync_domain::delete_conflict,
            commands::sync_domain::list_sync_tasks,
            commands::sync_domain::clear_sync_tasks,
            // 应用更新域
            commands::update::list_app_releases,
            commands::update::publish_app_release,
            commands::update::update_app_release,
            commands::update::delete_app_release,
            commands::update::check_app_update,
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|_app, event| {
            // 主窗口关闭会销毁窗口；它若是最后一个窗口，Tauri 会发起退出请求。
            // 没有退出码 = 因为窗口都关了而自动触发的，拦下来保持托盘常驻；
            // 托盘「退出」调的是 app.exit(0)，带退出码，放行。
            if let tauri::RunEvent::ExitRequested { code: None, api, .. } = event {
                api.prevent_exit();
            }
        });
}
