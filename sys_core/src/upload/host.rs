//! Host boundary: credentials, HTTP cancellation, logs and persistence stay with the host.
use super::{planner, protocol::UploadChunkData};
use serde::{de::DeserializeOwned, Serialize};
use serde_json::Value;
use std::{future::Future, pin::Pin, sync::Arc, time::Duration};

pub type HostFuture<'a, T> = Pin<Box<dyn Future<Output = Result<T, String>> + Send + 'a>>;
#[derive(Debug, serde::Deserialize, serde::Serialize)]
pub struct Response<T> {
    pub code: i32,
    pub message: String,
    pub data: Option<T>,
}
impl<T> Response<T> {
    pub fn is_ok(&self) -> bool {
        self.code == 200
    }
}
#[derive(Clone, Default)]
pub struct PlannerSettings {
    pub nodes: Vec<planner::PlannerNodeConfig>,
    pub main_node: String,
    pub state: planner::PersistedState,
}
pub enum RequestBody {
    Json(Value),
    Bytes(Vec<u8>),
}
pub struct UploadRequest {
    /// None uses the host's active endpoint and its ordinary cancellation policy.
    pub node_url: Option<String>,
    /// Route excludes the /v1 prefix. Hosts append it exactly once.
    pub path: String,
    pub query: Vec<(String, String)>,
    pub body: RequestBody,
    pub timeout: Option<Duration>,
}
pub trait UploadHost: Send + Sync + 'static {
    fn request(&self, request: UploadRequest) -> HostFuture<'_, Response<Value>>;
    fn server_url(&self) -> &str;
    /// Return an isolated upload session preserving credentials but surviving UI node switches.
    fn for_upload_session(&self) -> Arc<dyn UploadHost>;
    fn probe_share_url<'a>(&'a self, url: &'a str) -> HostFuture<'a, ()>;
    fn resume_chunk_size(&self) -> Option<u64> {
        None
    }
    fn is_cancelled_error(&self, _error: &str) -> bool {
        false
    }
    fn log(&self, _level: &str, _source: &str, _message: String) {}
    fn save_planner_state(&self, _state: planner::PersistedState) {}
}
#[derive(Clone)]
pub struct Client(Arc<dyn UploadHost>);
impl Client {
    pub fn new(host: Arc<dyn UploadHost>) -> Self {
        Self(host)
    }
    pub fn host(&self) -> &dyn UploadHost {
        self.0.as_ref()
    }
    pub fn server_url(&self) -> &str {
        self.0.server_url()
    }
    pub fn for_upload_session(&self) -> Self {
        Self(self.0.for_upload_session())
    }
    pub async fn probe_share_url(&self, url: &str) -> Result<(), String> {
        self.0.probe_share_url(url).await
    }
    pub(super) fn info(&self, source: &str, msg: String) {
        self.0.log("info", source, msg);
    }
    pub(super) fn warn(&self, source: &str, msg: String) {
        self.0.log("warn", source, msg);
    }
    async fn send<T: DeserializeOwned>(
        &self,
        request: UploadRequest,
    ) -> Result<Response<T>, String> {
        let response = self.0.request(request).await?;
        let data = response
            .data
            .filter(|v| !v.is_null())
            .map(serde_json::from_value)
            .transpose()
            .map_err(|e| format!("JSON 解析失败: {e}"))?;
        Ok(Response {
            code: response.code,
            message: response.message,
            data,
        })
    }
    pub(super) async fn post<T: DeserializeOwned, B: Serialize>(
        &self,
        path: &str,
        body: &B,
    ) -> Result<Response<T>, String> {
        self.send(UploadRequest {
            node_url: None,
            path: path.into(),
            query: vec![],
            body: RequestBody::Json(serde_json::to_value(body).map_err(|e| e.to_string())?),
            timeout: None,
        })
        .await
    }
    pub(super) async fn post_json_to<T: DeserializeOwned, B: Serialize>(
        &self,
        url: &str,
        path: &str,
        body: &B,
    ) -> Result<Response<T>, String> {
        self.post_json_to_timeout(url, path, body, Duration::from_secs(5))
            .await
    }
    pub(super) async fn post_json_to_timeout<T: DeserializeOwned, B: Serialize>(
        &self,
        url: &str,
        path: &str,
        body: &B,
        timeout: Duration,
    ) -> Result<Response<T>, String> {
        self.send(UploadRequest {
            node_url: Some(url.into()),
            path: path.into(),
            query: vec![],
            body: RequestBody::Json(serde_json::to_value(body).map_err(|e| e.to_string())?),
            timeout: Some(timeout),
        })
        .await
    }
    pub(super) async fn post_bytes_to<T: DeserializeOwned>(
        &self,
        url: &str,
        path: &str,
        params: &[(&str, &str)],
        data: Vec<u8>,
        timeout: Option<Duration>,
    ) -> Result<Response<T>, String> {
        self.send(UploadRequest {
            node_url: Some(url.into()),
            path: path.into(),
            query: params
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
            body: RequestBody::Bytes(data),
            timeout,
        })
        .await
    }
    pub(super) async fn upload_chunk(
        &self,
        url: Option<&str>,
        upload_id: &str,
        index: i32,
        data: Vec<u8>,
        timeout: Option<Duration>,
        attempt_id: &str,
    ) -> Result<Response<UploadChunkData>, String> {
        let mut query = vec![
            ("upload_id".into(), upload_id.into()),
            ("index".into(), index.to_string()),
        ];
        if !attempt_id.is_empty() {
            query.push(("attempt_id".into(), attempt_id.into()));
        }
        self.send(UploadRequest {
            node_url: url.map(str::to_owned),
            path: "/file/upload/chunk".into(),
            query,
            body: RequestBody::Bytes(data),
            timeout,
        })
        .await
    }
}
