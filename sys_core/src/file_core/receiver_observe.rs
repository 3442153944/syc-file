//! 接收器的可观测性与调优参数。
//!
//! 独立成文件便于控制单文件规模；这里只有纯内存状态，不做 IO。
//! - `Tuning`：open 的可注入参数（生产用 Default，测试注入小 pending/hash_wait 上限）。
//! - `FinalizeStats`：每次 finalize 走的是增量快路径还是全量 `fc_finalize`、重读多少字节。
//!   测试用它断言「正常 complete 没有整文件重算」，运维也可用来确认回退发生率。

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Mutex, MutexGuard, OnceLock};
use std::time::Duration;

use super::receiver::{DEFAULT_TTL, PENDING_CAP, SNAPSHOT_INTERVAL};
use super::receiver_incremental::HASH_WAIT_CAP;

/// open 的可调参数。生产路径 [`Default`] 即生产配置；测试注入小值验证边界。
#[derive(Debug, Clone, Copy)]
pub struct Tuning {
    /// 单会话 pending 内存上限。
    pub pending_cap: usize,
    /// 快照信号节流间隔。
    pub snapshot_interval: Duration,
    /// 会话空闲 TTL。
    pub ttl: Duration,
    /// 增量校验 `hash_wait` 暂存上限。
    pub hash_wait_cap: usize,
}

impl Default for Tuning {
    fn default() -> Self {
        Self {
            pending_cap: PENDING_CAP,
            snapshot_interval: SNAPSHOT_INTERVAL,
            ttl: DEFAULT_TTL,
            hash_wait_cap: HASH_WAIT_CAP,
        }
    }
}

/// 一次 finalize 的路径统计（观测/测试用，不参与业务语义）。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct FinalizeStats {
    /// true = 增量快路径：只做 O(片数) 比对，不重读/重算整文件。
    pub incremental: bool,
    /// 本次 finalize 重读的字节数（快路径 0；全量路径正常为 total_size）。
    pub file_read_bytes: u64,
    /// 快路径：传输期已增量哈希的字节数；全量路径为 0。
    pub hashed_bytes: u64,
    /// 本次 finalize 后 receiver 是否已进入 fallback（后续 finalize 走全量）。
    pub fallback_after: bool,
}

/// 最近一次 finalize 统计的保留条数；超限整体清空（调试信息，丢旧无妨）。
const REPORT_CAP: usize = 1024;

fn reports() -> &'static Mutex<HashMap<String, FinalizeStats>> {
    static REPORTS: OnceLock<Mutex<HashMap<String, FinalizeStats>>> = OnceLock::new();
    REPORTS.get_or_init(|| Mutex::new(HashMap::new()))
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

pub(crate) fn record_finalize(upload_id: &str, stats: FinalizeStats) {
    let mut map = lock(reports());
    if map.len() >= REPORT_CAP && !map.contains_key(upload_id) {
        map.clear();
    }
    map.insert(upload_id.to_string(), stats);
    if stats.incremental {
        INCREMENTAL_FINALIZES.fetch_add(1, Ordering::Relaxed);
    } else {
        FULL_FINALIZES.fetch_add(1, Ordering::Relaxed);
    }
}

/// 查询某 upload_id 最近一次 finalize 的路径统计（receiver 移除后仍可查，供测试/运维）。
pub fn last_finalize_stats(upload_id: &str) -> Option<FinalizeStats> {
    lock(reports()).get(upload_id).copied()
}

static INCREMENTAL_FINALIZES: AtomicU64 = AtomicU64::new(0);
static FULL_FINALIZES: AtomicU64 = AtomicU64::new(0);

/// 进程内累计 (增量快路径 finalize 次数, 全量回退 finalize 次数)。
pub fn finalize_counters() -> (u64, u64) {
    (
        INCREMENTAL_FINALIZES.load(Ordering::Relaxed),
        FULL_FINALIZES.load(Ordering::Relaxed),
    )
}
