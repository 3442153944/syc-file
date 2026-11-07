use super::protocol::{UploadCompleteParams, UploadInitData, UploadInitParams};
use super::{Client, Response, SYNC_CONFLICT};
use crate::file_core::file::Description;
use std::collections::HashSet;
use std::time::Duration;

const RECOVERY_RETRY_DELAY: Duration = Duration::from_millis(300);

/// 网关暂时不可用；其它 HTTP/业务码必须交回调用方失败，不能无限重试。
pub(super) fn is_temporary_status(code: i32) -> bool {
    matches!(code, 502 | 503 | 504)
}

/// 统一识别网络/网关暂时错误。取消、HTTP 业务错误和响应解析错误都不是暂时网络故障。
pub(super) fn is_temporary_error(client: &Client, error: &str) -> bool {
    if client.host().is_cancelled_error(error) || error.starts_with("JSON 解析失败") {
        return false;
    }
    match error.strip_prefix("HTTP ") {
        Some(rest) => rest
            .split(|c: char| !c.is_ascii_digit())
            .next()
            .and_then(|code| code.parse::<i32>().ok())
            .is_some_and(is_temporary_status),
        None => true,
    }
}

/// 只等待，不创建额外任务；取消上传 future 会立即取消这里的 sleep。
async fn wait_for_retry() {
    tokio::time::sleep(RECOVERY_RETRY_DELAY).await;
}

fn init_params(
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
    sync_base: Option<&str>,
    quick_share: bool,
) -> UploadInitParams {
    UploadInitParams {
        path: if quick_share {
            String::new()
        } else {
            remote_dir.to_string()
        },
        name: name.to_string(),
        total_size: desc.total_size,
        chunk_size: desc.chunk_size,
        chunk_count: desc.chunk_count,
        merkle_root: desc.merkle_root_hex.clone(),
        file_hash: desc.file_hash_hex.clone(),
        leaf_hashes: desc.leaf_hashes_hex.clone(),
        device_id: device_id.to_string(),
        on_conflict: on_conflict.to_string(),
        sync: sync_base.is_some(),
        base_hash: sync_base.unwrap_or("").to_string(),
        quick_share,
    }
}

fn parse_init_response(
    resp: Response<UploadInitData>,
    desc: &Description,
) -> Result<UploadInitData, String> {
    if resp.is_ok() {
        let data: UploadInitData = resp.data.ok_or("init 响应为空")?;
        if !data.instant {
            if data.upload_id.is_empty()
                || (data.chunk_size > 0 && data.chunk_size != desc.chunk_size)
                || (data.chunk_count > 0 && data.chunk_count != desc.chunk_count)
            {
                return Err("init 会话参数与文件描述不一致".into());
            }
            let mut seen = HashSet::new();
            if data
                .missing
                .iter()
                .any(|&i| i < 0 || i >= desc.chunk_count || !seen.insert(i))
            {
                return Err("init 缺失分片列表无效".into());
            }
        }
        Ok(data)
    } else if resp.code == 409 {
        Err(format!("{}: {}", SYNC_CONFLICT, resp.message))
    } else {
        Err(format!("init 失败: {}", resp.message))
    }
}

#[allow(clippy::too_many_arguments)]
async fn call_init_impl(
    client: &Client,
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
    sync_base: Option<&str>,
    quick_share: bool,
    recover_temporary: bool,
) -> Result<UploadInitData, String> {
    let params = init_params(
        name,
        remote_dir,
        desc,
        device_id,
        on_conflict,
        sync_base,
        quick_share,
    );
    loop {
        let result: Result<Response<UploadInitData>, String> =
            client.post("/file/upload/init", &params).await;
        match result {
            Err(error) if recover_temporary && is_temporary_error(client, &error) => {
                client.warn(
                    "upload",
                    format!("[upload] reinit 暂时失败，300ms 后重试: {error}"),
                );
                wait_for_retry().await;
            }
            Err(error) => return Err(error),
            Ok(resp) if recover_temporary && is_temporary_status(resp.code) => {
                client.warn(
                    "upload",
                    format!(
                        "[upload] reinit 暂时失败（{}），300ms 后重试: {}",
                        resp.code, resp.message
                    ),
                );
                wait_for_retry().await;
            }
            Ok(resp) => return parse_init_response(resp, desc),
        }
    }
}

/// 固定路径和自适应首次 init 保持单次请求行为。
#[allow(clippy::too_many_arguments)]
pub(super) async fn call_init(
    client: &Client,
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
    sync_base: Option<&str>,
    quick_share: bool,
) -> Result<UploadInitData, String> {
    call_init_impl(
        client,
        name,
        remote_dir,
        desc,
        device_id,
        on_conflict,
        sync_base,
        quick_share,
        false,
    )
    .await
}

/// 自适应 reinit 可持续等待网络恢复；明确业务错误仍立即返回。
#[allow(clippy::too_many_arguments)]
pub(super) async fn call_init_recovering(
    client: &Client,
    name: &str,
    remote_dir: &str,
    desc: &Description,
    device_id: &str,
    on_conflict: &str,
    sync_base: Option<&str>,
    quick_share: bool,
) -> Result<UploadInitData, String> {
    call_init_impl(
        client,
        name,
        remote_dir,
        desc,
        device_id,
        on_conflict,
        sync_base,
        quick_share,
        true,
    )
    .await
}

/// 固定路径收尾：只打激活入口，失败直接返回，保持旧行为。
pub(super) async fn complete_upload(
    client: &Client,
    upload_id: &str,
    device_id: &str,
) -> Result<serde_json::Value, String> {
    let params = UploadCompleteParams {
        upload_id: upload_id.to_string(),
        device_id: device_id.to_string(),
    };
    let resp: Response<serde_json::Value> = client.post("/file/upload/complete", &params).await?;
    if resp.is_ok() {
        resp.data.ok_or_else(|| "complete 响应为空".into())
    } else {
        Err(format!("complete 失败: {}", resp.message))
    }
}

#[derive(Debug)]
pub(super) struct CompleteError {
    message: String,
    bad_index: Option<i64>,
}

impl CompleteError {
    fn new(message: String) -> Self {
        Self {
            message,
            bad_index: None,
        }
    }

    pub(super) fn retryable_chunk(&self) -> Option<i64> {
        self.bad_index.filter(|&index| index >= 0)
    }

    pub(super) fn into_message(self) -> String {
        self.message
    }
}

/// 自适应普通 complete 可在同后端入口间恢复重试。
pub(super) async fn complete_upload_recovering(
    client: &Client,
    upload_id: &str,
    device_id: &str,
    candidates: Vec<String>,
) -> Result<serde_json::Value, CompleteError> {
    complete_with_candidates(client, upload_id, device_id, candidates, false).await
}

/// 快传 complete 也可跨同后端入口重试。后端保留完成结果，响应丢失不会生成第二个分享；
/// 没有总时间上限，网络恢复或调用方取消前持续等待。
pub(super) async fn complete_quick_share(
    client: &Client,
    upload_id: &str,
    device_id: &str,
    candidates: Vec<String>,
) -> Result<serde_json::Value, CompleteError> {
    complete_with_candidates(client, upload_id, device_id, candidates, true).await
}

async fn complete_with_candidates(
    client: &Client,
    upload_id: &str,
    device_id: &str,
    mut candidates: Vec<String>,
    quick_share: bool,
) -> Result<serde_json::Value, CompleteError> {
    candidates.sort_by_key(|url| !url.starts_with("https://"));
    let mut seen = HashSet::new();
    candidates.retain(|url| seen.insert(url.clone()));
    if candidates.is_empty() {
        return Err(CompleteError::new("没有同后端的可用上传收尾节点".into()));
    }
    let params = UploadCompleteParams {
        upload_id: upload_id.into(),
        device_id: device_id.into(),
    };
    loop {
        let mut last_error = String::new();
        for base in &candidates {
            match client
                .post_json_to_timeout::<serde_json::Value, _>(
                    base,
                    "/file/upload/complete",
                    &params,
                    Duration::from_secs(10),
                )
                .await
            {
                Ok(resp) if resp.is_ok() => {
                    let mut data = resp
                        .data
                        .ok_or_else(|| CompleteError::new("complete 响应为空".into()))?;
                    if quick_share {
                        data["entry_url"] = serde_json::json!(base.trim_end_matches('/'));
                        data["entry_candidates"] = serde_json::json!(candidates);
                    }
                    return Ok(data);
                }
                Ok(resp) if is_temporary_status(resp.code) => {
                    last_error = format!("[{}] {}", resp.code, resp.message);
                }
                Ok(resp) => {
                    let bad_index = (resp.code == 422)
                        .then(|| resp.data.as_ref()?.get("bad_index")?.as_i64())
                        .flatten();
                    return Err(CompleteError {
                        message: format!("complete 失败: [{}] {}", resp.code, resp.message),
                        bad_index,
                    });
                }
                Err(error) if is_temporary_error(client, &error) => last_error = error,
                Err(error) => return Err(CompleteError::new(format!("complete 失败: {error}"))),
            }
        }
        client.warn(
            "upload",
            format!("[upload] complete 暂时失败，300ms 后重试: {last_error}"),
        );
        wait_for_retry().await;
    }
}

/// ChunkUnavailable 在喂回 planner 前必须确认旧尝试已放弃。此函数不返回成功就绝不会
/// 让主循环派发新尝试；暂时性网络/网关失败按短间隔重试，业务错误立即失败。
pub(super) async fn confirm_abandon(
    client: &Client,
    urls: &[String],
    body: &serde_json::Value,
) -> Result<(), String> {
    if urls.is_empty() {
        return Err("无法确认后端已放弃旧分片尝试：没有可用入口".into());
    }
    loop {
        let mut last_error = String::new();
        for url in urls {
            match client
                .post_json_to::<serde_json::Value, _>(url, "/file/upload/abandon", body)
                .await
            {
                Ok(resp) if resp.is_ok() => return Ok(()),
                Ok(resp) if is_temporary_status(resp.code) => {
                    last_error = format!("[{}] {}", resp.code, resp.message);
                }
                Ok(resp) => {
                    return Err(format!(
                        "abandon 失败，停止上传以避免迟到写入: [{}] {}",
                        resp.code, resp.message
                    ));
                }
                Err(error) if is_temporary_error(client, &error) => last_error = error,
                Err(error) => {
                    return Err(format!("abandon 失败，停止上传以避免迟到写入: {error}"));
                }
            }
        }
        client.warn(
            "upload",
            format!("[upload] abandon 暂时失败，300ms 后重试: {last_error}"),
        );
        wait_for_retry().await;
    }
}

#[cfg(test)]
mod recovery_tests {
    use super::*;
    use crate::net_core::upload::{HostFuture, UploadHost, UploadRequest};
    use serde_json::{json, Value};
    use std::collections::VecDeque;
    use std::sync::{Arc, Mutex};

    struct SequenceHost {
        responses: Mutex<VecDeque<Result<Response<Value>, String>>>,
    }

    impl SequenceHost {
        fn new() -> Self {
            Self {
                responses: Mutex::new(VecDeque::new()),
            }
        }

        fn push(&self, response: Result<Response<Value>, String>) {
            self.responses.lock().unwrap().push_back(response);
        }

        fn is_empty(&self) -> bool {
            self.responses.lock().unwrap().is_empty()
        }
    }

    impl UploadHost for SequenceHost {
        fn request(&self, _: UploadRequest) -> HostFuture<'_, Response<Value>> {
            let response = self
                .responses
                .lock()
                .unwrap()
                .pop_front()
                .expect("unexpected request");
            Box::pin(async move { response })
        }

        fn server_url(&self) -> &str {
            "http://unit.invalid"
        }

        fn for_upload_session(&self) -> Arc<dyn UploadHost> {
            unreachable!()
        }

        fn probe_share_url<'a>(&'a self, _: &'a str) -> HostFuture<'a, ()> {
            Box::pin(async { Ok(()) })
        }
    }

    fn response(code: i32) -> Response<Value> {
        Response {
            code,
            message: format!("status {code}"),
            data: (code == 200).then(|| json!({"ok": true})),
        }
    }

    #[tokio::test(start_paused = true)]
    async fn abandon_retries_temporary_network_failure() {
        let host = Arc::new(SequenceHost::new());
        host.push(Err("connection reset".into()));
        host.push(Ok(response(200)));
        let client = Client::new(host.clone());
        confirm_abandon(
            &client,
            &["http://unit.invalid".into()],
            &json!({"upload_id": "u1", "attempts": []}),
        )
        .await
        .unwrap();
        assert!(host.is_empty());
    }

    #[tokio::test(start_paused = true)]
    async fn abandon_does_not_retry_business_failure() {
        let host = Arc::new(SequenceHost::new());
        host.push(Ok(response(400)));
        let client = Client::new(host.clone());
        let error = confirm_abandon(
            &client,
            &["http://unit.invalid".into()],
            &json!({"upload_id": "u1", "attempts": []}),
        )
        .await
        .unwrap_err();
        assert!(error.contains("400"));
        assert!(host.is_empty());
    }

    #[tokio::test(start_paused = true)]
    async fn reinit_retries_temporary_network_failure() {
        let host = Arc::new(SequenceHost::new());
        host.push(Err("connection reset".into()));
        host.push(Ok(Response {
            code: 200,
            message: String::new(),
            data: Some(json!({
                "upload_id": "restored",
                "chunk_size": 1,
                "chunk_count": 1,
                "missing": [0]
            })),
        }));
        let desc = Description {
            total_size: 1,
            chunk_size: 1,
            chunk_count: 1,
            leaf_hashes_hex: vec![String::new()],
            merkle_root_hex: String::new(),
            file_hash_hex: String::new(),
        };
        let init = call_init_recovering(
            &Client::new(host.clone()),
            "file",
            "/test",
            &desc,
            "device",
            "",
            None,
            false,
        )
        .await
        .unwrap();
        assert_eq!(init.upload_id, "restored");
        assert!(host.is_empty());
    }

    #[tokio::test(start_paused = true)]
    async fn quick_complete_can_recover_beyond_old_deadline() {
        let host = Arc::new(SequenceHost::new());
        for _ in 0..601 {
            host.push(Ok(response(503)));
        }
        host.push(Ok(response(200)));
        let client = Client::new(host.clone());
        let started = tokio::time::Instant::now();
        let data =
            complete_quick_share(&client, "u1", "device", vec!["http://unit.invalid".into()])
                .await
                .unwrap();
        assert_eq!(data["ok"], true);
        assert!(started.elapsed() > Duration::from_secs(180));
        assert!(host.is_empty());
    }
}
