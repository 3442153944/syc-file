use std::collections::HashMap;
use std::ffi::CString;
use std::time::Duration;

use super::*;

#[test]
fn unavailable_node_requeues_all_chunks_and_ignores_late_reports() {
    let p = Planner::new(input(
        vec![node("main", None), node("hk", Some("main"))],
        "main",
        1_000_000,
    ))
    .unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0, 1, 2]);
    let mut sent = Vec::new();
    while let Task::Chunk(t) = p.next_task() {
        sent.push(t);
    }
    let dead = &sent[0].node;
    let retired: Vec<_> = sent.iter().filter(|t| &t.node == dead).collect();
    assert!(retired.len() >= 2, "覆盖同节点多个在途分片");
    p.report(Report::ChunkUnavailable {
        seq: retired[0].seq,
    });
    assert_eq!(
        p.windows()
            .into_iter()
            .find(|(id, _)| id == dead)
            .unwrap()
            .1,
        0
    );
    for t in &retired {
        assert!(!p.is_chunk_inflight(t.seq));
        p.report(Report::ChunkOk {
            seq: t.seq,
            bytes: t.bytes,
            elapsed_ms: 10,
        });
        p.report(Report::ChunkSessionGone { seq: t.seq });
    }
    assert_eq!(p.phase_name(), "transfer");
    assert!(p.export_state().nodes.iter().all(|n| &n.id != dead));
    for t in sent.iter().filter(|t| &t.node != dead) {
        p.report(Report::ChunkOk {
            seq: t.seq,
            bytes: t.bytes,
            elapsed_ms: 100,
        });
    }
    let mut retried = Vec::new();
    loop {
        match p.next_task() {
            Task::Chunk(t) => {
                assert_ne!(&t.node, dead);
                retried.push(t.index);
                p.report(Report::ChunkOk {
                    seq: t.seq,
                    bytes: t.bytes,
                    elapsed_ms: 100,
                });
            }
            Task::Done => break,
            task => panic!("重传不能停滞: {task:?}"),
        }
    }
    retried.sort_unstable();
    let mut expected: Vec<_> = retired.iter().map(|t| t.index).collect();
    expected.sort_unstable();
    assert_eq!(retried, expected);
}

#[test]
fn all_transfer_nodes_unavailable_recovers_after_delay() {
    assert_eq!(PLANNER_VERSION, 2);
    let p = Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0]);
    let t = chunk_or_panic(p.next_task());
    p.report(Report::ChunkUnavailable { seq: t.seq });
    assert!(matches!(p.next_task(), Task::Recover { after_ms: 1000 }));
    assert!(matches!(
        p.next_task(),
        Task::Recover { after_ms: 1..=1000 }
    ));

    p.expire_recovery_for_test();
    let tiny = probe_or_panic(p.next_task());
    assert_eq!(tiny.node, "main");
    assert_eq!(tiny.kind, ProbeKind::Tiny);
    p.report(Report::ProbeOk {
        node: tiny.node,
        kind: tiny.kind,
        bytes: 0,
        elapsed_ms: 20,
        backend_node: "backend-1".into(),
    });
    let throughput = probe_or_panic(p.next_task());
    assert_eq!(throughput.kind, ProbeKind::Throughput);
    p.report(Report::ProbeOk {
        node: throughput.node,
        kind: throughput.kind,
        bytes: PROBE_THROUGHPUT_BYTES,
        elapsed_ms: 100,
        backend_node: "backend-1".into(),
    });
    let retry = chunk_or_panic(p.next_task());
    assert_eq!(retry.index, 0);
    p.report(Report::ChunkOk {
        seq: retry.seq,
        bytes: retry.bytes,
        elapsed_ms: 100,
    });
    assert!(matches!(p.next_task(), Task::Done));
}

#[test]
fn all_nodes_recover_partially_and_pending_chunks_use_healthy_node() {
    let p = Planner::new(input(
        vec![node("main", None), node("hk", Some("main"))],
        "main",
        1_000_000,
    ))
    .unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0, 1]);
    let first = chunk_or_panic(p.next_task());
    let second = chunk_or_panic(p.next_task());
    assert_ne!(first.node, second.node);
    p.report(Report::ChunkUnavailable { seq: first.seq });
    p.report(Report::ChunkUnavailable { seq: second.seq });
    assert!(matches!(p.next_task(), Task::Recover { .. }));

    p.expire_recovery_for_test();
    let main_tiny = probe_or_panic(p.next_task());
    let hk_tiny = probe_or_panic(p.next_task());
    assert_eq!(main_tiny.node, "main");
    assert_eq!(hk_tiny.node, "hk");
    p.report(Report::ProbeOk {
        node: main_tiny.node,
        kind: ProbeKind::Tiny,
        bytes: 0,
        elapsed_ms: 20,
        backend_node: "backend-1".into(),
    });
    p.report(Report::ProbeFailed {
        node: hk_tiny.node,
        kind: ProbeKind::Tiny,
    });
    let throughput = probe_or_panic(p.next_task());
    assert_eq!(throughput.node, "main");
    assert_eq!(throughput.kind, ProbeKind::Throughput);
    p.report(Report::ProbeOk {
        node: throughput.node,
        kind: ProbeKind::Throughput,
        bytes: PROBE_THROUGHPUT_BYTES,
        elapsed_ms: 100,
        backend_node: "backend-1".into(),
    });
    assert_eq!(p.windows().iter().find(|(id, _)| id == "hk").unwrap().1, 0);
    for _ in 0..2 {
        let retry = chunk_or_panic(p.next_task());
        assert_eq!(retry.node, "main");
        p.report(Report::ChunkOk {
            seq: retry.seq,
            bytes: retry.bytes,
            elapsed_ms: 100,
        });
    }
    assert!(matches!(p.next_task(), Task::Done));
}

#[test]
fn failed_recovery_probe_returns_recover_again() {
    let p = Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0]);
    let t = chunk_or_panic(p.next_task());
    p.report(Report::ChunkUnavailable { seq: t.seq });
    assert!(matches!(p.next_task(), Task::Recover { .. }));
    p.expire_recovery_for_test();
    let tiny = probe_or_panic(p.next_task());
    p.report(Report::ProbeFailed {
        node: tiny.node,
        kind: tiny.kind,
    });
    assert!(matches!(p.next_task(), Task::Recover { after_ms: 1000 }));
    assert!(p.windows().iter().all(|(_, window)| *window == 0));
}

#[test]
fn resume_does_not_bypass_recovery_probe() {
    let p = Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0]);
    let t = chunk_or_panic(p.next_task());
    p.report(Report::ChunkUnavailable { seq: t.seq });
    assert!(matches!(p.next_task(), Task::Recover { .. }));
    p.resume("u2", &[0]);
    assert!(matches!(p.next_task(), Task::Recover { .. }));
    assert!(p.windows().iter().all(|(_, window)| *window == 0));
    p.expire_recovery_for_test();
    assert_eq!(probe_or_panic(p.next_task()).kind, ProbeKind::Tiny);
}

fn node(id: &str, parent: Option<&str>) -> PlannerNodeConfig {
    PlannerNodeConfig {
        id: id.into(),
        url: format!("https://{id}.example/file"),
        parent: parent.map(|p| p.into()),
    }
}

fn input(nodes: Vec<PlannerNodeConfig>, main: &str, now: i64) -> PlannerInput {
    PlannerInput {
        total_size: 500 << 20,
        main_node: main.into(),
        now_ms: now,
        nodes,
        state: PersistedState::default(),
        adjust_cooldown_ms: 0,
        weight_exponent: DEFAULT_WEIGHT_EXPONENT,
    }
}

/// 跑完探测阶段。throughput_elapsed 控制吞吐样本耗时（决定测出的带宽档位）。
fn run_probes_with(mut p: Planner, fail: &[&str], throughput_elapsed: u64) -> Planner {
    let mut guard = 0;
    loop {
        guard += 1;
        assert!(guard < 100, "探测阶段死循环");
        match p.next_task() {
            Task::Probe(t) => {
                if fail.contains(&t.node.as_str()) {
                    p.report(Report::ProbeFailed {
                        node: t.node,
                        kind: t.kind,
                    });
                } else {
                    let (elapsed, bytes) = match t.kind {
                        ProbeKind::Tiny => (20, 0u64),
                        ProbeKind::Throughput => (throughput_elapsed, PROBE_THROUGHPUT_BYTES),
                    };
                    p.report(Report::ProbeOk {
                        node: t.node,
                        kind: t.kind,
                        bytes,
                        elapsed_ms: elapsed,
                        backend_node: "backend-1".into(),
                    });
                }
            }
            Task::Wait => break,
            other => panic!("探测阶段意外任务: {:?}", other),
        }
    }
    p
}

fn run_probes(p: Planner, fail: &[&str]) -> Planner {
    run_probes_with(p, fail, 1600)
}

fn chunk_or_panic(t: Task) -> ChunkTask {
    match t {
        Task::Chunk(t) => t,
        other => panic!("应派分片，got {:?}", other),
    }
}

fn probe_or_panic(t: Task) -> ProbeTask {
    match t {
        Task::Probe(t) => t,
        other => panic!("应派探测，got {:?}", other),
    }
}

#[test]
fn test_pick_chunk_size_by_size() {
    // 默认 4MiB，与文件大小无关（带宽交给运行期 AIMD）
    assert_eq!(pick_chunk_size_for(500 << 20), DEFAULT_UPLOAD_CHUNK);
    assert_eq!(pick_chunk_size_for(1 << 20), DEFAULT_UPLOAD_CHUNK);
    assert_eq!(pick_chunk_size_for(0), DEFAULT_UPLOAD_CHUNK);
    // 片数封顶：100GiB 在默认档会超 MAX_CHUNKS → 自动抬档直到放得下
    let total = 100u64 << 30;
    let c = pick_chunk_size_for(total);
    assert!(c > DEFAULT_UPLOAD_CHUNK);
    assert!(total.div_ceil(c) <= MAX_CHUNKS as u64 || c == MAX_CHUNK);
}

#[test]
fn test_initial_window() {
    assert_eq!(initial_window(8e6, Duration::from_millis(20), 8 << 20), 1);
    assert_eq!(initial_window(16e6, Duration::from_millis(100), 1 << 20), 2);
    // 300Mbps 单路径 → 38 路封顶 32
    assert_eq!(
        initial_window(300e6, Duration::from_millis(5), MAX_CHUNK),
        MAX_PER_NODE_CONCURRENCY
    );
    assert_eq!(initial_window(0.0, Duration::from_millis(10), MAX_CHUNK), 1);
}

#[test]
fn test_mem_budget_caps_window() {
    // 超大带宽探针（2MiB/5ms ≈ 419MB/s）→ 初始窗口需求巨大，
    // 必须被内存预算钳到 64MB/4MB=16，而不是 MAX_PER_NODE_CONCURRENCY(32)
    let nodes = vec![node("main", None)];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let mut p = run_probes_with(p, &[], 5);
    let chunk = p.planned_chunk_size().unwrap();
    assert_eq!(chunk, DEFAULT_UPLOAD_CHUNK);
    let w = p.windows()[0].1;
    assert_eq!(w, (MEM_BUDGET_PER_NODE / chunk) as usize);
    assert!(w < MAX_PER_NODE_CONCURRENCY);
}

#[test]
fn test_chunk_timeout_clamp() {
    assert_eq!(
        chunk_timeout(Duration::from_millis(10), Some(100e6), MAX_CHUNK),
        TIMEOUT_MIN
    );
    assert_eq!(
        chunk_timeout(Duration::from_secs(1), Some(1e3), MAX_CHUNK),
        TIMEOUT_MAX
    );
    let t = chunk_timeout(Duration::from_millis(100), Some(400e3), 4 << 20);
    assert!(t > TIMEOUT_MIN && t < TIMEOUT_MAX);
    // 无吞吐 EMA（首片）：给足上限时间，慢链路首片不被误判拥塞
    assert_eq!(
        chunk_timeout(Duration::from_millis(20), None, DEFAULT_UPLOAD_CHUNK),
        TIMEOUT_MAX
    );
}

#[test]
fn test_combine_effective_parent_cap() {
    let nodes = vec![
        node("main", None),
        node("hk", Some("main")),
        node("leaf", Some("hk")),
    ];
    let mut m = HashMap::new();
    m.insert("main".to_string(), 100e6);
    m.insert("hk".to_string(), 80e6);
    m.insert("leaf".to_string(), 50e6);
    let eff = combine_effective(&nodes, &m);
    assert_eq!(eff["main"], 100e6);
    assert_eq!(eff["hk"], 80e6);
    // leaf 经 hk 转发：自身 50 < hk 80 → 50
    assert_eq!(eff["leaf"], 50e6);
    // 父只有 20：子封顶 20
    let mut m2 = HashMap::new();
    m2.insert("main".to_string(), 20e6);
    m2.insert("hk".to_string(), 80e6);
    let eff2 = combine_effective(&nodes, &m2);
    assert_eq!(eff2["hk"], 20e6);
}

#[test]
fn test_probe_order_main_first_parent_before_child() {
    let nodes = vec![
        node("leaf", Some("hk")),
        node("hk", Some("main")),
        node("main", None),
        node("other", None),
    ];
    let order = probe_order(&nodes, "main");
    assert_eq!(order[0], "main");
    let pos = |id: &str| order.iter().position(|x| x == id).unwrap();
    assert!(pos("hk") < pos("leaf"));
    assert!(pos("main") < pos("hk"));
}

#[test]
fn test_full_transfer_multi_node() {
    let nodes = vec![node("main", None), node("hk", Some("main"))];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let mut p = run_probes(p, &[]);

    let chunk = p.planned_chunk_size().unwrap();
    assert!((MIN_CHUNK..=MAX_CHUNK).contains(&chunk));
    let windows = p.windows();
    assert_eq!(windows.len(), 2);
    assert!(windows.iter().all(|(_, w)| *w >= 1 && *w <= 32));

    // 驱动到完成：每派一片立即回报成功，统计节点使用
    p.begin_transfer("u1", &[0, 1, 2, 3, 4, 5]);
    let mut stats: HashMap<String, usize> = HashMap::new();
    let mut guard = 0;
    loop {
        guard += 1;
        assert!(guard < 10000, "传输阶段死循环");
        match p.next_task() {
            Task::Chunk(t) => {
                *stats.entry(t.node.clone()).or_default() += 1;
                p.report(Report::ChunkOk {
                    seq: t.seq,
                    bytes: t.bytes,
                    elapsed_ms: 500,
                });
            }
            Task::Done => break,
            Task::Failed { reason } => panic!("不应失败: {}", reason),
            Task::Wait => panic!("每派即报不应出现 Wait"),
            other => panic!("意外任务: {:?}", other),
        }
    }
    assert!(
        stats.get("main").copied().unwrap_or(0) >= 1,
        "主节点应被用到"
    );
    assert!(stats.get("hk").copied().unwrap_or(0) >= 1, "hk 应被用到");
}

#[test]
fn test_probes_run_during_transfer() {
    // 边传边测：不再需要先跑探测，begin_transfer 后主节点按启动窗口立即发片；
    // 窗口占满时顺手把后台探测派出去。
    let nodes = vec![node("main", None), node("hk", Some("main"))];
    let mut p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let chunk = p.planned_chunk_size().unwrap();
    assert_eq!(chunk, DEFAULT_UPLOAD_CHUNK);

    p.begin_transfer("u1", &(0..8).collect::<Vec<_>>());
    let mut pending: Vec<ChunkTask> = Vec::new();
    let mut chunks = 0;
    let mut probes = 0;
    let mut guard = 0;
    loop {
        guard += 1;
        assert!(guard < 1000, "死循环");
        match p.next_task() {
            Task::Chunk(t) => {
                chunks += 1;
                pending.push(t); // 先攒着不回报，模拟在途以逼出窗口占满
                if pending.len() >= 4 {
                    let t = pending.remove(0);
                    p.report(Report::ChunkOk {
                        seq: t.seq,
                        bytes: t.bytes,
                        elapsed_ms: 100,
                    });
                }
            }
            Task::Probe(t) => {
                probes += 1;
                let (elapsed, bytes) = match t.kind {
                    ProbeKind::Tiny => (20, 0u64),
                    ProbeKind::Throughput => (1600, PROBE_THROUGHPUT_BYTES),
                };
                p.report(Report::ProbeOk {
                    node: t.node,
                    kind: t.kind,
                    bytes,
                    elapsed_ms: elapsed,
                    backend_node: "backend-1".into(),
                });
            }
            Task::Done => break,
            Task::Failed { reason } => panic!("不应失败: {}", reason),
            Task::Reinit => panic!("不应重 init"),
            Task::Recover { .. } => panic!("不应进入恢复"),
            Task::Wait => {
                let t = pending
                    .first()
                    .cloned()
                    .expect("Wait 时既无任务又无在途分片");
                pending.remove(0);
                p.report(Report::ChunkOk {
                    seq: t.seq,
                    bytes: t.bytes,
                    elapsed_ms: 100,
                });
            }
        }
    }
    assert_eq!(chunks, 8);
    assert!(probes >= 1, "传输期应把后台探测也派出去");
}

#[test]
fn test_drr_weight_exponent_balances_links() {
    // 两节点带宽悬殊（main ≈21MB/s、hk ≈0.5MB/s），验证 weight_exponent 对分片分配的影响。
    let run = |exp: f64| -> (usize, usize) {
        let nodes = vec![node("main", None), node("hk", Some("main"))];
        let mut inp = input(nodes, "main", 1_000_000);
        inp.weight_exponent = exp;
        let mut p = Planner::new(inp).unwrap();
        loop {
            match p.next_task() {
                Task::Probe(t) => {
                    let (elapsed, bytes) = if t.kind == ProbeKind::Throughput {
                        (
                            if t.node == "main" { 100 } else { 4000 },
                            PROBE_THROUGHPUT_BYTES,
                        )
                    } else {
                        (20, 0u64)
                    };
                    p.report(Report::ProbeOk {
                        node: t.node,
                        kind: t.kind,
                        bytes,
                        elapsed_ms: elapsed,
                        backend_node: "backend-1".into(),
                    });
                }
                Task::Wait => break,
                other => panic!("探测阶段意外: {:?}", other),
            }
        }
        p.begin_transfer("u1", &(0..60).collect::<Vec<_>>());
        let mut counts: HashMap<String, usize> = HashMap::new();
        let mut guard = 0;
        loop {
            guard += 1;
            assert!(guard < 100000, "死循环");
            match p.next_task() {
                Task::Chunk(t) => {
                    *counts.entry(t.node.clone()).or_default() += 1;
                    p.report(Report::ChunkOk {
                        seq: t.seq,
                        bytes: t.bytes,
                        elapsed_ms: 50,
                    });
                }
                Task::Done => break,
                Task::Failed { reason } => panic!("不应失败: {}", reason),
                Task::Reinit => panic!("不应 reinit"),
                Task::Probe(t) => panic!("探测已在 begin 前派完: {:?}", t),
                Task::Recover { .. } => panic!("不应进入恢复"),
                Task::Wait => panic!("每派即报不应 Wait"),
            }
        }
        (
            counts.get("main").copied().unwrap_or(0),
            counts.get("hk").copied().unwrap_or(0),
        )
    };
    let (_, h0) = run(0.0);
    let (_, h1) = run(1.0);
    // exponent=0（均分）：慢链路也拿到接近一半
    assert!(h0 >= 24, "exponent=0 慢链路应接近均分: hk={}", h0);
    // exponent=1（严格按带宽）：慢链路只拿到零头
    assert!(h1 <= 8, "exponent=1 慢链路应被压缩: hk={}", h1);
}

#[test]
fn test_congestion_halves_window_and_requeues() {
    // 大吞吐探针（2MiB/100ms ≈ 21MB/s → 初始窗口 ≥2）才能测出减半
    let nodes = vec![node("main", None)];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let mut p = run_probes_with(p, &[], 100);
    let w0 = p.windows()[0].1;
    assert!(w0 >= 2, "w0={}", w0);

    p.begin_transfer("u1", &[0, 1]);
    let t1 = chunk_or_panic(p.next_task());
    let t2 = chunk_or_panic(p.next_task());
    p.report(Report::ChunkCongested { seq: t1.seq });
    p.report(Report::ChunkCongested { seq: t2.seq });
    assert_eq!(p.windows()[0].1, (w0 / 2).max(1));
    // 两片都应重排（预算 3 → 剩 1）
    let t3 = chunk_or_panic(p.next_task());
    assert!(t3.index == t1.index || t3.index == t2.index);
}

#[test]
fn test_session_gone_triggers_reinit_then_resume() {
    let nodes = vec![node("main", None)];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0, 1]);
    let t1 = chunk_or_panic(p.next_task());
    p.report(Report::ChunkSessionGone { seq: t1.seq });
    match p.next_task() {
        Task::Reinit => {}
        other => panic!("应要求 reinit: {:?}", other),
    }
    p.resume("u2", &[0, 1]);
    match p.next_task() {
        Task::Chunk(t) => assert_eq!(t.node, "main"),
        other => panic!("resume 后应继续派: {:?}", other),
    }
}

#[test]
fn test_retry_exhaustion_fails() {
    let nodes = vec![node("main", None)];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let mut p = run_probes(p, &[]);
    p.begin_transfer("u1", &[0]);
    // 预算 3 次：3 次数据错误后 Failed
    for _ in 0..CHUNK_ATTEMPTS {
        match p.next_task() {
            Task::Chunk(t) => p.report(Report::ChunkDataError { seq: t.seq }),
            other => panic!("应派发: {:?}", other),
        }
    }
    match p.next_task() {
        Task::Failed { reason } => assert!(reason.contains("重试耗尽")),
        other => panic!("应失败: {:?}", other),
    }
}

#[test]
fn test_all_probe_failed_bootstraps_main() {
    // 探测全失败也不再直接判死：主节点（init 锚点，已证明可达）先给启动窗口，
    // 靠运行期 AIMD/重试兜底；其余测不出/不符的节点出池。
    let nodes = vec![node("main", None), node("hk", Some("main"))];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let p = run_probes(p, &["main", "hk"]);
    let w = |id: &str| p.windows().into_iter().find(|(x, _)| x == id).unwrap().1;
    assert!(w("main") >= 1, "主节点应 bootstrap 出启动窗口");
    assert_eq!(w("hk"), 0, "探测失败的子节点应出池");
}

#[test]
fn test_backend_mismatch_excluded() {
    let nodes = vec![node("main", None), node("hk", Some("main"))];
    let p = Planner::new(input(nodes, "main", 1_000_000)).unwrap();
    let p = run_probes(p, &[]);
    // 人为制造不一致：把 hk 的吞吐结果换成另一个 backend_node
    // （run_probes 已跑完，直接重新构造场景：main 有新鲜状态、hk 现场探测）
    let mut inp = input(vec![node("main", None)], "main", 2_000_000);
    inp.state = p.export_state(); // main 新鲜
    inp.nodes = vec![node("main", None), node("hk", Some("main"))];
    let p2 = Planner::new(inp).unwrap();
    // hk 探测响应 backend 不同 → 出池
    let mut guard = 0;
    let mut p2 = p2;
    loop {
        guard += 1;
        assert!(guard < 50);
        match p2.next_task() {
            Task::Probe(t) => {
                let backend = if t.node == "hk" {
                    "evil-other"
                } else {
                    "backend-1"
                };
                let (elapsed, bytes) = match t.kind {
                    ProbeKind::Tiny => (20, 0u64),
                    ProbeKind::Throughput => (1600, PROBE_THROUGHPUT_BYTES),
                };
                p2.report(Report::ProbeOk {
                    node: t.node.clone(),
                    kind: t.kind,
                    bytes,
                    elapsed_ms: elapsed,
                    backend_node: backend.into(),
                });
            }
            Task::Wait => break,
            other => panic!("意外: {:?}", other),
        }
    }
    let windows = p2.windows();
    assert_eq!(windows.iter().find(|(id, _)| id == "hk").unwrap().1, 0);
    assert!(windows.iter().find(|(id, _)| id == "main").unwrap().1 >= 1);
}

#[test]
fn cached_identity_and_timestamp_survive_reuse() {
    let p = run_probes(
        Planner::new(input(
            vec![node("main", None), node("other", None)],
            "main",
            1_000_000,
        ))
        .unwrap(),
        &[],
    );
    let mut i = input(
        vec![node("main", None), node("other", None)],
        "main",
        1_100_000,
    );
    i.state = p.export_state();
    i.state
        .nodes
        .iter_mut()
        .find(|s| s.id == "other")
        .unwrap()
        .backend_node = "different".into();
    let mut cached = Planner::new(i).unwrap();
    cached.begin_transfer("session", &[0, 1]);
    assert_eq!(
        cached
            .windows()
            .iter()
            .find(|(id, _)| id == "other")
            .unwrap()
            .1,
        0
    );
    let state = cached.export_state();
    assert_eq!(
        state.nodes[0].measured_at_ms, 1_000_000,
        "复用不能刷新样本时间"
    );
    assert_eq!(state.nodes[0].backend_node, "backend-1");
    let mut expired = input(vec![node("main", None)], "main", 1_700_001);
    expired.state = state;
    assert!(matches!(
        Planner::new(expired).unwrap().next_task(),
        Task::Probe(_)
    ));
}

#[test]
fn cache_rejects_changed_url_future_clock_and_invalid_numbers() {
    let p = run_probes(
        Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap(),
        &[],
    );
    for case in 0..5 {
        let mut i = input(vec![node("main", None)], "main", 1_100_000);
        i.state = p.export_state();
        match case {
            0 => i.nodes[0].url = "https://changed".into(),
            1 => i.state.nodes[0].measured_at_ms = 1_200_000,
            2 => i.state.nodes[0].rtt_ms = f64::NAN,
            3 => i.state.nodes[0].bps = -1.0,
            _ => i.state.nodes[0].backend_node.clear(),
        }
        assert!(matches!(
            Planner::new(i).unwrap().next_task(),
            Task::Probe(_)
        ));
    }
}

#[test]
fn secondary_waits_for_main_identity_even_when_probes_finish_first() {
    let mut p = Planner::new(input(
        vec![node("main", None), node("other", None)],
        "main",
        1_000_000,
    ))
    .unwrap();
    let mut probes = vec![];
    while let Task::Probe(t) = p.next_task() {
        probes.push(t);
    }
    for t in probes.iter().filter(|t| t.node == "other") {
        p.report(Report::ProbeOk {
            node: t.node.clone(),
            kind: t.kind,
            bytes: t.bytes,
            elapsed_ms: 20,
            backend_node: "other-backend".into(),
        });
    }
    p.begin_transfer("session", &[0, 1]);
    assert_eq!(chunk_or_panic(p.next_task()).node, "main");
    assert_eq!(
        p.windows().iter().find(|(id, _)| id == "other").unwrap().1,
        0
    );
    for t in probes.iter().filter(|t| t.node == "main") {
        p.report(Report::ProbeOk {
            node: t.node.clone(),
            kind: t.kind,
            bytes: t.bytes,
            elapsed_ms: 20,
            backend_node: "main-backend".into(),
        });
    }
    assert_eq!(
        p.windows().iter().find(|(id, _)| id == "other").unwrap().1,
        0
    );
}

#[test]
fn late_throughput_cannot_revive_failed_tiny_probe() {
    let mut p = Planner::new(input(
        vec![node("main", None), node("other", None)],
        "main",
        1_000_000,
    ))
    .unwrap();
    let mut probes = vec![];
    while let Task::Probe(t) = p.next_task() {
        probes.push(t);
    }
    for t in probes.iter().filter(|t| t.node == "main") {
        p.report(Report::ProbeOk {
            node: t.node.clone(),
            kind: t.kind,
            bytes: t.bytes,
            elapsed_ms: 20,
            backend_node: "backend-1".into(),
        });
    }
    p.report(Report::ProbeFailed {
        node: "other".into(),
        kind: ProbeKind::Tiny,
    });
    p.report(Report::ProbeOk {
        node: "other".into(),
        kind: ProbeKind::Throughput,
        bytes: PROBE_THROUGHPUT_BYTES,
        elapsed_ms: 20,
        backend_node: "backend-1".into(),
    });
    assert_eq!(
        p.windows().iter().find(|(id, _)| id == "other").unwrap().1,
        0
    );
    assert!(!p.export_state().nodes.iter().any(|s| s.id == "other"));
}

#[test]
fn resume_requeues_aborted_probes_and_ignores_old_chunks() {
    let mut p = Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap();
    p.begin_transfer("old", &[0]);
    assert!(matches!(p.next_task(), Task::Probe(_)));
    assert!(matches!(p.next_task(), Task::Probe(_)));
    let old = chunk_or_panic(p.next_task());
    p.report(Report::ChunkSessionGone { seq: old.seq });
    p.resume("new", &[0]);
    p.report(Report::ChunkOk {
        seq: old.seq,
        bytes: old.bytes,
        elapsed_ms: 10,
    });
    assert!(matches!(p.next_task(), Task::Probe(_)));
    assert!(p.has_pending_for_test(0));
}

#[test]
fn healthy_wan_can_increase_window_when_chunk_time_exceeds_rtt() {
    let mut p = run_probes(
        Planner::new(input(vec![node("main", None)], "main", 1_000_000)).unwrap(),
        &[],
    );
    p.begin_transfer("session", &[0, 1, 2, 3, 4]);
    let before = p.windows()[0].1;
    for _ in 0..STREAK_TO_INCREASE {
        let t = chunk_or_panic(p.next_task());
        p.report(Report::ChunkOk {
            seq: t.seq,
            bytes: t.bytes,
            elapsed_ms: 3200,
        });
    }
    assert!(
        p.windows()[0].1 > before,
        "健康大分片不能因耗时超过 RTT 被永久锁在初始窗口"
    );
}

#[test]
fn test_cabi_json_roundtrip() {
    let inp = input(vec![node("main", None)], "main", 1_000_000);
    let desc = serde_json::to_string(&inp).unwrap();
    let h = fc_planner_new(CString::new(desc).unwrap().as_ptr());
    assert!(!h.is_null());
    let t = unsafe { std::ffi::CStr::from_ptr(fc_planner_next(h)) };
    let task: Task = serde_json::from_str(t.to_str().unwrap()).unwrap();
    assert!(matches!(task, Task::Probe(_)));
    // 免费泄漏无关紧要（测试进程）
    fc_planner_free(h);
}
