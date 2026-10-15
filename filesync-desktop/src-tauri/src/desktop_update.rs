//! 桌面更新：版本与任务状态由 Rust 持有，Tauri updater 负责验签和平台安装。
use crate::config::SharedSyncConfig;
use parking_lot::Mutex;
use serde::Serialize;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};
use tauri::{AppHandle, Emitter, Manager, State};
use tauri_plugin_updater::{Update, UpdaterExt};

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateStatus {
    pub current_version: String,
    pub platform: String,
    pub arch: String,
    pub bundle_type: String,
    pub stage: String,
    pub version: String,
    pub notes: String,
    pub mandatory: bool,
    pub downloaded: u64,
    pub total: Option<u64>,
    pub error: Option<String>,
}

pub struct UpdateState {
    status: UpdateStatus,
    pending: Option<Update>,
    bytes: Option<Vec<u8>>,
    source: String,
    busy: bool,
}
pub type SharedUpdate = Arc<Mutex<UpdateState>>;
static PENDING_OPEN: AtomicBool = AtomicBool::new(false);

pub fn open_update_window(app: &AppHandle) {
    // 先留下标记，避免重建窗口挂载早于这里写入；前端取快照后消费它。
    PENDING_OPEN.store(true, Ordering::SeqCst);
    match crate::app_shell::window::show_main_window(app) {
        Some((window, false)) => {
            PENDING_OPEN.store(false, Ordering::SeqCst);
            let _ = window.emit("desktop-update-open", ());
        }
        Some((_, true)) => {}
        None => { PENDING_OPEN.store(false, Ordering::SeqCst); }
    }
}

#[tauri::command]
pub fn take_pending_desktop_update() -> bool {
    PENDING_OPEN.swap(false, Ordering::SeqCst)
}

pub fn on_release(app: &AppHandle, release: &serde_json::Value) {
    let state = app.state::<SharedUpdate>();
    let matches = {
        let s = state.lock();
        release["platform"].as_str() == Some(s.status.platform.as_str())
            && release["arch"].as_str() == Some(s.status.arch.as_str())
            && release["bundle_type"].as_str() == Some(s.status.bundle_type.as_str())
    };
    if matches {
        crate::logger::info("update", "收到当前平台的版本发布通知，后台检查更新");
        auto_check(app.clone(), app.state::<SharedSyncConfig>().inner().clone());
    }
}

pub fn init() -> SharedUpdate {
    let bundle = match tauri::utils::platform::bundle_type() {
        Some(tauri::utils::config::BundleType::Deb) => "deb",
        Some(tauri::utils::config::BundleType::AppImage) => "appimage",
        Some(tauri::utils::config::BundleType::Nsis) => "nsis",
        _ if cfg!(windows) => "nsis",
        _ => "deb",
    };
    Arc::new(Mutex::new(UpdateState {
        status: UpdateStatus {
            current_version: String::new(), platform: std::env::consts::OS.into(),
            arch: std::env::consts::ARCH.into(), bundle_type: bundle.into(),
            stage: "idle".into(), version: String::new(), notes: String::new(), mandatory: false,
            downloaded: 0, total: None, error: None,
        }, pending: None, bytes: None, source: String::new(), busy: false,
    }))
}

fn notify(app: &AppHandle, state: &SharedUpdate) {
    let _ = app.emit("desktop-update-changed", state.lock().status.clone());
}

fn fail(app: &AppHandle, state: &SharedUpdate, error: String) -> String {
    { let mut s = state.lock(); s.busy = false; s.status.stage = "error".into(); s.status.error = Some(error.clone()); }
    notify(app, state);
    error
}

pub fn auto_check(app: AppHandle, config: SharedSyncConfig) {
    tauri::async_runtime::spawn(async move {
        let state = app.state::<SharedUpdate>().inner().clone();
        if let Err(e) = check(&app, &config, &state).await {
            crate::logger::warn("update", format!("自动检查更新失败: {e}"));
        }
    });
}

#[tauri::command]
pub fn desktop_update_status(app: AppHandle, state: State<SharedUpdate>) -> UpdateStatus {
    let mut s = state.lock();
    s.status.current_version = app.package_info().version.to_string();
    s.status.clone()
}

#[tauri::command]
pub async fn desktop_check_update(app: AppHandle, config: State<'_, SharedSyncConfig>, state: State<'_, SharedUpdate>) -> Result<UpdateStatus, String> {
    check(&app, &config, &state).await
}

fn package_url(base: &str, release_id: u64) -> Result<reqwest::Url, String> {
    if release_id == 0 { return Err("更新信息缺少安装包 ID".into()); }
    reqwest::Url::parse(&format!("{}/v1/update/package/{}", base.trim_end_matches('/'), release_id)).map_err(|e| e.to_string())
}

async fn secure_update_base(base: &str, nodes: &[crate::config::ServerNode]) -> Result<String, String> {
    let url = reqwest::Url::parse(base).map_err(|e| e.to_string())?;
    if url.scheme() == "https" { return Ok(base.to_string()); }
    if url.scheme() != "http" { return Err("更新入口必须使用 HTTPS".into()); }
    let current = nodes.iter().find(|node| node.server_url.trim_end_matches('/') == base)
        .cloned().unwrap_or(crate::config::ServerNode {
            id: "update-current".into(), name: "当前入口".into(), server_url: base.into(),
            ws_url: crate::config::ServerNode::derive_ws_url(base), builtin: false, parent: None,
        });
    let current_health = crate::net::probe_node(&current).await;
    if !current_health.reachable || current_health.server_node.is_empty() {
        return Err("无法确认当前后端，不能选择 HTTPS 更新入口".into());
    }
    for node in nodes {
        if !reqwest::Url::parse(&node.server_url).map(|url| url.scheme() == "https").unwrap_or(false) { continue; }
        let health = crate::net::probe_node(node).await;
        if health.reachable && health.server_node == current_health.server_node {
            crate::logger::info("update", format!("内网入口使用同后端 HTTPS 更新节点: {}", node.server_url));
            return Ok(node.server_url.trim_end_matches('/').to_string());
        }
    }
    Err("当前使用 HTTP 入口，未找到同一后端的可用 HTTPS 更新节点；请配置 HTTPS 节点后重试".into())
}

async fn check(app: &AppHandle, config: &SharedSyncConfig, state: &SharedUpdate) -> Result<UpdateStatus, String> {
    let (base, token, device_id, nodes) = {
        let cfg = config.read();
        if cfg.token.is_empty() { return Err("请先登录再检查更新".into()); }
        (cfg.server_url.trim_end_matches('/').to_string(), cfg.token.clone(), cfg.device_id.clone(), cfg.nodes.clone())
    };
    let (platform, arch, bundle, version) = {
        let mut s = state.lock();
        if s.busy || (s.status.stage == "ready" && s.source == base) { return Ok(s.status.clone()); }
        s.busy = true; s.pending = None; s.bytes = None;
        s.status.stage = "checking".into(); s.status.error = None;
        s.status.current_version = app.package_info().version.to_string();
        (s.status.platform.clone(), s.status.arch.clone(), s.status.bundle_type.clone(), s.status.current_version.clone())
    };
    notify(app, state);
    crate::logger::info("update", format!("检查更新: 当前版本={version} 平台={platform}/{arch}/{bundle}"));
    let outcome = async {
        // 更新器保留 HTTPS 和签名校验。HTTP 同步入口不变，只为更新选择已确认同后端的 HTTPS 节点。
        let update_base = secure_update_base(&base, &nodes).await?;
        let mut endpoint = reqwest::Url::parse(&format!("{update_base}/v1/update/desktop")).map_err(|e| e.to_string())?;
        endpoint.query_pairs_mut().extend_pairs([
            ("platform", platform.as_str()), ("arch", arch.as_str()), ("bundle_type", bundle.as_str()), ("version", version.as_str()),
        ]);
        let updater = app.updater_builder().endpoints(vec![endpoint]).map_err(|e| e.to_string())?
            .header("Token", token).map_err(|e| e.to_string())?
            .header("Device-Id", device_id).map_err(|e| e.to_string())?
            .timeout(Duration::from_secs(30)).build().map_err(|e| e.to_string())?;
        let mut update = updater.check().await.map_err(|e| e.to_string())?;
        if let Some(u) = update.as_mut() {
            // 不信任 manifest 的任意下载域名；固定当前入口，保留 /file 与 :8443。
            let id = u.raw_json.get("release_id").and_then(|v| v.as_u64()).ok_or("更新包 ID 无效")?;
            u.download_url = package_url(&update_base, id)?;
            u.timeout = Some(Duration::from_secs(600));
        }
        Ok::<_, String>(update)
    }.await;
    let update = outcome.map_err(|e| fail(app, state, e))?;
    {
        let mut s = state.lock();
        s.busy = false; s.source = base;
        s.status.version = update.as_ref().map(|u| u.version.clone()).unwrap_or_default();
        s.status.notes = update.as_ref().and_then(|u| u.body.clone()).unwrap_or_default();
        s.status.mandatory = update.as_ref().and_then(|u| u.raw_json.get("mandatory")).and_then(|v| v.as_bool()).unwrap_or(false);
        s.status.stage = if update.is_some() { "available" } else { "idle" }.into();
        s.status.downloaded = 0; s.status.total = None;
        s.pending = update;
    }
    notify(app, state);
    let status = state.lock().status.clone();
    crate::logger::info("update", if status.stage == "available" {
        format!("发现新版本: {} → {}", status.current_version, status.version)
    } else { format!("检查完成: 当前版本 {} 无可用更新", status.current_version) });
    Ok(state.lock().status.clone())
}

#[tauri::command]
pub async fn desktop_download_update(app: AppHandle, config: State<'_, SharedSyncConfig>, state: State<'_, SharedUpdate>) -> Result<(), String> {
    let source = config.read().server_url.trim_end_matches('/').to_string();
    let update = {
        let mut s = state.lock();
        if s.busy { return Err("更新操作进行中".into()); }
        if source != s.source { return Err("服务器已切换，请重新检查更新".into()); }
        let update = s.pending.clone().ok_or("没有可下载的更新")?;
        s.busy = true; s.status.stage = "downloading".into(); s.status.downloaded = 0; s.status.error = None;
        update
    };
    notify(&app, &state);
    let progress_state = state.inner().clone();
    let handle = app.clone();
    let mut last_emit = Instant::now();
    let bytes = update.download(move |chunk, total| {
        { let mut s = progress_state.lock(); s.status.downloaded += chunk as u64; s.status.total = total; }
        if last_emit.elapsed() >= Duration::from_millis(100) {
            notify(&handle, &progress_state);
            last_emit = Instant::now();
        }
    }, || {}).await.map_err(|e| fail(&app, &state, format!("下载或签名验证失败: {e}")))?;
    { let mut s = state.lock(); s.bytes = Some(bytes); s.busy = false; s.status.stage = "ready".into(); }
    notify(&app, &state);
    Ok(())
}

#[tauri::command]
pub async fn desktop_install_update(app: AppHandle, state: State<'_, SharedUpdate>, engine: State<'_, crate::sync_engine::SharedSyncEngine>) -> Result<(), String> {
    let snapshot = crate::transfers::snapshot();
    if snapshot.uploads.iter().any(|u| matches!(u.status.as_str(), "uploading" | "paused"))
        || snapshot.downloads.iter().any(|d| d.status == "downloading")
        || snapshot.sync_events.iter().any(|e| e.status == "uploading") {
        return Err("还有进行中或暂停的传输，请完成或取消后再安装更新".into());
    }
    let (update, bytes) = {
        let mut s = state.lock();
        if s.busy { return Err("更新操作进行中".into()); }
        let update = s.pending.clone().ok_or("没有可安装的更新")?;
        let bytes = s.bytes.take().ok_or("请先下载更新")?;
        s.busy = true; s.status.stage = "installing".into(); s.status.error = None;
        (update, bytes)
    };
    notify(&app, &state);
    let was_running = engine.lock().is_some();
    crate::sync_engine::stop_sync_engine(&engine);
    let result = tokio::task::spawn_blocking(move || update.install(&bytes)).await;
    let error = match result { Ok(Ok(())) => None, Ok(Err(e)) => Some(e.to_string()), Err(e) => Some(e.to_string()) };
    if let Some(error) = error {
        if was_running {
            let cfg = app.state::<SharedSyncConfig>();
            let _ = crate::sync_engine::do_start_sync(&cfg, &engine, &app).await;
        }
        return Err(fail(&app, &state, error));
    }
    #[cfg(not(windows))]
    app.restart();
    Ok(())
}

/// 发布页面读取安装包旁的 .sig，小文件且只读。
#[tauri::command]
pub fn read_update_signature(path: String) -> Result<String, String> {
    let p = std::path::Path::new(&path);
    if p.extension().and_then(|s| s.to_str()) != Some("sig") { return Err("请选择 .sig 文件".into()); }
    if p.metadata().map_err(|e| e.to_string())?.len() > 16384 { return Err("签名文件过大".into()); }
    std::fs::read_to_string(p).map(|s| s.trim().to_string()).map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn download_stays_on_configured_proxy_endpoint() {
        let u = package_url("https://jp.example:8443/file/", 7).unwrap();
        assert_eq!(u.as_str(), "https://jp.example:8443/file/v1/update/package/7");
        assert!(package_url("https://example/file", 0).is_err());
    }

    #[test]
    fn fixed_public_key_accepts_real_signature_and_rejects_tampering() {
        use base64::Engine;
        let config: serde_json::Value = serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        let key = config["plugins"]["updater"]["pubkey"].as_str().unwrap();
        let decoded_key = base64::prelude::BASE64_STANDARD.decode(key).unwrap();
        let public = minisign_verify::PublicKey::decode(std::str::from_utf8(&decoded_key).unwrap()).unwrap();
        let sig = include_str!("../tests/fixtures/update-payload.bin.sig").trim();
        let decoded_sig = base64::prelude::BASE64_STANDARD.decode(sig).unwrap();
        let signature = minisign_verify::Signature::decode(std::str::from_utf8(&decoded_sig).unwrap()).unwrap();
        let data = include_bytes!("../tests/fixtures/update-payload.bin");
        public.verify(data, &signature, true).unwrap();
        assert!(signature.trusted_comment().contains("0.1.1"));
        let mut changed = data.to_vec(); changed[0] ^= 1;
        assert!(public.verify(&changed, &signature, true).is_err());
    }
}
