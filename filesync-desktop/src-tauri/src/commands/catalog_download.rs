// 文件管理的手动下载：流式写入用户选择的位置，不把文件交给外部浏览器。
use super::support::make_client;
use crate::api::file::{api as file_api, params::DownloadParams};
use crate::config::SharedSyncConfig;
use std::{path::Path, time::Duration};
use tauri::State;

#[tauri::command]
pub async fn download_catalog_file(
    path: String,
    name: String,
    local_path: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    if !Path::new(&local_path).is_absolute() {
        return Err("请选择绝对路径作为保存位置".into());
    }
    let client = make_client(&config.read())?;
    let url = file_api::build_download_url(
        &client,
        &DownloadParams {
            path,
            name,
            device_id: String::new(),
        },
    );
    let response = reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(15))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|e| e.without_url().to_string())?
        .get(url)
        .send()
        .await
        .map_err(|e| e.without_url().to_string())?;
    crate::catalog_download::save_response(response, &local_path).await
}
