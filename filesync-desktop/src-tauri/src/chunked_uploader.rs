//! Desktop host adapter. Upload execution and wire types live in sys_core.
use crate::{
    api::client::{ApiClient, NODE_SWITCHED},
    config::SharedSyncConfig,
    logger,
};
use std::{path::Path, sync::Arc};
pub use sys_core::file::file_blake3_hex;
pub use sys_core::upload::{ProgressFn, UploadOptions, SYNC_CONFLICT};
use sys_core::{
    upload::{
        self, Client, HostFuture, PlannerSettings, RequestBody, Response, UploadHost, UploadRequest,
    },
    upload_planner,
};
use upload::protocol::{QuickShareCompleteData, UploadCompleteData};

struct DesktopHost {
    client: ApiClient,
    config: SharedSyncConfig,
}
impl UploadHost for DesktopHost {
    fn request(&self, request: UploadRequest) -> HostFuture<'_, Response<serde_json::Value>> {
        Box::pin(async move {
            let query: Vec<(&str, &str)> = request
                .query
                .iter()
                .map(|(k, v)| (k.as_str(), v.as_str()))
                .collect();
            let response = match request.body {
                RequestBody::Json(body) => match request.node_url {
                    Some(url) => {
                        self.client
                            .post_json_to_timeout(
                                &url,
                                &request.path,
                                &body,
                                request
                                    .timeout
                                    .unwrap_or(std::time::Duration::from_secs(60)),
                            )
                            .await?
                    }
                    None => self.client.post(&request.path, &body).await?,
                },
                RequestBody::Bytes(body) => match request.node_url {
                    Some(url) => {
                        self.client
                            .post_bytes_to(&url, &request.path, &query, body, request.timeout)
                            .await?
                    }
                    None => {
                        self.client
                            .post_raw_bytes(&request.path, &query, body)
                            .await?
                    }
                },
            };
            Ok(Response {
                code: response.code,
                message: response.message,
                data: response.data,
            })
        })
    }
    fn server_url(&self) -> &str {
        self.client.server_url()
    }
    fn for_upload_session(&self) -> Arc<dyn UploadHost> {
        Arc::new(Self {
            client: self.client.for_upload_session(),
            config: self.config.clone(),
        })
    }
    fn probe_share_url<'a>(&'a self, url: &'a str) -> HostFuture<'a, ()> {
        Box::pin(self.client.probe_share_url(url))
    }
    fn is_cancelled_error(&self, error: &str) -> bool {
        error.contains(NODE_SWITCHED)
    }
    fn log(&self, level: &str, source: &str, message: String) {
        if level == "warn" {
            logger::warn(source, message)
        } else {
            logger::info(source, message)
        }
    }
    fn save_planner_state(&self, state: upload_planner::PersistedState) {
        match serde_json::to_value(state) {
            Ok(value) => {
                let mut cfg = self.config.write();
                cfg.upload_planner_state = Some(value);
                cfg.save();
            }
            Err(e) => logger::warn("upload", format!("planner 状态序列化失败: {e}")),
        }
    }
}
fn context(client: &ApiClient, config: &SharedSyncConfig) -> (Client, PlannerSettings) {
    let cfg = config.read();
    let settings = PlannerSettings {
        nodes: cfg
            .nodes
            .iter()
            .map(|n| upload_planner::PlannerNodeConfig {
                id: n.id.clone(),
                url: n.server_url.clone(),
                parent: n.parent.clone(),
            })
            .collect(),
        main_node: cfg.active_node_id.clone(),
        state: cfg
            .upload_planner_state
            .as_ref()
            .and_then(|v| serde_json::from_value(v.clone()).ok())
            .unwrap_or_default(),
    };
    (
        Client::new(Arc::new(DesktopHost {
            client: client.clone(),
            config: config.clone(),
        })),
        settings,
    )
}
pub async fn upload(
    client: &ApiClient,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &SharedSyncConfig,
) -> Result<UploadCompleteData, String> {
    let (client, settings) = context(client, config);
    upload::upload(&client, file, remote_dir, options, on_progress, &settings).await
}
pub async fn quick_share_upload(
    client: &ApiClient,
    file: &Path,
    remote_dir: &str,
    options: &UploadOptions,
    on_progress: ProgressFn,
    config: &SharedSyncConfig,
) -> Result<QuickShareCompleteData, String> {
    let (client, settings) = context(client, config);
    upload::quick_share_upload(&client, file, remote_dir, options, on_progress, &settings).await
}
