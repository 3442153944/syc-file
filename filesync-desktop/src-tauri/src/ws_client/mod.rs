// ws_client/mod.rs
// 职责：WebSocket 连接生命周期管理 + 同步消息路由。
//   transport.rs  建连（走 reqwest 的代理/DNS/TLS 栈）
//   io.rs         单条连接上的读写循环：心跳、死连接检测、出站帧
//   polling.rs    WS 长期不可用时降级为 HTTP 轮询
//   tasks.rs      task_created（download/delete/mkdir）
//   conflict.rs   conflict / conflict_resolved
//   download.rs   下载发布原语与路径工具（tasks/conflict 共用）
mod conflict;
mod download;
mod io;
mod polling;
mod tasks;
mod transport;

use crate::api::ws::types::{ConflictContent, ConflictResolvedContent, TaskCreatedContent, WsEnvelope};
use crate::catch_up;
use crate::config::SharedSyncConfig;
use crate::logger;
use crate::upload_worker::UploadTask;
use futures_util::{Sink, Stream};
use parking_lot::Mutex;
use serde::Serialize;
use std::sync::atomic::{AtomicBool, AtomicI64, Ordering};
use std::sync::OnceLock;
use std::time::{Duration, Instant};
use tauri::{AppHandle, Emitter, Manager};
use tokio::sync::{mpsc, oneshot};
use tokio::time::{sleep, timeout};
use tokio_tungstenite::tungstenite::{Error as WsError, Message};
use transport::ConnectError;

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct WsStatus {
    pub connected: bool,
    pub message: String,
}

pub fn start_ws_client(
    config: SharedSyncConfig,
    upload_tx: mpsc::Sender<UploadTask>,
    app: AppHandle,
) {
    tokio::spawn(run_ws_loop(config, upload_tx, app));
}

/// 重连间隔：固定 3 秒，**不做指数退避、不限次数**。
/// WS 是同步链路的刚需（task_created 全靠它推），退避到 30s 只会让断网恢复后白等半分钟。
const RECONNECT_INTERVAL: Duration = Duration::from_secs(3);

/// 整个握手（DNS + TCP + 代理隧道 + TLS + HTTP 升级）的超时兜底。
/// 经企业代理 + TLS 检查的链路比直连慢得多，给得比 TCP 层超时宽裕。
const CONNECT_TIMEOUT: Duration = Duration::from_secs(15);

/// 连上后至少活过这么久才算「稳住了」。短命连接（连上几秒就被掐）和连不上一样算失败。
const STABLE_AFTER: Duration = Duration::from_secs(30);

/// 连续失败这么多次就降级到 HTTP 轮询。
const DEGRADE_AFTER_FAILURES: u32 = 3;

/// 降级后的循环间隔：每轮拉一次待办任务 + 试一次 WS。企业防火墙那边没必要每 3 秒敲一次门。
const DEGRADED_INTERVAL: Duration = Duration::from_secs(15);

/// 降级期间完整追赶（扫描本地 + 上报清单，让服务端补派缺失任务）的最小间隔。它要遍历并哈希整个同步目录，
/// 不能像拉任务那样每轮都跑。
const DEGRADED_CATCH_UP_EVERY: Duration = Duration::from_secs(600);

/// 当前会话的出站发送端。前端通过 Tauri 命令要往 WS 上发东西（如订阅监控），都经这里。
/// 会话建立时装上、断开时清掉；没有活动会话时发送返回 false。
static WS_OUTBOUND: OnceLock<Mutex<Option<mpsc::UnboundedSender<Message>>>> = OnceLock::new();

fn ws_outbound() -> &'static Mutex<Option<mpsc::UnboundedSender<Message>>> {
    WS_OUTBOUND.get_or_init(|| Mutex::new(None))
}

/// 往当前 WS 会话发一帧文本。无活动会话返回 false（调用方据此决定是否稍后重试）。
pub fn ws_send_text(json: String) -> bool {
    if let Some(tx) = ws_outbound().lock().as_ref() {
        return tx.send(Message::Text(json)).is_ok();
    }
    false
}

/// 监控订阅意向：断线重连后 connID 会变、服务端旧订阅随之失效，
/// 所以要在每次新会话建立时自动补发一次 subscribe。0 = 未订阅，>0 = 订阅且为推送间隔（秒）。
static MONITOR_SUB_INTERVAL: AtomicI64 = AtomicI64::new(0);
/// 是否要求服务端把进程明细采集顶到秒级（见 new_server 的 SetDetailBoost）。
/// 断线重连时跟 interval 一起补发，避免重连后悄悄掉回默认间隔而前端毫无感知。
static MONITOR_SUB_DETAIL_BOOST: AtomicBool = AtomicBool::new(false);

/// 设置/取消监控订阅意向，并立即对当前会话生效。interval<=0 表示退订。
pub fn set_monitor_subscription(interval: i64, detail_boost: bool) {
    MONITOR_SUB_INTERVAL.store(interval.max(0), Ordering::SeqCst);
    MONITOR_SUB_DETAIL_BOOST.store(detail_boost, Ordering::SeqCst);
    let frame = if interval > 0 {
        serde_json::json!({
            "type":"monitor",
            "content":{"event":"subscribe","interval":interval,"detail_boost":detail_boost}
        })
    } else {
        serde_json::json!({"type":"monitor","content":{"event":"unsubscribe"}})
    };
    ws_send_text(frame.to_string());
}

/// 新会话建立后调用：若之前订阅了监控，自动补发 subscribe（带上当时的 detail_boost）。
fn resubscribe_monitor() {
    let interval = MONITOR_SUB_INTERVAL.load(Ordering::SeqCst);
    if interval > 0 {
        let detail_boost = MONITOR_SUB_DETAIL_BOOST.load(Ordering::SeqCst);
        let frame = serde_json::json!({
            "type":"monitor",
            "content":{"event":"subscribe","interval":interval,"detail_boost":detail_boost}
        });
        ws_send_text(frame.to_string());
    }
}

/// 订阅系统监控。进入监控页时调用，指标经 WS 推来，前端监听 `monitor-metrics` 事件。
/// interval 是期望推送间隔（秒），服务端会夹到 [1,10]。断线重连由 Rust 侧自动补订阅。
/// 不改 detail_boost（沿用当前值，默认 false）——秒级采集由 set_process_detail_boost 单独控制。
#[tauri::command]
pub fn subscribe_monitor(interval: Option<i64>) {
    let detail_boost = MONITOR_SUB_DETAIL_BOOST.load(Ordering::SeqCst);
    set_monitor_subscription(interval.unwrap_or(2).max(1), detail_boost);
}

/// 退订系统监控。离开监控页时调用，服务端随即停止为本连接采样。
#[tauri::command]
pub fn unsubscribe_monitor() {
    set_monitor_subscription(0, false);
}

/// 打开/关闭进程秒级采集，不改推送间隔。进程页切换时间区间预设时调用：选了
/// 5/15/30 分钟这类短窗口传 true，选更长的区间传 false。
/// 前提是已经订阅了监控（interval>0）——没订阅就没有当前会话可用的推送间隔，直接忽略。
#[tauri::command]
pub fn set_process_detail_boost(on: bool) {
    let interval = MONITOR_SUB_INTERVAL.load(Ordering::SeqCst);
    if interval <= 0 {
        return;
    }
    set_monitor_subscription(interval, on);
}

async fn run_ws_loop(
    config: SharedSyncConfig,
    upload_tx: mpsc::Sender<UploadTask>,
    app: AppHandle,
) {
    // 连续「没能维持住」的次数：连不上，或者连上后活不过 STABLE_AFTER。
    let mut fail_streak: u32 = 0;
    let mut degraded = false;
    let mut last_degraded_catch_up: Option<Instant> = None;

    loop {
        let (ws_url, token, device_id, device_name) = {
            let cfg = config.read();
            (
                cfg.ws_url.clone(),
                cfg.token.clone(),
                cfg.device_id.clone(),
                cfg.device_name.clone(),
            )
        };

        // 还没配服务器 / 还没登录：没什么可连的，等下一轮
        if ws_url.is_empty() || token.is_empty() {
            sleep(RECONNECT_INTERVAL).await;
            continue;
        }

        // 协议 §2.1：连接时带 device_id/device_type/platform
        let url = format!(
            "{}/v1/ws/connect?token={}&device_id={}&device_type=desktop&device_name={}&platform=windows",
            ws_url.trim_end_matches('/'), token, device_id, urlenc(&device_name)
        );

        // 记下本次会话建立时的节点世代号：切节点时会话会立刻结束，
        // 不用等这条挂在旧地址上的连接自己超时断开（见 net.rs）。
        let epoch = crate::net::epoch();
        let started = Instant::now();

        match timeout(CONNECT_TIMEOUT, transport::connect(&url)).await {
            Ok(Ok(mut ws)) => {
                logger::info("ws", "已连接到服务器");
                emit_ws_status(&app, true, "已连接到服务器");
                // 装上本会话的出站通道，供前端命令（订阅监控等）往 WS 发消息
                let (out_tx, mut out_rx) = mpsc::unbounded_channel::<Message>();
                *ws_outbound().lock() = Some(out_tx);
                // 降级期间连接可能几十秒就被掐一次，每个短命会话都全量追赶会造成哈希风暴：
                // 沿用降级模式自己的节流（DEGRADED_CATCH_UP_EVERY），到点了才在会话里追赶
                let run_catch_up = !degraded
                    || last_degraded_catch_up.map_or(true, |t| t.elapsed() >= DEGRADED_CATCH_UP_EVERY);
                if degraded && run_catch_up {
                    last_degraded_catch_up = Some(Instant::now());
                }
                let reason = handle_session(
                    &mut ws, &mut out_rx, &config, &upload_tx, &app, epoch, run_catch_up,
                )
                .await;
                // 会话结束：清掉出站通道，之后的 ws_send_text 会返回 false
                *ws_outbound().lock() = None;
                match reason {
                    io::EndReason::Closed => logger::warn("ws", "连接断开，正在重连…"),
                    io::EndReason::Dead => logger::warn(
                        "ws",
                        format!(
                            "{}s 内没收到服务端任何数据，判定连接已死（多半是防火墙/网络静默丢包），正在重连…",
                            io::Timing::DEFAULT.dead_after.as_secs()
                        ),
                    ),
                    io::EndReason::WriteFailed => {
                        logger::warn("ws", "写入连接失败或超时，判定连接已死，正在重连…")
                    }
                    io::EndReason::Switched => {
                        logger::info("ws", "节点已切换，断开旧连接并用新地址重连")
                    }
                }
                if started.elapsed() >= STABLE_AFTER {
                    fail_streak = 0;
                } else {
                    fail_streak = fail_streak.saturating_add(1);
                }
                emit_ws_status(&app, false, &with_degraded_hint("连接断开，正在重连...", fail_streak));
            }
            Ok(Err(e)) => {
                fail_streak = fail_streak.saturating_add(1);
                logger::error("ws", format!("连接失败: {}", e));
                emit_ws_status(
                    &app,
                    false,
                    &with_degraded_hint(&format!("连接失败: {}", e), fail_streak),
                );
                // 只有传输层失败才怀疑节点；401 之类服务端明确拒绝说明链路是通的，换节点没用
                if matches!(e, ConnectError::Transport(_)) {
                    crate::net::report_transport_failure(&format!("WS 连接失败: {}", e));
                }
            }
            Err(_) => {
                fail_streak = fail_streak.saturating_add(1);
                let msg = format!("连接超时（{}s 未完成握手）", CONNECT_TIMEOUT.as_secs());
                logger::error("ws", &msg);
                emit_ws_status(&app, false, &with_degraded_hint(&msg, fail_streak));
                crate::net::report_transport_failure(&msg);
            }
        }

        // 刚切过节点：用户正盯着「连接中」看，别等，也别把旧节点的失败记到新节点头上
        if crate::net::epoch() != epoch {
            fail_streak = 0;
            degraded = false;
            last_degraded_catch_up = None;
            continue;
        }

        if fail_streak < DEGRADE_AFTER_FAILURES {
            degraded = false;
            last_degraded_catch_up = None;
            sleep(RECONNECT_INTERVAL).await;
            continue;
        }

        // ── 降级：WS 连续没能维持住，改用 HTTP 轮询保证同步不停 ──
        // （界面上的状态文案已经在上面各分支里带了降级提示，这里只记一次日志）
        if !degraded {
            degraded = true;
            logger::warn(
                "ws",
                format!(
                    "WS 连续 {} 次没能维持住，降级为 HTTP 轮询同步（每 {}s 一轮），WS 仍在后台重试",
                    fail_streak,
                    DEGRADED_INTERVAL.as_secs()
                ),
            );
        }
        // WS 会话开头的完整追赶在降级期间不会发生，低频补跑，让服务端能把缺失的任务补派下来
        if last_degraded_catch_up.map_or(true, |t| t.elapsed() >= DEGRADED_CATCH_UP_EVERY) {
            last_degraded_catch_up = Some(Instant::now());
            let (config, upload_tx, app) = (config.clone(), upload_tx.clone(), app.clone());
            tokio::spawn(async move {
                catch_up::catch_up_all_folders(&config, &upload_tx, &app).await;
            });
        }
        polling::poll_pending_tasks(&config).await;
        sleep(DEGRADED_INTERVAL).await;
    }
}

async fn handle_session<S>(
    ws: &mut S,
    out_rx: &mut mpsc::UnboundedReceiver<Message>,
    config: &SharedSyncConfig,
    upload_tx: &mpsc::Sender<UploadTask>,
    app: &AppHandle,
    epoch: u64,
    run_catch_up: bool,
) -> io::EndReason
where
    S: Stream<Item = Result<Message, WsError>> + Sink<Message> + Unpin,
{
    // 追赶放进独立任务：它要遍历并哈希整个同步目录，大目录能跑好几分钟。以前是在读循环
    // 之前 await，这段时间没人读 socket，服务端 ping 得不到 pong，60 秒后把连接掐了——
    // 重连又触发追赶，死循环。
    let (caught_up_tx, caught_up_rx) = oneshot::channel::<()>();
    if run_catch_up {
        let (config, upload_tx, app) = (config.clone(), upload_tx.clone(), app.clone());
        tokio::spawn(async move {
            catch_up::catch_up_all_folders(&config, &upload_tx, &app).await;
            let _ = caught_up_tx.send(());
        });
    } else {
        let _ = caught_up_tx.send(());
    }

    // 同步事件在独立任务里按序处理，不占读循环：冲突处理要现场下载整个文件，delete/mkdir
    // 要等 REST 回执，都可能很慢，读循环被占住就发不出心跳、回不了 pong。
    // 追赶没跑完之前先攒着——保持原来「追赶完才开始处理推送」的顺序语义。
    // 会话结束后这个任务会把积压的事件处理完再退出（那些是服务端已经派给我们的活）。
    let (sync_tx, mut sync_rx) = mpsc::unbounded_channel::<serde_json::Value>();
    {
        let (config, app) = (config.clone(), app.clone());
        tokio::spawn(async move {
            let _ = caught_up_rx.await; // 追赶任务异常退出（Err）也照样往下走
            while let Some(content) = sync_rx.recv().await {
                dispatch_file_sync(content, &config, &app).await;
            }
        });
    }

    // 断线重连后 connID 变了，服务端旧订阅已失效——重新补发一次监控订阅
    resubscribe_monitor();

    let app = app.clone();
    io::run(
        ws,
        out_rx,
        io::Timing::DEFAULT,
        crate::net::wait_switch(epoch),
        move |text| route_text(text, &app, &sync_tx),
    )
    .await
}

/// 入站文本帧分流。运行在读循环里，必须轻量：只做解析和分发，不做任何慢操作。
fn route_text(text: &str, app: &AppHandle, sync_tx: &mpsc::UnboundedSender<serde_json::Value>) {
    let env: WsEnvelope = match serde_json::from_str(text) {
        Ok(v) => v,
        Err(_) => return,
    };
    let Some(content) = env.content else {
        return; // ack 等没有内容的帧
    };
    match env.kind.as_str() {
        // 剪贴板：服务端单向投递（上报走 REST），内容就是一条 ClipItem
        "clipboard" => on_clipboard(content, app),
        // 监控：服务端定时推送的指标帧，原样转成 Tauri 事件给监控页
        "monitor" => {
            let _ = app.emit("monitor-metrics", content);
        }
        // 通用通知：服务端主动推送的提示（目前用于资源告警，见 new_server 的
        // ws.NotifyAll），内容形如 {title, message, level, time}，原样转发给前端
        // 弹 naive-ui 通知，不在 Rust 侧解析字段——以后这条通道加别的通知类型
        // 也不用跟着改 Rust。
        "notification" => {
            let _ = app.emit("server-notification", content);
        }
        "file_sync" => {
            let _ = sync_tx.send(content);
        }
        _ => {}
    }
}

async fn dispatch_file_sync(content: serde_json::Value, config: &SharedSyncConfig, app: &AppHandle) {
    let event = content
        .get("event")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();

    match event.as_str() {
        "task_created" => {
            if let Ok(tc) = serde_json::from_value::<TaskCreatedContent>(content) {
                tasks::on_task_created(tc, config).await;
            }
        }
        "conflict" => {
            if let Ok(cf) = serde_json::from_value::<ConflictContent>(content) {
                conflict::on_conflict(cf, config, app).await;
            }
        }
        "conflict_resolved" => {
            if let Ok(cr) = serde_json::from_value::<ConflictResolvedContent>(content) {
                conflict::on_conflict_resolved(cr, config, app).await;
            }
        }
        _ => {}
    }
}

// ── 工具函数 ─────────────────────────────────────────────────────────────────

/// 当前 WS 连接状态（电平）。`ws-status` 事件是边沿信号，只在连上/断开的瞬间发一次，
/// 前端若在那一下之前还没注册监听（app 启动即连上，监听注册在其后）就会永远错过，
/// 状态卡在初始的「未连接」。所以这里额外保留一份电平，供前端注册后主动查询补齐。
static WS_CONNECTED: AtomicBool = AtomicBool::new(false);

/// 前端注册监听后主动查一次，补上可能已错过的连接事件。
pub fn ws_is_connected() -> bool {
    WS_CONNECTED.load(Ordering::SeqCst)
}

/// 已经连续失败到会降级时，给状态文案补一句「同步还在走」：否则界面上只剩「连接失败」，
/// 用户会以为同步停了。
fn with_degraded_hint(base: &str, fail_streak: u32) -> String {
    if fail_streak >= DEGRADE_AFTER_FAILURES {
        format!("{} · 已降级为 HTTP 轮询同步，恢复后自动切回", base)
    } else {
        base.to_string()
    }
}

fn emit_ws_status(app: &AppHandle, connected: bool, message: &str) {
    WS_CONNECTED.store(connected, Ordering::SeqCst);
    app.emit(
        "ws-status",
        WsStatus {
            connected,
            message: message.into(),
        },
    )
    .ok();
}

fn urlenc(s: &str) -> String {
    s.chars()
        .flat_map(|c| {
            if c.is_alphanumeric() || matches!(c, '-' | '_' | '.') {
                vec![c]
            } else {
                format!("%{:02X}", c as u32).chars().collect()
            }
        })
        .collect()
}

/// 收到别的设备推来的剪贴板内容：始终发事件给前端（进历史列表），
/// 是否写入本机剪贴板由用户开关 auto_apply 决定。
fn on_clipboard(content: serde_json::Value, app: &AppHandle) {
    let item: crate::clipboard_sync::ClipItem = match serde_json::from_value(content) {
        Ok(v) => v,
        Err(e) => {
            logger::warn("clipboard", format!("解析剪贴板消息失败: {}", e));
            return;
        }
    };
    let state = app.state::<crate::clipboard_sync::SharedClipboardState>();
    let (enabled, auto_apply) = {
        let s = state.read();
        (s.enabled, s.auto_apply)
    };
    if !enabled {
        return; // 用户没开同步，收到也不处理
    }
    if auto_apply {
        match crate::clipboard_sync::apply_to_clipboard(&state, &item.content) {
            Ok(_) => logger::info(
                "clipboard",
                format!("已写入来自 {} 的剪贴板内容", item.device_name),
            ),
            Err(e) => logger::warn("clipboard", format!("写入剪贴板失败: {}", e)),
        }
    }
    let _ = app.emit("clipboard-received", item);
}
