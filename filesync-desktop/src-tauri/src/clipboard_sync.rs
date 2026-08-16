// clipboard_sync.rs
// 剪贴板同步（Windows 端）：监听本机剪贴板变化 → 推送到服务端 → 服务端转发给其它设备；
// 收到别的设备推来的内容 → 写入本机剪贴板。
//
// ── 防回声（本文件唯一的难点）────────────────────────────────
// 把收到的内容写进本机剪贴板，会立刻触发本机的监听 → 又推上去 → 服务端再发给对方 → 无限乒乓。
// 这和文件同步踩过的「下载→探测→原样回传」是同一个坑。三道闸：
//   ① 服务端推送时排除来源设备（见 internal/clipboard）；
//   ② 本端记住「最后一次同步涉及的内容哈希」，监听到相同内容一律不推（last_synced）；
//   ③ 自己写入剪贴板后设静音窗口（MUTE_WINDOW），窗口内的变更事件直接丢弃。
// 单独任何一道都不够：①挡不住三台设备的环、②挡不住用户真的又复制了一遍同样的内容后的重复推送、
// ③挡不住写入延迟导致事件晚到窗口外。三道叠加才收敛。
//
// ── 为什么是轮询而不是系统事件 ────────────────────────────────
// arboard 不提供变更通知，Windows 的 AddClipboardFormatListener 要自己开消息窗口。
// 剪贴板是低频操作，500ms 轮询的开销可以忽略，换来的是跨平台一致的实现。
use std::sync::Arc;
use std::time::{Duration, Instant};

use arboard::Clipboard;
use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter};

use crate::api::client::ApiClient;
use crate::config::SharedSyncConfig;
use crate::logger;

/// 轮询间隔。剪贴板是人操作的，500ms 足够跟手。
const POLL_INTERVAL: Duration = Duration::from_millis(500);
/// 自己写入剪贴板后的静音窗口，窗口内不上报。
const MUTE_WINDOW: Duration = Duration::from_secs(2);
/// 单条内容上限，与服务端 maxContentBytes 保持一致（64KiB）。
const MAX_CONTENT_BYTES: usize = 64 * 1024;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClipItem {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub device_id: String,
    #[serde(default)]
    pub device_name: String,
    #[serde(default)]
    pub content_type: String,
    #[serde(default)]
    pub content: String,
    #[serde(default)]
    pub size: i64,
    #[serde(default)]
    pub created_at: i64,
}

/// 运行期状态。
pub struct ClipboardState {
    /// 是否启用同步。**默认关闭**：剪贴板里全是密码和验证码，必须用户显式打开。
    pub enabled: bool,
    /// 收到别人的内容后是否自动写入本机剪贴板（关掉则只进历史，由用户手动取用）。
    pub auto_apply: bool,
    /// 最近一次「同步过」的内容哈希：推上去的和收下来的都记，用于②。
    last_synced: u64,
    /// 自己写入剪贴板的时刻，用于③。
    muted_until: Option<Instant>,
}

impl Default for ClipboardState {
    fn default() -> Self {
        ClipboardState {
            enabled: false,
            auto_apply: true,
            last_synced: 0,
            muted_until: None,
        }
    }
}

pub type SharedClipboardState = Arc<RwLock<ClipboardState>>;

pub fn init_clipboard_state() -> SharedClipboardState {
    Arc::new(RwLock::new(ClipboardState::default()))
}

/// 内容指纹。用哈希而不是存原文：状态对象会被读写多次，不该到处留剪贴板明文副本。
fn fingerprint(s: &str) -> u64 {
    use std::collections::hash_map::DefaultHasher;
    use std::hash::{Hash, Hasher};
    let mut h = DefaultHasher::new();
    s.hash(&mut h);
    h.finish()
}

/// 启动轮询任务。整个进程一份，靠 state.enabled 控制是否真的干活。
///
/// ⚠ **自带运行时，不能用 `tokio::spawn`**：本函数在 Tauri 的 `setup()` 里被调用，
/// 而那个时机**没有 tokio 运行时上下文**（`tokio::spawn` 会直接 panic
/// "there is no reactor running"，表现为启动即 exit code 101）。
/// 既有的同步引擎启动也是同样的处理——自己起线程 + `Runtime::new()`，见 lib.rs 的 setup。
pub fn start_clipboard_watcher(
    state: SharedClipboardState,
    config: SharedSyncConfig,
    app: AppHandle,
) {
    std::thread::spawn(move || {
        let rt = match tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
        {
            Ok(rt) => rt,
            Err(e) => {
                logger::error("clipboard", format!("创建剪贴板运行时失败: {}", e));
                return;
            }
        };
        rt.block_on(watch_loop(state, config, app));
    });
}

async fn watch_loop(state: SharedClipboardState, config: SharedSyncConfig, app: AppHandle) {
    loop {
        tokio::time::sleep(POLL_INTERVAL).await;

        let (enabled, last, muted) = {
            let s = state.read();
            (s.enabled, s.last_synced, s.muted_until)
        };
        if !enabled {
            continue;
        }
        // ③ 静音窗口：刚刚是我自己写进去的，别当成用户的新复制
        if let Some(until) = muted {
            if Instant::now() < until {
                continue;
            }
        }

        let text = match read_clipboard_text() {
            Some(t) if !t.is_empty() => t,
            _ => continue,
        };
        if text.len() > MAX_CONTENT_BYTES {
            continue; // 超限静默跳过，不打扰用户
        }
        let fp = fingerprint(&text);
        // ② 与上次同步过的内容相同 → 不是新变化
        if fp == last {
            continue;
        }

        // 先记指纹再推送：推送失败也不重试，否则网络一抖就会把同一条刷屏
        state.write().last_synced = fp;

        let (client, device_id, device_name) = {
            let cfg = config.read();
            match crate::make_client_public(&cfg) {
                Ok(c) => (c, cfg.device_id.clone(), cfg.device_name.clone()),
                Err(_) => continue, // 没配服务器/未登录
            }
        };
        match push_clipboard(&client, &text, &device_id, &device_name).await {
            Ok(_) => {
                logger::info(
                    "clipboard",
                    format!("已推送剪贴板内容（{} 字节）", text.len()),
                );
                let _ = app.emit("clipboard-pushed", text.len());
            }
            Err(e) => logger::warn("clipboard", format!("推送剪贴板失败: {}", e)),
        }
    }
}

fn read_clipboard_text() -> Option<String> {
    let mut cb = Clipboard::new().ok()?;
    cb.get_text().ok()
}

/// 写入本机剪贴板，并设置静音窗口 + 记录指纹（②③两道闸都在这里落）。
pub fn apply_to_clipboard(state: &SharedClipboardState, text: &str) -> Result<(), String> {
    let mut cb = Clipboard::new().map_err(|e| format!("打开剪贴板失败: {}", e))?;
    cb.set_text(text.to_string())
        .map_err(|e| format!("写入剪贴板失败: {}", e))?;
    let mut s = state.write();
    s.last_synced = fingerprint(text);
    s.muted_until = Some(Instant::now() + MUTE_WINDOW);
    Ok(())
}

async fn push_clipboard(
    client: &ApiClient,
    content: &str,
    device_id: &str,
    device_name: &str,
) -> Result<(), String> {
    let body = serde_json::json!({
        "content": content,
        "content_type": "text",
        "device_id": device_id,
        "device_name": device_name,
    });
    let resp: crate::api::client::ApiResponse<serde_json::Value> =
        client.post("/clipboard/push", &body).await?;
    if resp.is_ok() {
        Ok(())
    } else {
        Err(resp.message)
    }
}

/// 手动推送当前剪贴板内容（供「立即推送」按钮用）。
/// 不看 enabled 开关：用户显式点了就是要推；但仍然记指纹，避免紧接着被轮询重复推一次。
pub async fn push_current(
    config: &SharedSyncConfig,
    state: &SharedClipboardState,
) -> Result<(), String> {
    let text = read_clipboard_text().ok_or_else(|| "读取剪贴板失败".to_string())?;
    if text.is_empty() {
        return Err("剪贴板为空".into());
    }
    if text.len() > MAX_CONTENT_BYTES {
        return Err(format!("内容超过上限（{} KB）", MAX_CONTENT_BYTES / 1024));
    }
    let (client, device_id, device_name) = {
        let cfg = config.read();
        (
            crate::make_client_public(&cfg)?,
            cfg.device_id.clone(),
            cfg.device_name.clone(),
        )
    };
    push_clipboard(&client, &text, &device_id, &device_name).await?;
    state.write().last_synced = fingerprint(&text);
    Ok(())
}
