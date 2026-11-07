use super::Client;
use crate::net_core::upload_planner as planner;
use std::collections::HashMap;
use std::time::Duration;

#[derive(Default)]
pub(super) struct NodeStat {
    pub(super) dispatched: u64,
    pub(super) ok: u64,
    pub(super) failed: u64,
    pub(super) slow: u64,
    pub(super) bytes: u64,
    pub(super) active_ms: u64,
    pub(super) last_error: String,
}

/// 传输结束后的实绩汇总：整体吞吐、每节点成功/失败/慢片/均速、最终离线或未参与的节点。
pub(super) fn log_transfer_summary(
    client: &Client,
    pool: &[planner::PlannerNodeConfig],
    windows: &[(String, usize)],
    stats: &HashMap<String, NodeStat>,
    probe_rtt: &HashMap<String, u64>,
    probe_bps: &HashMap<String, f64>,
    probe_backend: &HashMap<String, String>,
    main_backend: &str,
    elapsed: Duration,
    quick: bool,
) {
    let secs = elapsed.as_secs_f64().max(0.001);
    let sent: u64 = stats.values().map(|s| s.bytes).sum();
    let ok_chunks: u64 = stats.values().map(|s| s.ok).sum();
    let failed: u64 = stats.values().map(|s| s.failed).sum();
    let used = stats.values().filter(|s| s.ok > 0).count();
    client.info(
        "upload",
        format!(
            "[upload] 汇总: 模式={} 传输耗时={:.1}s 实传={} 均速={} 成功={}片 失败={} 使用节点={}/{}",
            if quick { "快传" } else { "同步" },
            secs,
            fmt_bytes(sent),
            fmt_bps(sent as f64 / secs),
            ok_chunks,
            failed,
            used,
            pool.len()
        ),
    );
    let mut offline: Vec<String> = Vec::new();
    let reason_for = |id: &str| -> String {
        if let Some(nb) = probe_backend.get(id) {
            if !main_backend.is_empty() && nb != main_backend {
                format!("后端不一致:{}", nb)
            } else if probe_bps.contains_key(id) {
                "已测未入池".to_string()
            } else {
                "吞吐探测未完成".to_string()
            }
        } else if probe_rtt.contains_key(id) {
            "吞吐探测未完成".to_string()
        } else {
            "未探测".to_string()
        }
    };
    for n in pool {
        let w = windows
            .iter()
            .find(|(id, _)| id == &n.id)
            .map(|(_, w)| *w)
            .unwrap_or(0);
        let Some(s) = stats.get(&n.id) else {
            if w == 0 {
                offline.push(format!("{}({})", n.id, reason_for(&n.id)));
            }
            continue;
        };
        let speed = if s.active_ms > 0 {
            fmt_bps(s.bytes as f64 / (s.active_ms as f64 / 1000.0))
        } else {
            "-".to_string()
        };
        client.info(
            "upload",
            format!(
                "[upload] 节点实绩 node={} 窗口={} 派发={} 成功={}片/{} 失败={} 慢片={} 均速={}{}",
                n.id,
                w,
                s.dispatched,
                s.ok,
                fmt_bytes(s.bytes),
                s.failed,
                s.slow,
                speed,
                if s.last_error.is_empty() {
                    String::new()
                } else {
                    format!(" 最后错误={}", s.last_error)
                }
            ),
        );
        if w == 0 {
            offline.push(format!(
                "{}({})",
                n.id,
                if s.failed > 0 {
                    "失败".to_string()
                } else {
                    reason_for(&n.id)
                }
            ));
        }
    }
    if !offline.is_empty() {
        client.warn(
            "upload",
            format!("[upload] 最终离线/未参与节点: {}", offline.join(", ")),
        );
    }
}

/// 传输开始前的规划日志：节点池、主节点、分片档位及复用的新鲜样本。
pub(super) fn log_plan_start(
    client: &Client,
    pool: &[planner::PlannerNodeConfig],
    state: &planner::PersistedState,
    main_node: &str,
    chunk_size: u64,
    total: u64,
    quick: bool,
) {
    let now = now_ms();
    let fresh_ids: Vec<String> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS)
        .map(|n| n.id.clone())
        .collect();
    let pool_desc: Vec<String> = pool
        .iter()
        .map(|n| match &n.parent {
            Some(p) => format!("{}({}←{})", n.id, n.url, p),
            None => format!("{}({})", n.id, n.url),
        })
        .collect();
    client.info(
        "upload",
        format!(
            "[upload] 规划: 模式={} 主节点={} 分片={} 大小={} 节点池[{}] 新鲜样本[{}]",
            if quick { "快传" } else { "同步" },
            main_node,
            fmt_chunk(chunk_size),
            fmt_bytes(total),
            if pool_desc.is_empty() {
                "-".to_string()
            } else {
                pool_desc.join(", ")
            },
            if fresh_ids.is_empty() {
                "-".to_string()
            } else {
                fresh_ids.join(",")
            },
        ),
    );
    if pool.len() < 2 {
        client.warn(
            "upload",
            format!(
                "[upload] 节点池只有 {} 个节点，本次上传为单链路（多路径需 ≥2 个节点）",
                pool.len()
            ),
        );
    }
}

pub(super) fn log_initial_windows(client: &Client, p: &planner::Planner) {
    let w: Vec<String> = p
        .windows()
        .into_iter()
        .map(|(id, n)| format!("{}={}", id, n))
        .collect();
    client.info("upload", format!("[upload] 初始窗口: {}", w.join(" ")));
}

/// 收尾规划日志：每节点 rtt/bw/eff/最终窗口 + 实际派发量 + 出池原因。
pub(super) fn log_plan(
    client: &Client,
    p: &planner::Planner,
    pool: &[planner::PlannerNodeConfig],
    probe_rtt: &HashMap<String, u64>,
    probe_bps: &HashMap<String, f64>,
    probe_backend: &HashMap<String, String>,
    main_node: &str,
    chunk_size: u64,
    node_dispatch: &HashMap<String, (u64, u64)>,
) {
    let measured: HashMap<String, f64> = probe_bps.clone();
    let eff = planner::combine_effective(pool, &measured);
    let windows = p.windows();
    let main_backend = probe_backend.get(main_node).cloned().unwrap_or_default();
    client.info(
        "upload",
        format!("[upload] 规划收尾: chunk={}", fmt_chunk(chunk_size)),
    );
    for n in pool {
        let conc = windows
            .iter()
            .find(|(id, _)| id == &n.id)
            .map(|(_, w)| *w)
            .unwrap_or(0);
        let rtt = probe_rtt
            .get(&n.id)
            .map(|v| format!("{}ms", v))
            .unwrap_or_else(|| "-".to_string());
        let b = probe_bps
            .get(&n.id)
            .map(|v| fmt_bps(*v))
            .unwrap_or_else(|| "-".into());
        let e = eff
            .get(&n.id)
            .map(|v| fmt_bps(*v))
            .unwrap_or_else(|| "-".into());
        let (cnt, bytes) = node_dispatch.get(&n.id).copied().unwrap_or((0, 0));
        let state = if conc > 0 {
            "在用".to_string()
        } else if let Some(nb) = probe_backend.get(&n.id) {
            if !main_backend.is_empty() && nb != &main_backend {
                format!("出池:后端不一致(本节点={} 主节点={})", nb, main_backend)
            } else if probe_bps.contains_key(&n.id) {
                "出池:已测未入池".to_string()
            } else {
                "出池:吞吐探测失败/未完成".to_string()
            }
        } else if probe_rtt.contains_key(&n.id) {
            "出池:吞吐探测失败".to_string()
        } else {
            "出池:探测失败/未探测".to_string()
        };
        client.info(
            "upload",
            format!(
                "[upload] node={} rtt={} bw={} eff={} conc={} [{}] 实传={}片/{}",
                n.id,
                rtt,
                b,
                e,
                conc,
                state,
                cnt,
                fmt_bytes(bytes)
            ),
        );
    }
    let used = node_dispatch.values().filter(|(c, _)| *c > 0).count();
    if used >= 2 {
        client.info(
            "upload",
            format!("[upload] 多链路生效: {} 个节点参与上传", used),
        );
    } else {
        client.warn(
            "upload",
            format!(
                "[upload] 单链路：仅 {} 个节点参与上传（其余节点见上方出池原因）",
                used
            ),
        );
    }
}

pub(super) fn log_window_changes(
    client: &Client,
    p: &planner::Planner,
    snapshot: &mut Vec<(String, usize)>,
    reason: &str,
) {
    let now = p.windows();
    for (id, w) in &now {
        if let Some((_, old)) = snapshot.iter().find(|(sid, _)| sid == id) {
            if old != w {
                let (kind, desc) = match (*old, *w) {
                    (0, n) if n > 0 => ("入池", format!("0→{}", n)),
                    (n, 0) if n > 0 => ("出池", format!("{}→0", n)),
                    _ => ("窗口", format!("{}→{}", old, w)),
                };
                client.info(
                    "upload",
                    format!("[upload] node={} {} {} ({})", id, kind, desc, reason),
                );
            }
        }
    }
    *snapshot = now;
}

pub(super) fn report_reason(r: &planner::Report) -> &'static str {
    match r {
        planner::Report::ChunkOk { .. } => "streak",
        planner::Report::ChunkCongested { .. } => "congested",
        planner::Report::ChunkUnavailable { .. } => "unavailable",
        planner::Report::ChunkDataError { .. } => "data",
        planner::Report::ChunkSessionGone { .. } => "gone",
        _ => "probe",
    }
}

pub(super) fn persist_planner_state(client: &Client, p: &planner::Planner) {
    client.host().save_planner_state(p.export_state());
}

pub(super) fn fmt_bps(bps: f64) -> String {
    if bps >= 1_000_000.0 {
        format!("{:.1}MB/s", bps / 1_000_000.0)
    } else if bps >= 1_000.0 {
        format!("{:.0}KB/s", bps / 1_000.0)
    } else {
        format!("{:.0}B/s", bps)
    }
}

fn fmt_chunk(n: u64) -> String {
    if n >= (1 << 20) && n % (1 << 20) == 0 {
        format!("{}MB", n >> 20)
    } else if n >= (1 << 10) && n % (1 << 10) == 0 {
        format!("{}KB", n >> 10)
    } else {
        format!("{}B", n)
    }
}

pub(super) fn fmt_bytes(n: u64) -> String {
    let f = n as f64;
    if f >= 1e9 {
        format!("{:.1}GB", f / 1e9)
    } else if f >= 1e6 {
        format!("{:.1}MB", f / 1e6)
    } else if f >= 1e3 {
        format!("{:.0}KB", f / 1e3)
    } else {
        format!("{}B", n)
    }
}

pub(super) fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}
