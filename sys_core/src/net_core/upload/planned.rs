use super::executor::{execute_chunk, run_probe, Outcome};
use super::fixed::run_once;
use super::logging::{
    log_initial_windows, log_plan, log_plan_start, log_transfer_summary, log_window_changes,
    now_ms, persist_planner_state, report_reason, NodeStat,
};
use super::session::{
    call_init, call_init_recovering, complete_quick_share, complete_upload_recovering,
    confirm_abandon,
};
use super::{
    describe_async, Client, PlannerSettings, ProgressFn, UploadOptions, DEFAULT_CHUNK_SIZE,
};
use crate::net_core::upload_planner as planner;
use protocol::UploadCompleteData;
use std::collections::HashMap;
use std::path::Path;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::mpsc;
use tokio::task::JoinSet;

use super::protocol;

/// 传输循环在 channel 与在途分片都无进展时的停滞保护。Recover 是 planner 的明确恢复
/// 等待，不走这个超时。
const STALL_TIMEOUT: Duration = Duration::from_secs(240);

/// 自适应路径：按大小定档（不等探测）→ describe → init → 多路传输（后台探测并发）→ complete。
pub(super) async fn run_planned(
    client: &Client,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &PlannerSettings,
) -> Result<serde_json::Value, String> {
    let total = file
        .metadata()
        .map_err(|e| format!("读取文件信息失败: {}", e))?
        .len();
    let file_name = file
        .file_name()
        .ok_or_else(|| "无效路径".to_string())?
        .to_string_lossy()
        .to_string();

    let pool = config.nodes.clone();
    if pool.is_empty() {
        client.warn("upload", "节点池为空，退回固定分片上传".to_string());
        let desc = describe_async(file, DEFAULT_CHUNK_SIZE).await?;
        on_progress(0, desc.total_size);
        return run_once(client, file, remote_dir, &desc, options, on_progress)
            .await
            .map_err(|e| e.into_string());
    }
    let (main_node, state) = (config.main_node.clone(), config.state.clone());
    let input = planner::PlannerInput {
        total_size: total,
        main_node: main_node.clone(),
        now_ms: now_ms(),
        nodes: pool.clone(),
        state: state.clone(),
        adjust_cooldown_ms: planner::ADJUST_COOLDOWN_MS,
        weight_exponent: planner::DEFAULT_WEIGHT_EXPONENT,
    };
    let mut p = planner::Planner::new(input).map_err(|e| format!("上传规划失败: {}", e))?;

    if let Some(size) = client.host().resume_chunk_size() {
        p.set_session_chunk_size(size)?;
    }

    let chunk_size = match p.planned_chunk_size() {
        Some(c) => c as usize,
        None => return Err("上传规划失败：无法确定分片大小".to_string()),
    };
    log_plan_start(
        client,
        &pool,
        &state,
        &main_node,
        chunk_size as u64,
        total,
        options.quick_share,
    );
    let now = now_ms();
    let mut probe_rtt: HashMap<String, u64> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS && n.rtt_ms > 0.0)
        .map(|n| (n.id.clone(), n.rtt_ms as u64))
        .collect();
    let mut probe_bps: HashMap<String, f64> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS && n.bps > 0.0)
        .map(|n| (n.id.clone(), n.bps))
        .collect();
    let mut probe_backend: HashMap<String, String> = state
        .nodes
        .iter()
        .filter(|n| now - n.measured_at_ms <= planner::STATE_FRESH_MS && !n.backend_node.is_empty())
        .map(|n| (n.id.clone(), n.backend_node.clone()))
        .collect();
    let mut node_dispatch: HashMap<String, (u64, u64)> = HashMap::new();
    let mut node_stats: HashMap<String, NodeStat> = HashMap::new();
    let mut probes_started: usize = 0;
    let mut probes_finished: usize = 0;
    let mut completion_retries = 0;

    let desc = describe_async(file, chunk_size).await?;
    on_progress(0, desc.total_size);

    let init = match call_init(
        client,
        &file_name,
        remote_dir,
        &desc,
        &options.device_id,
        &options.on_conflict,
        options.sync_base.as_deref(),
        options.quick_share,
    )
    .await
    {
        Ok(i) => i,
        Err(e) => {
            persist_planner_state(client, &p);
            return Err(e);
        }
    };

    // 秒传没有建会话，不能调 complete；文件名和路径以服务端返回值为准。
    if init.instant {
        if options.quick_share {
            return Err("快传收到意外的秒传响应".into());
        }
        on_progress(desc.total_size, desc.total_size);
        let name = if init.file_name.is_empty() {
            file_name
        } else {
            init.file_name.clone()
        };
        let path = if init.storage_path.is_empty() {
            format!("{}/{}", remote_dir.trim_end_matches(['/', '\\']), name)
        } else {
            init.storage_path.clone()
        };
        persist_planner_state(client, &p);
        let data = UploadCompleteData {
            file_id: 0,
            file_name: name,
            storage_path: path,
            file_size: desc.total_size,
            file_hash: desc.file_hash_hex.clone(),
            synced: true,
        };
        return serde_json::to_value(&data).map_err(|e| format!("序列化上传结果失败: {}", e));
    }

    let missing: Vec<u32> = init.missing.iter().map(|&i| i as u32).collect();
    let bytes_sent = Arc::new(AtomicI64::new(
        (desc.chunk_count as i64 - missing.len() as i64) * desc.chunk_size,
    ));
    on_progress(
        bytes_sent.load(Ordering::Relaxed).min(desc.total_size),
        desc.total_size,
    );

    p.begin_transfer(&init.upload_id, &missing);
    let mut upload_id = init.upload_id.clone();
    log_initial_windows(client, &p);
    let transfer_started = Instant::now();

    let (tx, mut rx) = mpsc::channel::<Outcome>(64);
    let mut tasks: JoinSet<()> = JoinSet::new();
    let mut chunk_handles = HashMap::new();
    let attempt_prefix = uuid::Uuid::new_v4().to_string();
    let mut win_snapshot: Vec<(String, usize)> = p.windows();
    let result: Result<serde_json::Value, String> = loop {
        match p.next_task() {
            planner::Task::Chunk(t) => {
                {
                    let e = node_dispatch.entry(t.node.clone()).or_insert((0, 0));
                    e.0 += 1;
                    e.1 += t.bytes;
                    node_stats.entry(t.node.clone()).or_default().dispatched += 1;
                }
                let tx = tx.clone();
                let client = client.clone();
                let path = file.to_path_buf();
                let uid = upload_id.clone();
                let desc_chunk = desc.chunk_size;
                let desc_total = desc.total_size;
                let seq = t.seq;
                let dispatched = t.clone();
                let attempt_id = format!("{attempt_prefix}-{seq}");
                let request_attempt = attempt_id.clone();
                let handle = tasks.spawn(async move {
                    let outcome = execute_chunk(
                        &client,
                        &path,
                        &uid,
                        desc_chunk,
                        desc_total,
                        &t,
                        &request_attempt,
                    )
                    .await;
                    let _ = tx.send(outcome).await;
                });
                chunk_handles.insert(seq, (dispatched, attempt_id, handle));
            }
            planner::Task::Probe(t) => {
                probes_started += 1;
                client.info(
                    "upload",
                    format!(
                        "[upload] 探测开始 node={} kind={} bytes={}",
                        t.node,
                        if t.kind == planner::ProbeKind::Tiny {
                            "rtt"
                        } else {
                            "bw"
                        },
                        t.bytes
                    ),
                );
                let tx = tx.clone();
                let client = client.clone();
                tasks.spawn(async move {
                    let outcome = run_probe(&client, &t).await;
                    let _ = tx.send(outcome).await;
                });
            }
            planner::Task::Recover { after_ms } => {
                // 明确的恢复等待不是调度停滞；sleep 随整个 upload future 一起可取消。
                tokio::time::sleep(Duration::from_millis(after_ms)).await;
            }
            planner::Task::Wait => {
                let wake = if tasks.is_empty() {
                    tokio::select! {
                        o = rx.recv() => o.map(Wake::Outcome),
                        _ = tokio::time::sleep(STALL_TIMEOUT) => Some(Wake::Stalled),
                    }
                } else {
                    tokio::select! {
                        o = rx.recv() => o.map(Wake::Outcome),
                        j = tasks.join_next() => Some(Wake::TaskDone(j)),
                        _ = tokio::time::sleep(STALL_TIMEOUT) => Some(Wake::Stalled),
                    }
                };
                match wake {
                    Some(Wake::Outcome(Outcome::Report(r))) => {
                        if let planner::Report::ChunkUnavailable { seq } = &r {
                            if !p.is_chunk_inflight(*seq) {
                                continue;
                            }
                            let failed_node = &chunk_handles
                                .get(seq)
                                .expect("在途分片必须持有取消句柄")
                                .0
                                .node;
                            let mut attempts = Vec::new();
                            let mut urls = Vec::new();
                            for (task, attempt, handle) in chunk_handles.values() {
                                if &task.node == failed_node {
                                    handle.abort();
                                    attempts.push(serde_json::json!({"index": task.index, "attempt_id": attempt}));
                                }
                            }
                            // 确认前不向 planner 报告，因此绝不派发替代尝试。优先其它已验证入口，
                            // 失败节点最后尝试；所有入口暂时不可用时在 confirm_abandon 内等待恢复。
                            for (node, window) in p.windows() {
                                if node != *failed_node && window > 0 {
                                    if let Some(cfg) = pool.iter().find(|cfg| cfg.id == node) {
                                        urls.push(cfg.url.clone());
                                    }
                                }
                            }
                            urls.push(chunk_handles.get(seq).unwrap().0.url.clone());
                            let body =
                                serde_json::json!({"upload_id": upload_id, "attempts": attempts});
                            if let Err(error) = confirm_abandon(client, &urls, &body).await {
                                break Err(error);
                            }
                        }
                        record_outcome(&r, &chunk_handles, &mut node_stats);
                        if matches!(
                            &r,
                            planner::Report::ProbeOk { .. } | planner::Report::ProbeFailed { .. }
                        ) {
                            probes_finished += 1;
                        }
                        record_probe_sample(&r, &mut probe_rtt, &mut probe_bps, &mut probe_backend);
                        let reason = report_reason(&r);
                        if let planner::Report::ChunkOk { seq, bytes, .. } = &r {
                            if !p.is_chunk_inflight(*seq) {
                                continue;
                            }
                            let prev = bytes_sent.fetch_add(*bytes as i64, Ordering::Relaxed);
                            let now = (prev + *bytes as i64).min(desc.total_size);
                            on_progress(now, desc.total_size);
                        }
                        p.report(r);
                        chunk_handles.retain(|seq, (_, _, handle)| {
                            if p.is_chunk_inflight(*seq) {
                                return true;
                            }
                            handle.abort();
                            false
                        });
                        log_window_changes(client, &p, &mut win_snapshot, reason);
                    }
                    Some(Wake::Outcome(Outcome::Fatal(e))) => break Err(e),
                    Some(Wake::TaskDone(Some(Ok(())))) => {}
                    Some(Wake::TaskDone(Some(Err(e)))) => {
                        if e.is_cancelled() {
                            continue;
                        }
                        break Err(format!("分片任务异常: {}", e));
                    }
                    Some(Wake::TaskDone(None)) => {}
                    None | Some(Wake::Stalled) => {
                        break Err("上传调度停滞：在途分片长时间无响应".into())
                    }
                }
            }
            planner::Task::Reinit => {
                // 旧会话的结果不能在 resume 后污染新会话进度或触发第二次 reinit。
                tasks.abort_all();
                chunk_handles.clear();
                while tasks.join_next().await.is_some() {}
                while rx.try_recv().is_ok() {}
                client.warn(
                    "upload",
                    format!("[upload] 会话过期，重新 init 后续传: {}", file_name),
                );
                let init2 = match call_init_recovering(
                    client,
                    &file_name,
                    remote_dir,
                    &desc,
                    &options.device_id,
                    &options.on_conflict,
                    options.sync_base.as_deref(),
                    options.quick_share,
                )
                .await
                {
                    Ok(i) => i,
                    Err(e) => break Err(e),
                };
                if init2.instant {
                    if options.quick_share {
                        break Err("快传收到意外的秒传响应".into());
                    }
                    on_progress(desc.total_size, desc.total_size);
                    let name = if init2.file_name.is_empty() {
                        file_name.clone()
                    } else {
                        init2.file_name.clone()
                    };
                    let path = if init2.storage_path.is_empty() {
                        format!("{}/{}", remote_dir.trim_end_matches(['/', '\\']), name)
                    } else {
                        init2.storage_path.clone()
                    };
                    let data = UploadCompleteData {
                        file_id: 0,
                        file_name: name,
                        storage_path: path,
                        file_size: desc.total_size,
                        file_hash: desc.file_hash_hex.clone(),
                        synced: true,
                    };
                    break serde_json::to_value(&data)
                        .map_err(|e| format!("序列化上传结果失败: {}", e));
                }
                upload_id = init2.upload_id.clone();
                let missing2: Vec<u32> = init2.missing.iter().map(|&i| i as u32).collect();
                bytes_sent.store(
                    (desc.chunk_count as i64 - missing2.len() as i64) * desc.chunk_size,
                    Ordering::Relaxed,
                );
                on_progress(
                    bytes_sent.load(Ordering::Relaxed).min(desc.total_size),
                    desc.total_size,
                );
                p.resume(&upload_id, &missing2);
            }
            planner::Task::Done => {
                let eligible: Vec<String> = p
                    .windows()
                    .into_iter()
                    .filter(|(_, window)| *window > 0)
                    .filter_map(|(id, _)| {
                        pool.iter()
                            .find(|node| node.id == id)
                            .map(|node| node.url.clone())
                    })
                    .collect();
                let completion = if options.quick_share {
                    complete_quick_share(client, &upload_id, &options.device_id, eligible).await
                } else {
                    // 普通 complete 也能切换同后端可用入口；仅在没有候选时保留激活入口兜底。
                    let mut candidates = eligible;
                    if candidates.is_empty() {
                        candidates.push(client.server_url().to_string());
                    }
                    complete_upload_recovering(client, &upload_id, &options.device_id, candidates)
                        .await
                };
                match completion {
                    Ok(d) => break Ok(d),
                    Err(e)
                        if e.retryable_chunk().is_some()
                            && completion_retries < planner::CHUNK_ATTEMPTS =>
                    {
                        completion_retries += 1;
                        client.warn(
                            "upload",
                            format!(
                                "[upload] 最终校验定位到坏块 {}，重新获取缺片并补传",
                                e.retryable_chunk().unwrap()
                            ),
                        );
                        p.request_reinit();
                    }
                    Err(e) => break Err(e.into_message()),
                }
            }
            planner::Task::Failed { reason } => break Err(reason),
        }
    };

    let probes_pending = probes_started.saturating_sub(probes_finished);
    if probes_pending > 0 {
        client.info(
            "upload",
            format!(
                "[upload] 探测收尾: 完成 {}/{}，{} 个未完成（随传输结束取消）",
                probes_finished, probes_started, probes_pending
            ),
        );
    }
    tasks.abort_all();
    log_plan(
        client,
        &p,
        &pool,
        &probe_rtt,
        &probe_bps,
        &probe_backend,
        &main_node,
        chunk_size as u64,
        &node_dispatch,
    );
    let main_backend = probe_backend.get(&main_node).cloned().unwrap_or_default();
    log_transfer_summary(
        client,
        &pool,
        &p.windows(),
        &node_stats,
        &probe_rtt,
        &probe_bps,
        &probe_backend,
        &main_backend,
        transfer_started.elapsed(),
        options.quick_share,
    );
    persist_planner_state(client, &p);
    result
}

fn record_probe_sample(
    r: &planner::Report,
    rtt: &mut HashMap<String, u64>,
    bps: &mut HashMap<String, f64>,
    backend: &mut HashMap<String, String>,
) {
    if let planner::Report::ProbeOk {
        node,
        kind,
        bytes,
        elapsed_ms,
        backend_node,
    } = r
    {
        if !backend_node.is_empty() {
            backend.insert(node.clone(), backend_node.clone());
        }
        match kind {
            planner::ProbeKind::Tiny => {
                rtt.insert(node.clone(), *elapsed_ms);
            }
            planner::ProbeKind::Throughput => {
                let secs = *elapsed_ms as f64 / 1000.0;
                if secs > 0.0 {
                    bps.insert(node.clone(), *bytes as f64 / secs);
                }
            }
        }
    }
}

fn record_outcome(
    r: &planner::Report,
    handles: &HashMap<u64, (planner::ChunkTask, String, tokio::task::AbortHandle)>,
    stats: &mut HashMap<String, NodeStat>,
) {
    let seq = match r {
        planner::Report::ChunkOk { seq, .. }
        | planner::Report::ChunkCongested { seq }
        | planner::Report::ChunkUnavailable { seq }
        | planner::Report::ChunkDataError { seq }
        | planner::Report::ChunkSessionGone { seq } => *seq,
        _ => return,
    };
    let Some((task, _, _)) = handles.get(&seq) else {
        return;
    };
    let s = stats.entry(task.node.clone()).or_default();
    match r {
        planner::Report::ChunkOk {
            bytes, elapsed_ms, ..
        } => {
            s.ok += 1;
            s.bytes += *bytes;
            s.active_ms += *elapsed_ms;
            if *elapsed_ms > 5_000 {
                s.slow += 1;
            }
        }
        _ => {
            s.failed += 1;
            s.last_error = report_reason(r).to_string();
        }
    }
}

enum Wake {
    Outcome(Outcome),
    TaskDone(Option<Result<(), tokio::task::JoinError>>),
    Stalled,
}
