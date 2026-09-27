// commands/update.rs
// 职责：应用更新域——发布/查询/修改/删除 release，客户端查新。
// 走与其它域相同的 invoke → ApiClient 路径（token 来自 SyncConfig，reqwest 直发，
// 避免 webview fetch 的 CORS / localStorage 陈旧 token 导致 401）。用 serde_json::Value
// 透传，无需新增 params/response 结构体。
use crate::api;
use crate::api::routes;
use crate::commands::support::{api_data, ensure_ok, make_client};
use crate::config::SharedSyncConfig;
use tauri::State;

#[tauri::command]
pub async fn list_app_releases(
    platform: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let mut params = std::collections::HashMap::new();
    params.insert("platform", platform);
    let resp: api::client::ApiResponse<serde_json::Value> =
        client.get(routes::UPDATE_RELEASES, Some(&params)).await?;
    api_data(resp, "list_app_releases")
}

#[tauri::command]
pub async fn publish_app_release(
    release: serde_json::Value,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let resp: api::client::ApiResponse<serde_json::Value> =
        client.post(routes::UPDATE_PUBLISH, &release).await?;
    api_data(resp, "publish_app_release")
}

#[tauri::command]
pub async fn update_app_release(
    id: u64,
    updates: serde_json::Value,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let path = routes::UPDATE_RELEASE_BY_ID.replace("{}", &id.to_string());
    let resp: api::client::ApiResponse<serde_json::Value> = client.put(&path, &updates).await?;
    ensure_ok(resp)
}

#[tauri::command]
pub async fn delete_app_release(id: u64, config: State<'_, SharedSyncConfig>) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let path = routes::UPDATE_RELEASE_BY_ID.replace("{}", &id.to_string());
    let resp: api::client::ApiResponse<serde_json::Value> = client.delete(&path).await?;
    ensure_ok(resp)
}

#[tauri::command]
pub async fn check_app_update(
    platform: String,
    version_code: i64,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let mut params = std::collections::HashMap::new();
    params.insert("platform", platform);
    params.insert("version_code", version_code.to_string());
    let resp: api::client::ApiResponse<serde_json::Value> =
        client.get(routes::UPDATE_CHECK, Some(&params)).await?;
    api_data(resp, "check_app_update")
}
