//! Event bridge for JNI/foreign runtimes. Scheduling, files and retries stay in Rust.
//! Hosts execute only HTTP requests and return the unmodified response envelope.
use super::*;
use serde_json::{json, Value};
use std::{
    collections::VecDeque,
    sync::{
        atomic::{AtomicU64, Ordering as AtomicOrdering},
        Mutex, OnceLock,
    },
};
use tokio::sync::oneshot;

#[derive(serde::Deserialize)]
pub struct Start {
    pub file: std::path::PathBuf,
    pub remote_dir: String,
    pub base_url: String,
    #[serde(default)]
    pub device_id: String,
    #[serde(default)]
    pub quick_share: bool,
    pub chunk_size: Option<usize>,
    pub concurrency: Option<usize>,
    #[serde(default)]
    pub nodes: Vec<planner::PlannerNodeConfig>,
    #[serde(default)]
    pub main_node: String,
    #[serde(default)]
    pub state: planner::PersistedState,
    pub resume: Option<protocol::UploadInitData>,
}
struct Pending {
    reply: oneshot::Sender<Result<Response<Value>, String>>,
    bytes: Option<Vec<u8>>,
}
struct State {
    base: String,
    events: Mutex<VecDeque<Value>>,
    pending: Mutex<HashMap<u64, Pending>>,
    next: AtomicU64,
    resume_size: Option<u64>,
    resume: Mutex<Option<protocol::UploadInitData>>,
}
impl State {
    fn event(&self, event: Value) {
        self.events
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .push_back(event);
    }
}
struct Guard {
    state: Arc<State>,
    id: u64,
}
impl Drop for Guard {
    fn drop(&mut self) {
        if self
            .state
            .pending
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(&self.id)
            .is_some()
        {
            self.state
                .event(json!({"type":"cancel_request", "id":self.id}));
        }
    }
}
#[derive(Clone)]
struct Host(Arc<State>);
impl UploadHost for Host {
    fn request(&self, request: UploadRequest) -> HostFuture<'_, Response<Value>> {
        Box::pin(async move {
            let is_init = request.path == "/file/upload/init";
            if is_init {
                let resume = self
                    .0
                    .resume
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .take();
                if let Some(data) = resume {
                    self.0
                        .event(json!({"type":"session", "upload_id":data.upload_id}));
                    return Ok(Response {
                        code: 200,
                        message: String::new(),
                        data: Some(serde_json::to_value(data).map_err(|e| e.to_string())?),
                    });
                }
            }
            let id = self.0.next.fetch_add(1, AtomicOrdering::Relaxed);
            let (tx, rx) = oneshot::channel();
            let (body, bytes) = match request.body {
                RequestBody::Json(value) => (json!({"kind":"json", "value":value}), None),
                RequestBody::Bytes(bytes) => (json!({"kind":"bytes"}), Some(bytes)),
            };
            self.0
                .pending
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .insert(id, Pending { reply: tx, bytes });
            let _guard = Guard {
                state: self.0.clone(),
                id,
            };
            self.0.event(json!({"type":"request", "id":id,
                "base_url":request.node_url.unwrap_or_else(|| self.0.base.clone()),
                "path":request.path, "query":request.query, "body":body,
                "timeout_ms":request.timeout.map(|t| t.as_millis() as u64).unwrap_or(60_000)}));
            let response = rx.await.map_err(|_| "upload host closed".to_string())??;
            if is_init && response.is_ok() {
                if let Some(id) = response
                    .data
                    .as_ref()
                    .and_then(|v| v.get("upload_id"))
                    .and_then(Value::as_str)
                {
                    self.0.event(json!({"type":"session", "upload_id":id}));
                }
            }
            Ok(response)
        })
    }
    fn server_url(&self) -> &str {
        &self.0.base
    }
    fn for_upload_session(&self) -> Arc<dyn UploadHost> {
        Arc::new(self.clone())
    }
    fn resume_chunk_size(&self) -> Option<u64> {
        self.0.resume_size
    }
    fn probe_share_url<'a>(&'a self, url: &'a str) -> HostFuture<'a, ()> {
        Box::pin(async move {
            let id = self.0.next.fetch_add(1, AtomicOrdering::Relaxed);
            let (tx, rx) = oneshot::channel();
            self.0
                .pending
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .insert(
                    id,
                    Pending {
                        reply: tx,
                        bytes: None,
                    },
                );
            let _guard = Guard {
                state: self.0.clone(),
                id,
            };
            self.0
                .event(json!({"type":"probe_share", "id":id, "url":url}));
            let response = rx.await.map_err(|_| "upload host closed".to_string())??;
            if response.is_ok() {
                Ok(())
            } else {
                Err(response.message)
            }
        })
    }
    fn log(&self, level: &str, source: &str, message: String) {
        self.0
            .event(json!({"type":"log", "level":level, "source":source, "message":message}));
    }
    fn save_planner_state(&self, state: planner::PersistedState) {
        self.0.event(json!({"type":"state", "state":state}));
    }
}
struct Job {
    state: Arc<State>,
    task: tokio::task::JoinHandle<()>,
}
static JOBS: OnceLock<Mutex<HashMap<u64, Job>>> = OnceLock::new();
static RUNTIME: OnceLock<Result<tokio::runtime::Runtime, String>> = OnceLock::new();
static NEXT: AtomicU64 = AtomicU64::new(1);
fn jobs() -> &'static Mutex<HashMap<u64, Job>> {
    JOBS.get_or_init(|| Mutex::new(HashMap::new()))
}
fn state(id: u64) -> Result<Arc<State>, String> {
    jobs()
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .get(&id)
        .map(|j| j.state.clone())
        .ok_or_else(|| "unknown upload job".into())
}
pub fn start(input: Start) -> Result<u64, String> {
    let runtime = RUNTIME
        .get_or_init(|| {
            tokio::runtime::Builder::new_multi_thread()
                .enable_all()
                .build()
                .map_err(|e| e.to_string())
        })
        .as_ref()
        .map_err(Clone::clone)?;
    if let Some(ref resume) = input.resume {
        if resume.chunk_size <= 0 || resume.chunk_size as u64 > (1 << 30) {
            return Err("invalid resumed chunk size".into());
        }
    }
    let state = Arc::new(State {
        base: input.base_url,
        events: Mutex::new(VecDeque::new()),
        pending: Mutex::new(HashMap::new()),
        next: AtomicU64::new(1),
        resume_size: input.resume.as_ref().map(|r| r.chunk_size as u64),
        resume: Mutex::new(input.resume),
    });
    let host = Client::new(Arc::new(Host(state.clone())));
    let mut options = UploadOptions::new(input.device_id);
    options.chunk_size = if input.chunk_size.is_some() {
        state.resume_size.map(|s| s as usize).or(input.chunk_size)
    } else {
        None
    };
    options.concurrency = input.concurrency;
    options.quick_share = input.quick_share;
    let settings = PlannerSettings {
        nodes: input.nodes,
        main_node: input.main_node,
        state: input.state,
    };
    let progress_state = state.clone();
    let progress: ProgressFn = Arc::new(move |sent, total| {
        progress_state.event(json!({"type":"progress", "sent":sent, "total":total}))
    });
    let output = state.clone();
    let task = runtime.spawn(async move {
        let result = if options.quick_share {
            quick_share_upload(
                &host,
                &input.file,
                &input.remote_dir,
                &options,
                progress,
                &settings,
            )
            .await
            .and_then(|v| serde_json::to_value(v).map_err(|e| e.to_string()))
        } else {
            upload(
                &host,
                &input.file,
                &input.remote_dir,
                &options,
                progress,
                &settings,
            )
            .await
            .and_then(|v| serde_json::to_value(v).map_err(|e| e.to_string()))
        };
        match result {
            Ok(data) => output.event(json!({"type":"done", "data":data})),
            Err(error) => output.event(json!({"type":"failed", "error":error})),
        }
    });
    let id = NEXT.fetch_add(1, AtomicOrdering::Relaxed);
    jobs()
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .insert(id, Job { state, task });
    Ok(id)
}
pub fn poll(id: u64) -> Result<Value, String> {
    let (state, finished) = jobs()
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .get(&id)
        .map(|job| (job.state.clone(), job.task.is_finished()))
        .ok_or("unknown upload job")?;
    let mut events = state.events.lock().unwrap_or_else(|e| e.into_inner());
    let mut result: Vec<Value> = (0..64).filter_map(|_| events.pop_front()).collect();
    if result.is_empty() && finished {
        result.push(json!({"type":"failed", "error":"upload runtime terminated unexpectedly"}));
    }
    Ok(Value::Array(result))
}
pub fn take_body(id: u64, request: u64) -> Result<Vec<u8>, String> {
    state(id)?
        .pending
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .get_mut(&request)
        .and_then(|p| p.bytes.take())
        .ok_or_else(|| "request has no byte body".into())
}
pub fn reply(
    id: u64,
    request: u64,
    response: Result<Response<Value>, String>,
) -> Result<(), String> {
    let pending = state(id)?
        .pending
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .remove(&request);
    if let Some(pending) = pending {
        let _ = pending.reply.send(response);
    }
    Ok(())
}
/// Removes the handle, aborts the Rust task, and releases all pending request buffers.
pub fn cancel(id: u64) {
    if let Some(job) = jobs().lock().unwrap_or_else(|e| e.into_inner()).remove(&id) {
        job.task.abort();
        job.state
            .pending
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clear();
    }
}
