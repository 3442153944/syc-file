use super::logging::fmt_bps;
use super::session::is_temporary_error;
use super::{Client, Response};
use crate::net_core::upload_planner as planner;
use std::path::Path;
use std::time::{Duration, Instant};

/// 单片超过 10 秒即放弃当前链路，由调度器交给其他可用节点。
const CHUNK_FAILOVER_TIMEOUT: Duration = Duration::from_secs(10);

/// 平台侧执行结果：正常 Report 喂回 planner；Fatal 是平台级失败（取消 / 读文件失败），
/// 没法也不应交给 planner 重排，整趟上传直接失败。
pub(super) enum Outcome {
    Report(planner::Report),
    Fatal(String),
}

/// 测速接口响应 data 段（/v1/net/speedtest/upload）。
#[derive(Debug, serde::Deserialize, Default)]
#[allow(dead_code)]
struct SpeedtestData {
    /// 服务端自报节点名：planner 的同后端校验依据。
    #[serde(default)]
    node: String,
    #[serde(default)]
    bytes: u64,
    #[serde(default)]
    server_ms: i64,
}

/// 执行一个探测任务：POST planner 指定节点的测速接口，把响应当 RTT/带宽样本回报。
pub(super) async fn run_probe(client: &Client, t: &planner::ProbeTask) -> Outcome {
    let kind = if t.kind == planner::ProbeKind::Tiny {
        "rtt"
    } else {
        "bw"
    };
    let body = vec![0u8; t.bytes as usize];
    let bytes_q = t.bytes.to_string();
    let params = [("bytes", bytes_q.as_str())];
    let started = Instant::now();
    let resp: Result<Response<SpeedtestData>, String> = client
        .post_bytes_to(
            &t.url,
            "/net/speedtest/upload",
            &params,
            body,
            Some(Duration::from_secs(30)),
        )
        .await;
    match resp {
        Err(e) if client.host().is_cancelled_error(&e) => Outcome::Fatal(e),
        Err(e) => {
            client.warn(
                "upload",
                format!("[upload] 探测失败 node={} kind={}: {}", t.node, kind, e),
            );
            Outcome::Report(planner::Report::ProbeFailed {
                node: t.node.clone(),
                kind: t.kind,
            })
        }
        Ok(r) => {
            if r.is_ok() {
                let data = r.data.unwrap_or_default();
                let elapsed_ms = started.elapsed().as_millis().max(1) as u64;
                if t.kind == planner::ProbeKind::Throughput {
                    let bps = t.bytes as f64 / (elapsed_ms as f64 / 1000.0);
                    client.info(
                        "upload",
                        format!(
                            "[upload] 探测 node={} bw={}/s ({}ms) backend={}",
                            t.node,
                            fmt_bps(bps),
                            elapsed_ms,
                            if data.node.is_empty() {
                                "?"
                            } else {
                                &data.node
                            }
                        ),
                    );
                } else {
                    client.info(
                        "upload",
                        format!(
                            "[upload] 探测 node={} rtt={}ms backend={}",
                            t.node,
                            elapsed_ms,
                            if data.node.is_empty() {
                                "?"
                            } else {
                                &data.node
                            }
                        ),
                    );
                }
                Outcome::Report(planner::Report::ProbeOk {
                    node: t.node.clone(),
                    kind: t.kind,
                    bytes: t.bytes,
                    elapsed_ms,
                    backend_node: data.node,
                })
            } else {
                client.warn(
                    "upload",
                    format!(
                        "[upload] 探测失败 node={} kind={}: [{}] {}",
                        t.node, kind, r.code, r.message
                    ),
                );
                Outcome::Report(planner::Report::ProbeFailed {
                    node: t.node.clone(),
                    kind: t.kind,
                })
            }
        }
    }
}

/// 执行一个分片任务：定位读出字节 → POST 到 planner 指定的节点 URL → 分类成 Report。
/// 2xx→ChunkOk；422→ChunkDataError；404→ChunkSessionGone；10 秒期限、网络错误或
/// 502/503/504→ChunkUnavailable。平台级取消/读文件失败→Fatal。
pub(super) async fn execute_chunk(
    client: &Client,
    file: &Path,
    upload_id: &str,
    chunk_size: i64,
    total_size: i64,
    t: &planner::ChunkTask,
    attempt_id: &str,
) -> Outcome {
    let data = match crate::file_core::file::read_chunk(
        file,
        t.index as i32,
        chunk_size as usize,
        total_size,
    ) {
        Ok(data) => data,
        Err(e) => return Outcome::Fatal(format!("读取分片失败: {e}")),
    };
    let len = data.len();
    let started = Instant::now();
    let resp = tokio::time::timeout(
        CHUNK_FAILOVER_TIMEOUT,
        client.upload_chunk(
            Some(&t.url),
            upload_id,
            t.index as i32,
            data,
            Some(Duration::from_millis(t.timeout_ms)),
            attempt_id,
        ),
    )
    .await;
    let resp = match resp {
        Ok(resp) => resp,
        Err(_) => {
            client.warn(
                "upload",
                format!(
                    "[upload] node={} chunk={} 超过10秒未完成（{}B），取消该节点在途分片并切换链路",
                    t.node, t.index, len
                ),
            );
            return Outcome::Report(planner::Report::ChunkUnavailable { seq: t.seq });
        }
    };
    let elapsed_ms = started.elapsed().as_millis().max(1) as u64;
    match resp {
        Err(e) if client.host().is_cancelled_error(&e) => Outcome::Fatal(e),
        Err(e) if is_temporary_error(client, &e) => {
            client.warn(
                "upload",
                format!(
                    "[upload] node={} chunk={} 链路失败 {}ms {}B，切换节点: {}",
                    t.node, t.index, elapsed_ms, len, e
                ),
            );
            Outcome::Report(planner::Report::ChunkUnavailable { seq: t.seq })
        }
        Err(e) => {
            client.warn(
                "upload",
                format!(
                    "[upload] node={} chunk={} HTTP/解析错误 {}ms {}B，按拥塞处理: {}",
                    t.node, t.index, elapsed_ms, len, e
                ),
            );
            Outcome::Report(planner::Report::ChunkCongested { seq: t.seq })
        }
        Ok(resp) => {
            if resp.is_ok() {
                if elapsed_ms > 5_000 {
                    client.warn(
                        "upload",
                        format!(
                            "[upload] node={} chunk={} 慢片 {}ms {}B（>5s，可能链路拥塞）",
                            t.node, t.index, elapsed_ms, len
                        ),
                    );
                }
                Outcome::Report(planner::Report::ChunkOk {
                    seq: t.seq,
                    bytes: len as u64,
                    elapsed_ms,
                })
            } else {
                match resp.code {
                    422 => Outcome::Report(planner::Report::ChunkDataError { seq: t.seq }),
                    404 => Outcome::Report(planner::Report::ChunkSessionGone { seq: t.seq }),
                    502 | 503 | 504 => {
                        Outcome::Report(planner::Report::ChunkUnavailable { seq: t.seq })
                    }
                    _ => {
                        client.warn(
                            "upload",
                            format!(
                                "[upload] node={} chunk={} 业务码={} 按拥塞处理: {}",
                                t.node, t.index, resp.code, resp.message
                            ),
                        );
                        Outcome::Report(planner::Report::ChunkCongested { seq: t.seq })
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod execution_tests {
    use super::*;
    use crate::net_core::upload::{HostFuture, UploadHost, UploadRequest};
    use std::sync::Arc;

    struct Hanging;

    impl UploadHost for Hanging {
        fn request(&self, _: UploadRequest) -> HostFuture<'_, Response<serde_json::Value>> {
            Box::pin(std::future::pending())
        }

        fn server_url(&self) -> &str {
            "http://unit.invalid"
        }

        fn for_upload_session(&self) -> Arc<dyn UploadHost> {
            Arc::new(Self)
        }

        fn probe_share_url<'a>(&'a self, _: &'a str) -> HostFuture<'a, ()> {
            Box::pin(std::future::pending())
        }
    }

    #[tokio::test(start_paused = true)]
    async fn response_body_stall_is_abandoned_after_ten_seconds() {
        let file = std::env::temp_dir().join(format!("sys-core-stall-{}", uuid::Uuid::new_v4()));
        std::fs::write(&file, b"chunk").unwrap();
        let client = Client::new(Arc::new(Hanging));
        let task = planner::ChunkTask {
            seq: 7,
            node: "stalled".into(),
            url: "http://unit.invalid".into(),
            index: 0,
            timeout_ms: 120_000,
            bytes: 5,
        };
        let started = tokio::time::Instant::now();
        let result = execute_chunk(&client, &file, "session", 5, 5, &task, "attempt-7").await;
        std::fs::remove_file(file).unwrap();
        assert!(matches!(
            result,
            Outcome::Report(planner::Report::ChunkUnavailable { seq: 7 })
        ));
        assert_eq!(started.elapsed(), CHUNK_FAILOVER_TIMEOUT);
    }
}
