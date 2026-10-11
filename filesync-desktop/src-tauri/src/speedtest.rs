// speedtest.rs
// 节点测速：把多路径上传核心（sync_core::upload_planner）的探测决策搬到 UI 层。
//
// 核心的探测循环一次只派一个任务（Wait 驱动），同一 planner 里没法并发；测速页要的是
// 「多网同时测」，所以对每个节点各建一个单节点 planner 并发跑：探测任务本身仍由核心
// 生成（微样本 RTT → 2MiB 吞吐样本，样本大小/顺序/失败处理都在核心里），本模块只负责
// 执行 HTTP 探测、回喂 Report、收集结果，最后用 combine_effective 做父链折算并给出
// 有效带宽与初始并发窗口。
//
// 测速结果同时合并回 config.upload_planner_state：10 分钟内的上传直接复用新鲜样本，
// 不再重复探测（见 chunked_uploader.rs 的 persist_planner_state）。
use crate::api::client::{ApiClient, ApiResponse};
use crate::api::routes;
use crate::config::{ServerNode, SharedSyncConfig};
use crate::logger;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::time::{Duration, Instant};
use sync_core::upload_planner as planner;
use tauri::State;

/// 单节点整体超时：RTT + 吞吐两趟，每趟 HTTP 自身 30s，留点余量。
const NODE_TIMEOUT: Duration = Duration::from_secs(70);

/// 测速接口响应 data 段（/v1/net/speedtest/upload）。
#[derive(Debug, Deserialize, Default)]
struct SpeedtestData {
    /// 服务端自报的后端节点名（同后端校验依据）
    #[serde(default)]
    node: String,
}

/// 单节点测速结果，前端表格直接渲染。
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NodeSpeed {
    pub id: String,
    pub name: String,
    pub server_url: String,
    /// 父节点 id（父链折算用；None = 独立链路）
    pub parent: Option<String>,
    pub reachable: bool,
    /// 微样本往返时延（ms）
    pub rtt_ms: u64,
    /// 实测上行带宽（B/s）
    pub upload_bps: f64,
    /// 父链折算后的有效带宽（B/s）：min(自身, 父链…)
    pub effective_bps: f64,
    /// 核心按有效带宽给出的初始并发窗口
    pub window: usize,
    /// 测速响应里的后端节点名（多个入口指向不同后端时不一致）
    pub backend_node: String,
    /// 实际发出的吞吐样本字节数
    pub probe_bytes: u64,
    /// 失败/降级原因（成功为空串）
    pub message: String,
}

/// 整轮测速结果。
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SpeedtestResult {
    pub tested_at: i64,
    pub active_id: String,
    /// 所有可达节点是否指向同一后端
    pub same_backend: bool,
    pub nodes: Vec<NodeSpeed>,
}

struct NodeRun {
    speed: NodeSpeed,
    /// 核心导出的样本（rtt+bps 都有才 Some），用于写回 config
    sample: Option<planner::PersistedNodeState>,
}

fn blank_speed(node: &ServerNode, msg: &str) -> NodeSpeed {
    NodeSpeed {
        id: node.id.clone(),
        name: node.name.clone(),
        server_url: node.server_url.clone(),
        parent: node.parent.clone(),
        reachable: false,
        rtt_ms: 0,
        upload_bps: 0.0,
        effective_bps: 0.0,
        window: 0,
        backend_node: String::new(),
        probe_bytes: 0,
        message: msg.to_string(),
    }
}

/// 对全部节点并发测速（桌面端；Web 端没有核心，页面自行提示不支持）。
#[tauri::command]
pub async fn speedtest_nodes(
    config: State<'_, SharedSyncConfig>,
) -> Result<SpeedtestResult, String> {
    let (server_url, token, device_id, active_id, nodes) = {
        let cfg = config.read();
        if cfg.token.is_empty() {
            return Err("未登录或登录已过期，请先登录再测速".into());
        }
        (
            cfg.server_url.clone(),
            cfg.token.clone(),
            cfg.device_id.clone(),
            cfg.active_node_id.clone(),
            cfg.nodes.clone(),
        )
    };
    if nodes.is_empty() {
        return Err("节点池为空，请先在网络设置里添加节点".into());
    }
    if server_url.is_empty() {
        return Err("服务器地址未配置".into());
    }

    let client = ApiClient::new(&server_url, &token, &device_id);
    let now = now_ms();

    // 每节点一个单节点 planner 并发跑；谁先探完谁先出结果，互不等待。
    let tasks = nodes.iter().map(|node| {
        let client = client.clone();
        let node = node.clone();
        async move {
            match tokio::time::timeout(NODE_TIMEOUT, run_node(client, &node, now)).await {
                Ok(run) => run,
                Err(_) => NodeRun {
                    speed: blank_speed(&node, "测速超时"),
                    sample: None,
                },
            }
        }
    });
    let mut runs: Vec<NodeRun> = futures_util::future::join_all(tasks).await;

    // 父链折算：子节点有效带宽 = min(自身, 父节点有效)；父节点测不出则不约束。
    let pool: Vec<planner::PlannerNodeConfig> = nodes
        .iter()
        .map(|n| planner::PlannerNodeConfig {
            id: n.id.clone(),
            url: n.server_url.clone(),
            parent: n.parent.clone(),
        })
        .collect();
    let measured: HashMap<String, f64> = runs
        .iter()
        .filter(|r| r.speed.reachable && r.speed.upload_bps > 0.0)
        .map(|r| (r.speed.id.clone(), r.speed.upload_bps))
        .collect();
    let eff = planner::combine_effective(&pool, &measured);
    for r in &mut runs {
        if !r.speed.reachable {
            continue;
        }
        let effective = eff.get(&r.speed.id).copied().unwrap_or(r.speed.upload_bps);
        r.speed.effective_bps = effective;
        r.speed.window =
            planner::initial_window(effective, Duration::from_millis(r.speed.rtt_ms.max(1)), 0);
    }

    // 样本合并回 config：测过的节点用新样本覆盖，失败节点保留旧样本等其自然过期。
    let tested: Vec<String> = nodes.iter().map(|n| n.id.clone()).collect();
    let fresh: Vec<planner::PersistedNodeState> =
        runs.iter().filter_map(|r| r.sample.clone()).collect();
    {
        let mut cfg = config.write();
        let mut state = cfg
            .upload_planner_state
            .as_ref()
            .and_then(|v| serde_json::from_value::<planner::PersistedState>(v.clone()).ok())
            .unwrap_or_default();
        state.nodes.retain(|n| !tested.contains(&n.id));
        state.nodes.extend(fresh);
        match serde_json::to_value(&state) {
            Ok(v) => {
                cfg.upload_planner_state = Some(v);
                cfg.save();
            }
            Err(e) => logger::warn("speedtest", format!("planner 样本序列化失败: {}", e)),
        }
    }

    for r in &runs {
        logger::info(
            "speedtest",
            if r.speed.reachable {
                format!(
                    "[speedtest] node={} rtt={}ms up={}/s eff={}/s conc={} backend={}",
                    r.speed.id,
                    r.speed.rtt_ms,
                    fmt_bps(r.speed.upload_bps),
                    fmt_bps(r.speed.effective_bps),
                    r.speed.window,
                    r.speed.backend_node
                )
            } else {
                format!("[speedtest] node={} 失败: {}", r.speed.id, r.speed.message)
            },
        );
    }

    // 同后端校验：所有拿到 backend_node 的节点必须一致。
    let backends: Vec<&str> = runs
        .iter()
        .map(|r| r.speed.backend_node.as_str())
        .filter(|b| !b.is_empty())
        .collect();
    let same_backend = backends.windows(2).all(|w| w[0] == w[1]);

    Ok(SpeedtestResult {
        tested_at: now,
        active_id,
        same_backend,
        nodes: runs.into_iter().map(|r| r.speed).collect(),
    })
}

/// 单节点：建一个单节点 planner（空样本、强制实测），把核心生成的探测任务 inline 执行完。
async fn run_node(client: ApiClient, node: &ServerNode, now: i64) -> NodeRun {
    let input = planner::PlannerInput {
        total_size: 0,
        main_node: node.id.clone(),
        now_ms: now,
        nodes: vec![planner::PlannerNodeConfig {
            id: node.id.clone(),
            url: node.server_url.clone(),
            parent: node.parent.clone(),
        }],
        // 空样本 = 不复用持久化缓存：测速页要的就是当下实测值
        state: planner::PersistedState::default(),
        adjust_cooldown_ms: planner::ADJUST_COOLDOWN_MS,
    };
    let mut p = match planner::Planner::new(input) {
        Ok(p) => p,
        Err(e) => {
            return NodeRun {
                speed: blank_speed(node, &e),
                sample: None,
            }
        }
    };

    let mut speed = blank_speed(node, "");
    let mut backend_node = String::new();
    loop {
        match p.next_task() {
            planner::Task::Probe(t) => {
                if t.kind == planner::ProbeKind::Throughput {
                    speed.probe_bytes = t.bytes;
                }
                let started = Instant::now();
                let mut outcome = run_probe(&client, &t).await;
                // 连接复用下中间代理可能刚好掐掉池化连接：吞吐探针「快速失败」时重试一次
                //（超时/业务码错误不重试：前者重试还是超时，后者是服务端明确拒绝）
                if t.kind == planner::ProbeKind::Throughput {
                    if let Err(e) = &outcome {
                        if started.elapsed() < Duration::from_secs(5) && !e.starts_with('[') {
                            outcome = run_probe(&client, &t).await;
                        }
                    }
                }
                match outcome {
                    Ok(report) => {
                        if let planner::Report::ProbeOk {
                            kind,
                            bytes,
                            elapsed_ms,
                            backend_node: bn,
                            ..
                        } = &report
                        {
                            backend_node = bn.clone();
                            match kind {
                                planner::ProbeKind::Tiny => speed.rtt_ms = *elapsed_ms,
                                planner::ProbeKind::Throughput => {
                                    let secs = *elapsed_ms as f64 / 1000.0;
                                    if secs > 0.0 {
                                        speed.upload_bps = *bytes as f64 / secs;
                                    }
                                }
                            }
                        }
                        p.report(report);
                    }
                    Err(e) => {
                        // 传输层失败（含切节点作废）：记下原因，但仍要回喂核心让状态机收敛
                        if speed.message.is_empty() {
                            speed.message = probe_error(t.kind, started.elapsed(), &e);
                        }
                        p.report(planner::Report::ProbeFailed {
                            node: t.node.clone(),
                            kind: t.kind,
                        });
                    }
                }
            }
            planner::Task::Wait => break,
            planner::Task::Failed { reason } => {
                if speed.message.is_empty() {
                    speed.message = reason;
                }
                break;
            }
            planner::Task::Done => break,
            other => {
                speed.message = format!("测速规划异常任务: {:?}", other);
                break;
            }
        }
    }

    let _ = p.planned_chunk_size(); // 触发 finalize（父链折算/出池判定）
    let sample = p.export_state().nodes.into_iter().find(|s| s.id == node.id);
    speed.reachable = speed.rtt_ms > 0;
    speed.backend_node = backend_node;
    if let Some(s) = &sample {
        speed.rtt_ms = s.rtt_ms.round() as u64;
        speed.upload_bps = s.bps;
    }
    if !speed.reachable && speed.message.is_empty() {
        speed.message = p.fail_reason().unwrap_or("探测失败").to_string();
    }
    if speed.reachable && speed.upload_bps <= 0.0 && speed.message.is_empty() {
        speed.message = "吞吐样本失败（仅测到 RTT）".into();
    }
    NodeRun { speed, sample }
}

/// 执行一个探测任务：POST 核心指定节点的测速接口，把响应当 RTT/带宽样本回报。
async fn run_probe(client: &ApiClient, t: &planner::ProbeTask) -> Result<planner::Report, String> {
    let body = vec![0u8; t.bytes as usize];
    let bytes_q = t.bytes.to_string();
    let params = [("bytes", bytes_q.as_str())];
    let started = Instant::now();
    let resp: Result<ApiResponse<SpeedtestData>, String> = client
        .post_bytes_to(
            &t.url,
            routes::NET_SPEEDTEST_UPLOAD,
            &params,
            body,
            Some(Duration::from_secs(30)),
        )
        .await;
    match resp {
        // 传输层失败（含切节点作废 NODE_SWITCHED）：原因原样上抛，调用方记进 message
        Err(e) => Err(e),
        Ok(r) if r.is_ok() => {
            let data = r.data.unwrap_or_default();
            Ok(planner::Report::ProbeOk {
                node: t.node.clone(),
                kind: t.kind,
                bytes: t.bytes,
                elapsed_ms: started.elapsed().as_millis().max(1) as u64,
                backend_node: data.node,
            })
        }
        // 业务码非 200（如登录过期）：把服务端的话原样带给 UI
        Ok(r) => Err(format!("[{}] {}", r.code, r.message)),
    }
}

/// 探测失败的人话化：reqwest 对「发送途中的整体超时」也只给一句
/// "error sending request for url (...)"，看不出原因；本地耗时接近请求超时上限（30s）
/// 时基本可以断定是吞吐样本没传完（链路太慢），换算成人能读的结论。
fn probe_error(kind: planner::ProbeKind, elapsed: Duration, raw: &str) -> String {
    const PROBE_TIMEOUT: Duration = Duration::from_secs(30);
    if kind == planner::ProbeKind::Throughput && elapsed >= PROBE_TIMEOUT - Duration::from_secs(1) {
        let min_kb = (planner::PROBE_THROUGHPUT_BYTES as f64 / PROBE_TIMEOUT.as_secs_f64() / 1000.0)
            .round() as u64;
        return format!(
            "吞吐探测超时：30s 内未传完 {}（上行带宽 < 约 {}KB/s）",
            fmt_size(planner::PROBE_THROUGHPUT_BYTES),
            min_kb
        );
    }
    raw.to_string()
}

/// 字节数 → 人类可读（2MiB / 512KB / 64B）。
fn fmt_size(n: u64) -> String {
    if n >= 1 << 20 {
        format!("{}MiB", n >> 20)
    } else if n >= 1 << 10 {
        format!("{}KB", n >> 10)
    } else {
        format!("{}B", n)
    }
}

/// bps → 人类可读（1.3MB/s / 500KB/s / 80B/s）。
fn fmt_bps(bps: f64) -> String {
    if bps >= 1_000_000.0 {
        format!("{:.1}MB/s", bps / 1_000_000.0)
    } else if bps >= 1_000.0 {
        format!("{:.0}KB/s", bps / 1_000.0)
    } else {
        format!("{:.0}B/s", bps)
    }
}

fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}
