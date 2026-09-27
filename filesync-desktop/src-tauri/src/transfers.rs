// transfers.rs
// 职责：传输状态的唯一来源 —— 上传 / 下载 / 同步活动三张列表 + 上传测速。
//
// 为什么放 Rust 而不是前端 pinia：主窗口关闭时会真正销毁 WebView（省掉托盘常驻时
// WebView2 的几百 MB），前端内存里的一切随之消失。进度要能在窗口重建后接着显示，
// 就只能由长期存活的 Rust 进程持有。前端只是镜像：打开时 get_transfers 取快照，
// 之后靠 `transfer-changed` 事件增量更新。
//
// 以前这些进度是散落在各模块里的几个事件（upload-progress-byte / download-progress /
// sync-event / upload-progress），由前端各自拼装状态；现在统一收口到这里，
// 各模块调用对应函数，由这里负责记录与推送。
use parking_lot::Mutex;
use serde::Serialize;
use std::collections::{HashMap, VecDeque};
use std::sync::OnceLock;
use std::time::Duration;
use tauri::{AppHandle, Emitter, WebviewWindow};

/// 每张列表最多保留的条数（与原前端 MAX_LIST 一致）
const MAX_LIST: usize = 100;

// ── 数据结构（字段名与前端 useTransferStore 的类型一一对应，camelCase） ─────────

#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct UploadEntry {
    pub id: String,
    pub name: String,
    /// manual = 文件管理/发布里的分片上传（Rust 执行）；quick-share = 粘贴快传（WebView 里的 XHR）
    pub kind: String,
    pub total: i64,
    pub sent: i64,
    /// uploading / done / error
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    pub started_at: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<i64>,
    /// 当前速度（字节/秒）；完成后为整段平均速度
    pub speed: f64,
    /// 是否已进入真实传输阶段（分片上传前两次上报只是簿记，见 SpeedMeter）
    pub started: bool,
    /// 发起这次上传的窗口。WebView 里跑的上传（快传）依赖该窗口存活，
    /// 关窗时要据此决定能不能立刻销毁 WebView。
    #[serde(skip)]
    pub owner_window: Option<String>,
}

#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct DownloadEntry {
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub task_id: Option<u64>,
    pub name: String,
    pub path: String,
    /// downloading / done / blocked / error
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    pub started_at: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finished_at: Option<i64>,
}

#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct SyncEventEntry {
    pub id: String,
    pub path: String,
    /// create / modify / delete / deleted_by_server / upload
    pub kind: String,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
    pub time: i64,
}

#[derive(Serialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    pub uploads: Vec<UploadEntry>,
    pub downloads: Vec<DownloadEntry>,
    pub sync_events: Vec<SyncEventEntry>,
}

/// `transfer-changed` 事件负载：哪张列表的哪一条变了（整条推送，前端按 id upsert）
#[derive(Serialize, Clone)]
#[serde(tag = "list", content = "entry", rename_all = "camelCase")]
enum Change {
    Upload(UploadEntry),
    Download(DownloadEntry),
    Sync(SyncEventEntry),
}

// ── 测速（与前端 utils/speedMeter.ts 同一套算法，注释见那边） ──────────────────

const WINDOW_MS: i64 = 5000;
const MIN_SPAN_MS: i64 = 500;

struct SpeedMeter {
    samples: VecDeque<(i64, i64)>, // (t_ms, bytes)
    origin: Option<(i64, i64)>,
    pushes: u32,
    /// 前导簿记上报次数：分片上传 2（算完哈希报 0、init 后报已有字节），快传 0
    leading: u32,
}

impl SpeedMeter {
    fn new(leading: u32) -> Self {
        SpeedMeter { samples: VecDeque::new(), origin: None, pushes: 0, leading }
    }

    fn push(&mut self, bytes: i64, t: i64) {
        self.pushes += 1;
        let last = self.samples.back().copied();
        // 字节倒退 = 重新 init，这一次就是新基线
        if matches!(last, Some((_, b)) if bytes < b) || self.pushes <= self.leading {
            self.samples.clear();
            self.samples.push_back((t, bytes));
            self.origin = Some((t, bytes));
            return;
        }
        self.samples.push_back((t, bytes));
        if self.origin.is_none() {
            self.origin = Some((t, bytes));
        }
        self.trim(t);
    }

    fn rate(&mut self, now: i64) -> f64 {
        self.trim(now);
        let (Some(&(t0, b0)), Some(&(_, b1))) = (self.samples.front(), self.samples.back()) else {
            return 0.0;
        };
        let span = now - t0;
        if span < MIN_SPAN_MS {
            return 0.0;
        }
        ((b1 - b0) as f64 * 1000.0 / span as f64).max(0.0)
    }

    fn average(&self, finished_at: i64) -> f64 {
        let (Some((t0, b0)), Some(&(_, b1))) = (self.origin, self.samples.back()) else {
            return 0.0;
        };
        let span = finished_at - t0;
        if span < 1000 {
            return 0.0;
        }
        ((b1 - b0) as f64 * 1000.0 / span as f64).max(0.0)
    }

    fn started(&self) -> bool {
        self.pushes >= self.leading.max(1)
    }

    fn trim(&mut self, now: i64) {
        let cutoff = now - WINDOW_MS;
        while self.samples.len() >= 2 && self.samples[1].0 <= cutoff {
            self.samples.pop_front();
        }
    }
}

// ── 运行期状态 ───────────────────────────────────────────────────────────────

#[derive(Default)]
struct State {
    // 新的在前，和界面展示顺序一致
    uploads: VecDeque<UploadEntry>,
    downloads: VecDeque<DownloadEntry>,
    sync_events: VecDeque<SyncEventEntry>,
    meters: HashMap<String, SpeedMeter>,
}

static APP: OnceLock<AppHandle> = OnceLock::new();
static STATE: OnceLock<Mutex<State>> = OnceLock::new();

fn state() -> &'static Mutex<State> {
    STATE.get_or_init(|| Mutex::new(State::default()))
}

fn now_ms() -> i64 {
    chrono::Local::now().timestamp_millis()
}

fn new_id() -> String {
    uuid::Uuid::new_v4().simple().to_string()
}

fn emit(change: Change) {
    if let Some(app) = APP.get() {
        let _ = app.emit("transfer-changed", change);
    }
}

/// setup 阶段调用：绑定 AppHandle，起测速 ticker。
pub fn init(app: AppHandle) {
    let _ = APP.set(app);
    // 链路卡住时没有新进度，速度必须靠定时重算才会衰减（见 SpeedMeter 注释）。
    // 空闲时每秒只是拿一次锁发现没有进行中的上传，开销可忽略。
    tauri::async_runtime::spawn(async {
        loop {
            tokio::time::sleep(Duration::from_secs(1)).await;
            let changed: Vec<UploadEntry> = {
                let mut st = state().lock();
                let now = now_ms();
                let State { uploads, meters, .. } = &mut *st;
                uploads
                    .iter_mut()
                    .filter(|u| u.status == "uploading")
                    .filter_map(|u| {
                        let speed = meters.get_mut(&u.id)?.rate(now);
                        // 速度没变就不推，免得每秒刷一遍所有窗口
                        if (speed - u.speed).abs() < 1.0 {
                            return None;
                        }
                        u.speed = speed;
                        Some(u.clone())
                    })
                    .collect()
            };
            for u in changed {
                emit(Change::Upload(u));
            }
        }
    });
}

pub fn snapshot() -> Snapshot {
    let st = state().lock();
    Snapshot {
        uploads: st.uploads.iter().cloned().collect(),
        downloads: st.downloads.iter().cloned().collect(),
        sync_events: st.sync_events.iter().cloned().collect(),
    }
}

// ── 上传 ─────────────────────────────────────────────────────────────────────

/// 登记一个上传，返回 id。`owner_window` 仅对在 WebView 里执行的上传（快传）有意义。
pub fn upload_begin(name: &str, kind: &str, total: i64, owner_window: Option<String>) -> String {
    let entry = UploadEntry {
        id: new_id(),
        name: name.to_string(),
        kind: kind.to_string(),
        total: total.max(0),
        sent: 0,
        status: "uploading".into(),
        error: None,
        started_at: now_ms(),
        finished_at: None,
        speed: 0.0,
        started: false,
        owner_window,
    };
    let leading = if kind == "manual" { 2 } else { 0 };
    {
        let mut st = state().lock();
        st.meters.insert(entry.id.clone(), SpeedMeter::new(leading));
        st.uploads.push_front(entry.clone());
        while st.uploads.len() > MAX_LIST {
            if let Some(dropped) = st.uploads.pop_back() {
                st.meters.remove(&dropped.id);
            }
        }
    }
    let id = entry.id.clone();
    emit(Change::Upload(entry));
    id
}

pub fn upload_progress(id: &str, sent: i64, total: i64) {
    let changed = {
        let mut st = state().lock();
        let State { uploads, meters, .. } = &mut *st;
        let Some(u) = uploads.iter_mut().find(|u| u.id == id && u.status == "uploading") else {
            return;
        };
        u.sent = sent.max(0);
        if total > 0 {
            u.total = total;
        }
        if let Some(m) = meters.get_mut(id) {
            let now = now_ms();
            m.push(u.sent, now);
            u.speed = m.rate(now);
            u.started = m.started();
        } else {
            u.started = true;
        }
        u.clone()
    };
    emit(Change::Upload(changed));
}

/// 结束一个上传。`error` 为 None 表示成功。
pub fn upload_finish(id: &str, error: Option<String>) {
    let changed = {
        let mut st = state().lock();
        let State { uploads, meters, .. } = &mut *st;
        let Some(u) = uploads.iter_mut().find(|u| u.id == id) else {
            return;
        };
        let now = now_ms();
        u.finished_at = Some(now);
        match error {
            None => {
                u.status = "done".into();
                if u.total > 0 {
                    u.sent = u.total;
                }
            }
            Some(e) => {
                u.status = "error".into();
                u.error = Some(e);
            }
        }
        // 完成后显示整段平均速度，比"最后一瞬间"有意义
        u.speed = meters.remove(id).map(|m| m.average(now)).unwrap_or(0.0);
        u.clone()
    };
    emit(Change::Upload(changed));
}

/// 某窗口是否还有依赖它存活的进行中上传（关窗时判断能否立刻销毁 WebView）
pub fn has_active_uploads_owned_by(window: &str) -> bool {
    state()
        .lock()
        .uploads
        .iter()
        .any(|u| u.status == "uploading" && u.owner_window.as_deref() == Some(window))
}

// ── 下载（同步引擎收到 task_created 后执行） ──────────────────────────────────

/// 按 taskId 或 path upsert；只有 downloading 状态会新建条目（与原前端逻辑一致）。
pub fn download_progress(path: &str, status: &str, task_id: Option<u64>, error: Option<String>) {
    let changed = {
        let mut st = state().lock();
        let idx = st
            .downloads
            .iter()
            .position(|d| (task_id.is_some() && d.task_id == task_id) || d.path == path);
        let idx = match idx {
            Some(i) => i,
            None if status == "downloading" => {
                st.downloads.push_front(DownloadEntry {
                    id: new_id(),
                    task_id,
                    name: path.rsplit(['/', '\\']).next().unwrap_or(path).to_string(),
                    path: path.to_string(),
                    status: "downloading".into(),
                    error: None,
                    started_at: now_ms(),
                    finished_at: None,
                });
                st.downloads.truncate(MAX_LIST);
                0
            }
            None => return,
        };
        let d = &mut st.downloads[idx];
        d.status = status.to_string();
        if error.is_some() {
            d.error = error;
        }
        if status != "downloading" {
            d.finished_at = Some(now_ms());
        }
        d.clone()
    };
    emit(Change::Download(changed));
}

// ── 同步活动 ─────────────────────────────────────────────────────────────────

/// 文件监听/服务端派发产生的一次同步动作（每次新增一条）
pub fn sync_event(path: &str, kind: &str) {
    let entry = SyncEventEntry {
        id: new_id(),
        path: path.to_string(),
        kind: kind.to_string(),
        status: kind.to_string(),
        error: None,
        time: now_ms(),
    };
    {
        let mut st = state().lock();
        st.sync_events.push_front(entry.clone());
        st.sync_events.truncate(MAX_LIST);
    }
    emit(Change::Sync(entry));
}

/// 同步引擎的上传进度：按 path upsert 进行中的那一条。
/// done/error 必须更新原 uploading 行而不是另起一行 —— 否则那一行永远停在「进行中」，
/// 指示器转圈不止（原前端逻辑里踩过的坑）。
pub fn sync_upload_progress(path: &str, status: &str, error: Option<String>) {
    let changed = {
        let mut st = state().lock();
        match st
            .sync_events
            .iter_mut()
            .find(|s| s.kind == "upload" && s.path == path && s.status == "uploading")
        {
            Some(s) => {
                s.status = status.to_string();
                s.error = error;
                s.time = now_ms();
                s.clone()
            }
            None => {
                let entry = SyncEventEntry {
                    id: new_id(),
                    path: path.to_string(),
                    kind: "upload".into(),
                    status: status.to_string(),
                    error,
                    time: now_ms(),
                };
                st.sync_events.push_front(entry.clone());
                st.sync_events.truncate(MAX_LIST);
                entry
            }
        }
    };
    emit(Change::Sync(changed));
}

// ── 清理 ─────────────────────────────────────────────────────────────────────

/// 清除已结束的记录。list = uploads / downloads / syncEvents。
/// 清完推一次 `transfer-cleared`，前端各窗口据此同步删除。
pub fn clear_finished(list: &str) {
    {
        let mut st = state().lock();
        match list {
            "uploads" => st.uploads.retain(|u| u.status == "uploading"),
            "downloads" => st.downloads.retain(|d| d.status == "downloading"),
            // 同步活动原本就是整张清空
            "syncEvents" => st.sync_events.clear(),
            _ => return,
        }
    }
    if let Some(app) = APP.get() {
        let _ = app.emit("transfer-cleared", list);
    }
}

// ── commands（前端镜像本模块）─────────────────────────────────────────────────

/// 传输列表快照：窗口（重建后）打开时取一次，之后靠 transfer-changed 事件增量更新。
#[tauri::command]
pub fn get_transfers() -> Snapshot {
    snapshot()
}

/// 登记一个在 WebView 里执行的上传（粘贴快传的 XHR）。记下发起窗口，
/// 关窗时据此判断不能立刻销毁 WebView。
#[tauri::command]
pub fn transfer_upload_begin(name: String, kind: String, total: i64, window: WebviewWindow) -> String {
    upload_begin(&name, &kind, total, Some(window.label().to_string()))
}

#[tauri::command]
pub fn transfer_upload_progress(id: String, sent: i64, total: i64) {
    upload_progress(&id, sent, total);
}

#[tauri::command]
pub fn transfer_upload_finish(id: String, error: Option<String>, app_handle: AppHandle) {
    upload_finish(&id, error);
    crate::app_shell::window::destroy_main_if_idle(&app_handle);
}

/// 清除已结束的记录：uploads / downloads / syncEvents
#[tauri::command]
pub fn transfer_clear_finished(list: String) {
    clear_finished(&list);
}
