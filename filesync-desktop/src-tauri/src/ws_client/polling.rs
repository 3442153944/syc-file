// ws_client/polling.rs
// 职责：WS 长期连不上/老被掐断时的降级——改用 HTTP 拉取服务端为本设备排好的 pending 任务并执行。
//
// 为什么可行：上行（本地变更 → 服务端）本来就全走 REST（分片上传 + notify），
// 只有下行的 task_created 推送依赖 WS；而服务端的任务是落库的（PendingTasksForDevice 就是
// 「供 WS 不可用时 HTTP 拉取」），执行完照样 REST 上报 complete/failed。企业网络里 HTTPS
// 请求/响应几乎总是放行的，WS 升级和长连接才是最容易被掐的部分。
//
// 降级模式只覆盖同步任务（download/delete/mkdir）。冲突下发、剪贴板推送、监控指标依赖 WS，
// 降级期间收不到，WS 恢复后自动回来。
use super::download::build_remote_dir;
use super::tasks::on_task_created;
use crate::api::{client::ApiClient, sync::api as sync_api, ws::types::TaskCreatedContent};
use crate::config::SharedSyncConfig;
use crate::logger;

/// 拉一轮 pending 任务并逐个执行。失败只记 debug：断网时每 15 秒一次的失败不值得刷屏，
/// 真正的传输层故障 ApiClient 已经上报给灾备模块了。
pub async fn poll_pending_tasks(config: &SharedSyncConfig) {
    let (server_url, token, device_id) = {
        let cfg = config.read();
        (cfg.server_url.clone(), cfg.token.clone(), cfg.device_id.clone())
    };
    if server_url.is_empty() || token.is_empty() {
        return;
    }
    let client = ApiClient::new(&server_url, &token);
    let tasks = match sync_api::list_pending_tasks(&client, &device_id).await {
        Ok(resp) if resp.is_ok() => resp.data.unwrap_or_default(),
        Ok(resp) => {
            logger::debug("poll", format!("拉取待办任务失败: [{}] {}", resp.code, resp.message));
            return;
        }
        Err(e) => {
            logger::debug("poll", format!("拉取待办任务失败: {}", e));
            return;
        }
    };
    if tasks.is_empty() {
        return;
    }
    logger::info("poll", format!("降级轮询：拉到 {} 个待办任务", tasks.len()));
    for t in tasks {
        // 推送里的 remote_dir 是服务端算好的绝对目录；REST 列表没有这个字段，
        // 按本地目录映射同样规则拼出来（冲突收敛下载走的也是这条）
        let remote_dir = build_remote_dir(config, t.folder_id, &t.relative_path);
        on_task_created(
            TaskCreatedContent {
                event: "task_created".into(),
                task_id: t.id,
                task_type: t.task_type,
                direction: None,
                folder_id: t.folder_id,
                relative_path: t.relative_path,
                file_name: t.file_name,
                file_size: t.file_size,
                file_hash: t.file_hash,
                remote_path: None,
                remote_dir: (!remote_dir.is_empty()).then_some(remote_dir),
            },
            config,
        )
        .await;
    }
}
