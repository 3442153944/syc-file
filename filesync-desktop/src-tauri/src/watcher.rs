use notify::{Event, EventKind, RecommendedWatcher, RecursiveMode, Watcher};
use parking_lot::Mutex;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::path::PathBuf;
use std::sync::Arc;
use tauri::{AppHandle, Emitter};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileChangeEvent {
    pub paths: Vec<String>,
    pub kind: String,
}

impl FileChangeEvent {
    fn from_notify(event: Event) -> Self {
        let kind = match event.kind {
            EventKind::Create(_) => "create",
            EventKind::Modify(_) => "modify",
            EventKind::Remove(_) => "remove",
            EventKind::Access(_) => "access",
            EventKind::Other => "other",
            _ => "unknown",
        };
        FileChangeEvent {
            paths: event
                .paths
                .iter()
                .map(|p| p.to_string_lossy().to_string())
                .collect(),
            kind: kind.to_string(),
        }
    }
}

pub struct WatcherState {
    watcher: Option<RecommendedWatcher>,
    pub watched_paths: HashSet<PathBuf>,
}

impl WatcherState {
    pub fn new() -> Self {
        WatcherState {
            watcher: None,
            watched_paths: HashSet::new(),
        }
    }
}

pub type SharedWatcherState = Arc<Mutex<WatcherState>>;

pub fn init_watcher_state() -> SharedWatcherState {
    Arc::new(Mutex::new(WatcherState::new()))
}

fn ensure_watcher(state: &mut WatcherState, app_handle: AppHandle) {
    if state.watcher.is_some() {
        return;
    }
    let handle = app_handle.clone();
    let watcher = notify::recommended_watcher(move |res: Result<Event, notify::Error>| {
        match res {
            Ok(event) => {
                // 过滤掉 access 事件，只保留写入相关
                if matches!(event.kind, EventKind::Access(_)) {
                    return;
                }
                let payload = FileChangeEvent::from_notify(event);
                handle.emit("file-change", payload).ok();
            }
            Err(e) => {
                handle.emit("watch-error", e.to_string()).ok();
            }
        }
    });

    match watcher {
        Ok(w) => {
            state.watcher = Some(w);
        }
        Err(e) => {
            eprintln!("Failed to create watcher: {}", e);
        }
    }
}

pub fn add_watch_path(
    state: &SharedWatcherState,
    path: PathBuf,
    app_handle: AppHandle,
) -> Result<(), String> {
    let mut s = state.lock();
    ensure_watcher(&mut s, app_handle);

    let watcher = s.watcher.as_mut().ok_or("Watcher not initialized")?;
    watcher
        .watch(&path, RecursiveMode::Recursive)
        .map_err(|e| e.to_string())?;
    s.watched_paths.insert(path);
    Ok(())
}

pub fn remove_watch_path(state: &SharedWatcherState, path: &PathBuf) -> Result<(), String> {
    let mut s = state.lock();
    let watcher = s.watcher.as_mut().ok_or("Watcher not initialized")?;
    watcher.unwatch(path).map_err(|e| e.to_string())?;
    s.watched_paths.remove(path);
    Ok(())
}

pub fn list_watch_paths(state: &SharedWatcherState) -> Vec<String> {
    let s = state.lock();
    s.watched_paths
        .iter()
        .map(|p| p.to_string_lossy().to_string())
        .collect()
}
