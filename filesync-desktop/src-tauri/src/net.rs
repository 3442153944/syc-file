// net.rs
// 职责：桌面端统一网络出口 —— 服务器节点注册表 + 健康探测 + 自动灾备切换 + 切换时的请求取消。
//
// 为什么需要它：以前 server_url/ws_url 是配置里的两个死字符串，换服务器要用户手填，
// 且换完之后在途的 HTTP 请求仍然打在旧地址上（reqwest 超时 60s，最坏要等一分钟才失败），
// WS 也要等自己断了才会用新地址重连。节点挂掉时体验就是「点什么都转圈，转一分钟再报错」。
//
// 这里的做法是给所有出网操作套一个**世代号（epoch）**：
//   - ApiClient 构造时记下当时的 epoch；
//   - 每个请求都和 `wait_switch(epoch)` 一起 select，节点一换立刻返回 Err，不等 TCP 超时；
//   - WS 会话同样 select，节点一换立刻断开、由重连循环用新地址重连。
// 于是「切节点」变成一个瞬时动作，不必等在途请求跑完。
//
// 故障发现有两条路径：
//   1. 低频兜底轮询：默认 15 分钟 ping 一次当前节点（config.health_interval_minutes）；
//   2. 请求失败即时探测：ApiClient 撞到传输层错误时上报，触发一次（有节流的）全节点探测。
use crate::config::{ServerNode, SharedSyncConfig};
use crate::logger;
use parking_lot::{Mutex, RwLock};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::OnceLock;
use std::time::{Duration, Instant};
use tauri::{AppHandle, Emitter};
use tokio::sync::watch;

/// 单次探测的超时。比业务请求的 15s 连接超时短得多：探测的目的是「快速判断死活」，
/// 一个要等 15 秒才回话的节点，对用户来说和挂了没区别。
const PROBE_TIMEOUT: Duration = Duration::from_secs(6);

/// 失败上报触发全节点探测的最小间隔。并发请求同时失败时不该探测 N 次。
const FAILURE_PROBE_COOLDOWN: Duration = Duration::from_secs(30);

/// 当前没有任何节点可用时的重试间隔。此时不能还按 15 分钟等——网一恢复要尽快回来。
const OFFLINE_RETRY: Duration = Duration::from_secs(60);

// ── 对外数据结构 ──────────────────────────────────────────────────────────────

/// 服务端 /v1/ping 的 data 段。字段全 default，老服务端少几个也能解析。
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct PingData {
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub node: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub server_time: i64,
    #[serde(default)]
    pub uptime: i64,
    #[serde(default)]
    pub ws_conns: i64,
}

/// 一个节点最近一次探测的结果，前端网络面板直接渲染它。
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NodeHealth {
    pub id: String,
    pub name: String,
    pub server_url: String,
    pub reachable: bool,
    /// 往返耗时（毫秒）。不可达时填超时上限，仅供排序参考。
    pub latency_ms: u64,
    /// 本地时间戳（毫秒），标记这条结果有多新
    pub checked_at: i64,
    /// 不可达时的原因；可达时为空
    pub message: String,
    /// 服务端自报的节点名/版本（两条 frp 隧道打到同一后端时会相同）
    pub server_node: String,
    pub version: String,
    /// 本地时钟 - 服务端时钟（毫秒），正数表示本地偏快
    pub clock_skew_ms: i64,
    pub ws_conns: i64,
    pub uptime: i64,
}

/// 推给前端的整体网络状态（命令返回值 + `network-status` 事件负载）。
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NetStatus {
    pub active_id: String,
    pub active_name: String,
    pub server_url: String,
    pub ws_url: String,
    pub auto_failover: bool,
    pub interval_minutes: u64,
    pub nodes: Vec<ServerNode>,
    pub health: Vec<NodeHealth>,
    /// 全部节点都探不通
    pub offline: bool,
    /// 世代号，前端可用来判断「这条状态是不是切换之后的」
    pub epoch: u64,
}

// ── 运行期单例 ────────────────────────────────────────────────────────────────

struct NetRuntime {
    app: AppHandle,
    config: SharedSyncConfig,
    health: RwLock<HashMap<String, NodeHealth>>,
    /// 节点世代号。每切一次 +1；在途请求靠它感知「我该放弃了」。
    epoch: AtomicU64,
    switch_tx: watch::Sender<u64>,
    /// 最近一次「由请求失败触发」的探测时刻，用于节流
    last_failure_probe: Mutex<Option<Instant>>,
    /// 探测中标记，避免重入
    probing: AtomicBool,
    /// 唤醒健康轮询循环（切了节点/改了间隔后立即重新计时）
    wake_tx: watch::Sender<u64>,
}

static RT: OnceLock<NetRuntime> = OnceLock::new();

fn rt() -> Option<&'static NetRuntime> {
    RT.get()
}

/// 由 lib.rs 的 setup 调用一次。
pub fn init(app: AppHandle, config: SharedSyncConfig) {
    let (switch_tx, _) = watch::channel(0u64);
    let (wake_tx, _) = watch::channel(0u64);
    let _ = RT.set(NetRuntime {
        app,
        config,
        health: RwLock::new(HashMap::new()),
        epoch: AtomicU64::new(0),
        switch_tx,
        last_failure_probe: Mutex::new(None),
        probing: AtomicBool::new(false),
        wake_tx,
    });
}

/// 启动健康轮询循环。用 tauri 的全局运行时起任务，这样可以在 setup（非 async 上下文）里调。
pub fn start_health_loop() {
    tauri::async_runtime::spawn(async move {
        // 启动后先等几秒再首探：让窗口先画出来，别和启动时的登录校验抢带宽。
        tokio::time::sleep(Duration::from_secs(5)).await;
        loop {
            let ok = check_active_and_failover("定时巡检").await;
            let interval = if ok {
                let m = rt()
                    .map(|r| r.config.read().health_interval_minutes)
                    .unwrap_or(15)
                    .clamp(1, 24 * 60);
                Duration::from_secs(m * 60)
            } else {
                OFFLINE_RETRY
            };
            // 睡到下一轮，期间若有人切了节点/改了间隔就提前醒来重新计时
            let mut wake = match rt() {
                Some(r) => r.wake_tx.subscribe(),
                None => return,
            };
            tokio::select! {
                _ = tokio::time::sleep(interval) => {}
                _ = wake.changed() => {}
            }
        }
    });
}

// ── 世代号 / 请求取消 ─────────────────────────────────────────────────────────

/// 当前节点世代号。ApiClient::new 会把它记进请求里。
pub fn epoch() -> u64 {
    rt().map(|r| r.epoch.load(Ordering::SeqCst)).unwrap_or(0)
}

/// 等到节点从 `since` 这一代切走为止。和业务请求一起 select，用来「立即放弃在途请求」。
///
/// 未初始化（比如单测）时永远 pending —— 没有节点管理就没有切换，select 永远选另一边。
pub async fn wait_switch(since: u64) {
    let r = match rt() {
        Some(r) => r,
        None => return std::future::pending().await,
    };
    let mut rx = r.switch_tx.subscribe();
    loop {
        if r.epoch.load(Ordering::SeqCst) != since {
            return;
        }
        if rx.changed().await.is_err() {
            return std::future::pending().await;
        }
    }
}

/// ApiClient 撞到**传输层**错误（连不上/超时/TLS 失败）时调这里。
///
/// 只认传输层错误：HTTP 5xx 或业务码失败说明链路是通的，换节点没有意义，
/// 而且那种情况下切来切去只会把另一个节点也拖下水。
pub fn report_transport_failure(reason: &str) {
    let r = match rt() {
        Some(r) => r,
        None => return,
    };
    {
        let mut last = r.last_failure_probe.lock();
        if let Some(t) = *last {
            if t.elapsed() < FAILURE_PROBE_COOLDOWN {
                return; // 节流：一波并发请求同时失败只探测一次
            }
        }
        *last = Some(Instant::now());
    }
    let reason = reason.to_string();
    tokio::spawn(async move {
        logger::warn("net", format!("请求失败，触发节点探测: {}", reason));
        check_active_and_failover("请求失败").await;
    });
}

// ── 探测 ──────────────────────────────────────────────────────────────────────

/// ping 单个地址，返回 (往返毫秒, ping data)。
///
/// 两处向后兼容，都是为了「服务端还没升级」的过渡期：
/// - 方法：先 GET，撞上 404/405 再试一次 POST（旧后端只注册了 POST /v1/ping）；
/// - 响应：新版是 `{code,message,data}` 信封，旧版只有 `{"message":"pong"}`，
///   后者只要 HTTP 200 就算通，data 留空（前端会显示成「未知节点」，但不影响选路）。
async fn ping_url(server_url: &str) -> Result<(u64, PingData), String> {
    let client = reqwest::Client::builder()
        .connect_timeout(PROBE_TIMEOUT)
        .timeout(PROBE_TIMEOUT)
        .build()
        .map_err(|e| e.to_string())?;
    let url = format!("{}/v1/ping", server_url.trim_end_matches('/'));
    let started = Instant::now();

    let mut resp = send_probe(&client, reqwest::Method::GET, &url).await?;
    if matches!(resp.status().as_u16(), 404 | 405) {
        resp = send_probe(&client, reqwest::Method::POST, &url).await?;
    }

    let status = resp.status();
    let elapsed = started.elapsed().as_millis() as u64;
    if !status.is_success() {
        return Err(format!("HTTP {}", status.as_u16()));
    }
    let text = resp.text().await.unwrap_or_default();
    #[derive(Deserialize)]
    struct Envelope {
        code: i32,
        #[serde(default)]
        message: String,
        #[serde(default)]
        data: Option<PingData>,
    }
    match serde_json::from_str::<Envelope>(&text) {
        Ok(env) if env.code == 200 => Ok((elapsed, env.data.unwrap_or_default())),
        Ok(env) => Err(format!("[{}] {}", env.code, env.message)),
        // 老服务端只回 {"message":"pong"}，没有 code 字段。认 "pong" 而不是「只要 200 就算通」：
        // 有些中转/门户会对任意路径回一个 200 的 HTML，那种节点切过去只会更糟。
        Err(_) if text.contains("pong") => Ok((elapsed, PingData::default())),
        Err(_) => Err("响应不是本服务的 ping 接口".to_string()),
    }
}

/// 发一次探测请求，把 reqwest 的错误翻译成给用户看的短句。
async fn send_probe(
    client: &reqwest::Client,
    method: reqwest::Method,
    url: &str,
) -> Result<reqwest::Response, String> {
    client.request(method, url).send().await.map_err(|e| {
        if e.is_timeout() {
            "探测超时".to_string()
        } else if e.is_connect() {
            format!("连接失败: {}", short_err(&e.to_string()))
        } else {
            short_err(&e.to_string())
        }
    })
}

fn short_err(s: &str) -> String {
    let s = s.trim();
    if s.chars().count() > 120 {
        s.chars().take(120).collect::<String>() + "…"
    } else {
        s.to_string()
    }
}

fn now_ms() -> i64 {
    chrono::Local::now().timestamp_millis()
}

/// 探测一个节点并把结果写进健康表。
pub async fn probe_node(node: &ServerNode) -> NodeHealth {
    let h = match ping_url(&node.server_url).await {
        Ok((latency, d)) => NodeHealth {
            id: node.id.clone(),
            name: node.name.clone(),
            server_url: node.server_url.clone(),
            reachable: true,
            latency_ms: latency,
            checked_at: now_ms(),
            message: String::new(),
            server_node: d.node,
            version: d.version,
            clock_skew_ms: if d.server_time > 0 {
                now_ms() - d.server_time
            } else {
                0
            },
            ws_conns: d.ws_conns,
            uptime: d.uptime,
        },
        Err(e) => NodeHealth {
            id: node.id.clone(),
            name: node.name.clone(),
            server_url: node.server_url.clone(),
            reachable: false,
            latency_ms: PROBE_TIMEOUT.as_millis() as u64,
            checked_at: now_ms(),
            message: e,
            server_node: String::new(),
            version: String::new(),
            clock_skew_ms: 0,
            ws_conns: 0,
            uptime: 0,
        },
    };
    if let Some(r) = rt() {
        r.health.write().insert(h.id.clone(), h.clone());
    }
    h
}

/// 并发探测全部节点，结果按配置里的节点顺序返回。
pub async fn probe_all() -> Vec<NodeHealth> {
    let nodes = match rt() {
        Some(r) => r.config.read().nodes.clone(),
        None => return vec![],
    };
    let out = futures_util::future::join_all(nodes.iter().map(probe_node)).await;
    emit_status();
    out
}

/// 探测当前节点；不通且开了自动灾备就切到最优的可用节点。
///
/// 返回 true 表示「当前有一个可用节点」（可能是原来的，也可能是刚切过去的）。
pub async fn check_active_and_failover(trigger: &str) -> bool {
    let r = match rt() {
        Some(r) => r,
        None => return false,
    };
    // 同一时刻只允许一轮探测：定时巡检和请求失败上报可能撞在一起
    if r.probing.swap(true, Ordering::SeqCst) {
        return true;
    }
    let result = do_check(r, trigger).await;
    r.probing.store(false, Ordering::SeqCst);
    result
}

async fn do_check(r: &'static NetRuntime, trigger: &str) -> bool {
    let (active, auto, nodes) = {
        let cfg = r.config.read();
        (
            cfg.active_node().cloned(),
            cfg.auto_failover,
            cfg.nodes.clone(),
        )
    };
    let active = match active {
        Some(n) => n,
        None => return false,
    };

    let h = probe_node(&active).await;
    if h.reachable {
        logger::debug(
            "net",
            format!("[{}] 节点 {} 可达 {}ms", trigger, active.name, h.latency_ms),
        );
        emit_status();
        return true;
    }
    logger::warn(
        "net",
        format!("[{}] 节点 {} 不可达: {}", trigger, active.name, h.message),
    );

    if !auto {
        emit_status();
        return false;
    }

    // 并发探测其余节点，挑延迟最低的可用者
    let others: Vec<ServerNode> = nodes.into_iter().filter(|n| n.id != active.id).collect();
    if others.is_empty() {
        emit_status();
        return false;
    }
    let best = futures_util::future::join_all(others.iter().map(probe_node))
        .await
        .into_iter()
        .filter(|h| h.reachable)
        .min_by_key(|h| h.latency_ms);

    match best {
        Some(b) => {
            let reason = format!("{} 不可达（{}）", active.name, h.message);
            match switch_to(&b.id, &reason) {
                Ok(_) => true,
                Err(e) => {
                    logger::error("net", format!("自动切换失败: {}", e));
                    emit_status();
                    false
                }
            }
        }
        None => {
            logger::error("net", "所有节点均不可达，保持当前节点等待网络恢复");
            emit_status();
            false
        }
    }
}

// ── 切换 ──────────────────────────────────────────────────────────────────────

/// 切到指定节点：改配置 → 落盘 → epoch+1（在途请求/WS 会话立即放弃）→ 通知前端。
///
/// 同步函数：调用方常常是 Tauri 命令或原生菜单回调，没有 async 上下文。
pub fn switch_to(node_id: &str, reason: &str) -> Result<ServerNode, String> {
    let r = rt().ok_or("网络模块未初始化")?;
    let node = {
        let mut cfg = r.config.write();
        let node = cfg
            .nodes
            .iter()
            .find(|n| n.id == node_id)
            .cloned()
            .ok_or_else(|| format!("节点不存在: {}", node_id))?;
        // 已经在这个节点上就什么都不做，否则会白白掐掉在途请求
        if cfg.active_node_id == node.id
            && cfg.server_url == node.server_url
            && cfg.ws_url == node.ws_url
        {
            return Ok(node);
        }
        cfg.active_node_id = node.id.clone();
        cfg.server_url = node.server_url.clone();
        cfg.ws_url = node.ws_url.clone();
        cfg.save();
        node
    };

    bump_epoch();
    logger::info(
        "net",
        format!(
            "已切换到节点 {} ({}) —— {}",
            node.name, node.server_url, reason
        ),
    );
    let _ = r.app.emit(
        "network-node-changed",
        serde_json::json!({
            "id": node.id, "name": node.name,
            "serverUrl": node.server_url, "wsUrl": node.ws_url,
            "reason": reason,
        }),
    );
    emit_status();
    Ok(node)
}

/// 世代号 +1 并广播：在途 HTTP 请求与 WS 会话据此立刻收手。
/// 同时唤醒健康轮询循环，让它按新节点重新计时。
fn bump_epoch() {
    if let Some(r) = rt() {
        let next = r.epoch.fetch_add(1, Ordering::SeqCst) + 1;
        let _ = r.switch_tx.send(next);
        let _ = r.wake_tx.send(next);
    }
}

/// 节点配置被改动（增删改、开关自动灾备、改巡检间隔）后调用：落盘 + 通知前端。
/// `restart_links` 为 true 时还会掐掉在途连接——改了**当前**节点的地址时必须这么做，
/// 否则 WS 会一直挂在老地址上直到它自己断。
pub fn on_nodes_changed(restart_links: bool) {
    if let Some(r) = rt() {
        r.config.write().save();
        if restart_links {
            bump_epoch();
        } else {
            let _ = r.wake_tx.send(r.epoch.load(Ordering::SeqCst));
        }
    }
    emit_status();
}

// ── 状态查询 ──────────────────────────────────────────────────────────────────

pub fn status() -> NetStatus {
    let r = match rt() {
        Some(r) => r,
        None => {
            return NetStatus {
                active_id: String::new(),
                active_name: String::new(),
                server_url: String::new(),
                ws_url: String::new(),
                auto_failover: true,
                interval_minutes: 15,
                nodes: vec![],
                health: vec![],
                offline: false,
                epoch: 0,
            }
        }
    };
    let cfg = r.config.read();
    let health_map = r.health.read();
    // 按节点顺序输出，前端不必自己排序
    let health: Vec<NodeHealth> = cfg
        .nodes
        .iter()
        .filter_map(|n| health_map.get(&n.id).cloned())
        .collect();
    // 一条探测结果都还没有时不算离线（刚启动的正常状态）
    let offline = !health.is_empty() && health.iter().all(|h| !h.reachable);
    NetStatus {
        active_id: cfg.active_node_id.clone(),
        active_name: cfg.active_node().map(|n| n.name.clone()).unwrap_or_default(),
        server_url: cfg.server_url.clone(),
        ws_url: cfg.ws_url.clone(),
        auto_failover: cfg.auto_failover,
        interval_minutes: cfg.health_interval_minutes,
        nodes: cfg.nodes.clone(),
        health,
        offline,
        epoch: r.epoch.load(Ordering::SeqCst),
    }
}

/// 把最新状态推给前端（菜单栏指示灯、网络面板都监听它），并重建原生菜单栏 ——
/// 节点勾选项和后面那串延迟数字都要跟着变。
///
/// 所有会改变网络状态的路径（切换、增删改、探测）最终都会走到这里，
/// 所以调用方不需要自己再去刷菜单。
pub fn emit_status() {
    if let Some(r) = rt() {
        let _ = r.app.emit("network-status", status());
        crate::refresh_app_menu(&r.app);
    }
}
