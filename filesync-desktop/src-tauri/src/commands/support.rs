// commands/support.rs
// 职责：所有 command 模块共用的小工具——构造 ApiClient、统一响应解包、通用 API 代理。
use crate::api;
use crate::config::SyncConfig;
use tauri::State;

pub fn make_client(cfg: &SyncConfig) -> Result<api::client::ApiClient, String> {
    if cfg.server_url.is_empty() {
        return Err("服务器地址未配置，请先在设置页面填写服务器地址".into());
    }
    Ok(api::client::ApiClient::new(&cfg.server_url, &cfg.token, &cfg.device_id))
}

// 业务失败统一格式化为 "[code] message"，把后端信封 code 带给前端（前端 String(e) 即可看到码 + 信息）。
pub fn api_data<T>(resp: api::client::ApiResponse<T>, op: &str) -> Result<T, String> {
    if resp.is_ok() {
        resp.data.ok_or_else(|| format!("[{}] 响应 data 为空", op))
    } else {
        Err(format!("[{}] {}", resp.code, resp.message))
    }
}

// 无数据返回的命令（更新/删除类）统一成功/失败判定，失败带上 code。
pub fn ensure_ok<T>(resp: api::client::ApiResponse<T>) -> Result<(), String> {
    if resp.is_ok() {
        Ok(())
    } else {
        Err(format!("[{}] {}", resp.code, resp.message))
    }
}

/// 通用 API 代理：把任意 v1 接口透传给 Rust 侧的 ApiClient。
///
/// 为什么要有它：Tauri 模式下前端**不能**直接 fetch 后端（webview 里的 token 可能是陈旧的，
/// 且要绕 CORS），既有做法是每个接口写一个 command。管理域一口气新增了二十来个只读/简单写的
/// 接口，逐个包 command 纯属重复劳动，故提供一个统一代理：路径与 body 由前端给，
/// token/服务器地址仍由 Rust 侧的 SyncConfig 提供（安全边界不变）。
///
/// 只允许 /v1 下的相对路径，防止被当成任意 URL 的转发器。
#[tauri::command]
pub async fn api_request(
    method: String,
    path: String,
    body: Option<serde_json::Value>,
    query: Option<std::collections::HashMap<String, String>>,
    config: State<'_, crate::config::SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    if !path.starts_with('/') || path.contains("://") {
        return Err("非法的接口路径".into());
    }
    let client = make_client(&config.read())?;
    let params: Option<std::collections::HashMap<&str, String>> = query
        .as_ref()
        .map(|m| m.iter().map(|(k, v)| (k.as_str(), v.clone())).collect());

    let resp: api::client::ApiResponse<serde_json::Value> = match method.to_uppercase().as_str() {
        "GET" => client.get(&path, params.as_ref()).await?,
        "POST" => match body {
            Some(b) => client.post(&path, &b).await?,
            None => client.post_empty(&path).await?,
        },
        "PUT" => {
            client
                .put(&path, &body.unwrap_or(serde_json::Value::Null))
                .await?
        }
        "DELETE" => client.delete(&path).await?,
        other => return Err(format!("不支持的方法: {}", other)),
    };
    if !resp.is_ok() {
        return Err(resp.message);
    }
    Ok(resp.data.unwrap_or(serde_json::Value::Null))
}
