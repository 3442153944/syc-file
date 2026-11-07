//! 服务端分片接收器（VM 单 Go 进程、单实例部署设计）。
//!
//! Rust 负责：有界内存缓存（pending）、accepted/persisted 双位图、abandoned 判断、
//! 相邻片合并的批量定位写、**增量校验**（accept 算叶子 + flush 增量喂整文件 blake3）、
//! finalize 的 O(片数) 比对与坏块清理。Go 只做 HTTP/鉴权/会话元数据 + 低频 Redis
//! persisted 位图快照；收片热路径 0 条 Redis。
//!
//! 为什么增量校验（方向 A）：旧路径 complete 时对整文件做一次并行 BLAKE3，465MB 需
//! ~0.66 CPU 秒 × 多核，形成 complete 峰值。现在叶子哈希摊到 accept、整文件哈希摊到
//! flush（都是单线程、随传输期分散），complete 只做 O(片数) 比对；只有「重启/快传恢复
//!（带 persisted 位图）、hash_wait 超限、叶子失配、增量状态不可用」才回退全量
//! `fc_finalize`。增量状态见 `receiver_incremental.rs`。
//!
//! 并发模型（契约 §1）：
//! - 全局 `Mutex<HashMap<String, Arc<Mutex<Receiver>>>>` 只在查找/插入/移除时短暂持有，
//!   从不持全局锁做盘 IO；写盘/校验只持对应会话的 Mutex，不同会话完全并行。
//! - 位图字节序与 Redis `SETBIT` 一致（MSB-first，offset i 对应 byte i/8 的第 `7-(i%8)` 位）。
//! - 不变式：`accepted` 不能代替 `persisted`；只有写盘成功才置 `persisted`；崩溃恢复
//!   只用传入的 Redis persisted 位图初始化，内存里仅 accepted（未 flush）的片不会出现在
//!   任何持久化路径。
//! - `closed` 标记 + 全局表移除后置位，兜住「finalize/close 成功后仍有 in-flight accept
//!   持有旧 Arc」的窗口。
//! - abandon/open 竞态：receiver 不存在时 abandon 先落全局缓冲并**复查**，open 先注册
//!   再合并缓冲（见 [`abandon`] / [`open_with_tuning`] 注释），保证「先 abandon 后 open」
//!   与「open 途中 abandon」都不漏判。
//! - 服务端按单 Go 进程、单实例部署设计：receiver 是进程内状态，多实例需要实例亲和，
//!   不在本模块实现。
//!
//! 本模块是 typed 语义层（pub，供 Rust 测试直接调用）；C ABI 薄包装见 `receiver_ffi.rs`。

use std::collections::{BTreeMap, HashMap, HashSet, VecDeque};
use std::sync::{Arc, Mutex, MutexGuard, OnceLock};
use std::time::{Duration, Instant};

use super::file_ops::{
    FC_ERR_ABANDONED, FC_ERR_ARG, FC_ERR_IO, FC_ERR_LEAF_MISMATCH, FC_ERR_NOT_FOUND,
    FC_ERR_ROOT_MISMATCH, FC_ERR_SIZE_MISMATCH, MAX_CHUNK_SIZE,
};
use super::receiver_bitset::{bitmap_len, Bitset};
use super::receiver_incremental::IncrementalState;

pub use super::receiver_observe::{finalize_counters, last_finalize_stats, Tuning};

/// 单会话 pending 内存上限。超出前先把 pending 合并落盘；允许最多超出一个 chunk
///（chunk_size 上限 1 GiB 在 open 时校验），换取「不在热路径做零散小写」。
pub const PENDING_CAP: usize = 64 << 20; // 64 MiB

/// open 未指定 ttl（ttl_secs=0）时的默认会话 TTL，与 Go 侧 24h 保持一致。
pub const DEFAULT_TTL: Duration = Duration::from_secs(24 * 3600);

/// 快照节流间隔：dirty 时最多每 1s 通知一次 Go 落 Redis persisted 位图。
/// 普通收片在间隔内应为 0 条 Redis 命令（契约 §5）。
pub const SNAPSHOT_INTERVAL: Duration = Duration::from_secs(1);

/// 全局机会式 GC 节流：所有 FFI 入口最多每 60s 扫一次过期会话/缓冲。
const GC_INTERVAL: Duration = Duration::from_secs(60);

/// abandon 全局缓冲：open 之前到达的 abandon 记录。上限 4096 个 upload_id，
/// 每个 id 最多 256 条，24h 过期（契约 §1）。
const ABANDON_BUFFER_MAX_IDS: usize = 4096;
const ABANDON_BUFFER_MAX_ENTRIES: usize = 256;
const ABANDON_BUFFER_TTL: Duration = Duration::from_secs(24 * 3600);

/// 单 receiver 的 abandoned 集合上限。正常客户端一个分片的 attempt 数很小，
/// 该上限只防御异常客户端把内存打爆，超限后新记录丢弃（仍保证热路径正确性）。
const MAX_ABANDONED_PER_RECEIVER: usize = 16 * 1024;

/// upload_id 长度上限：会话 id 是进程内哈希键 + Redis key 的一部分，防御异常输入。
const MAX_UPLOAD_ID_LEN: usize = 256;

/// attempt_id 长度上限：与 upload_chunk.go 的参数校验保持一致（防御异常输入）。
const MAX_ATTEMPT_ID_LEN: usize = 256;

/// 分片数上限：与契约 §3 一致（位图最大 2 MiB，单会话内存可控）。
const MAX_CHUNK_COUNT: u64 = 1 << 24;

/// blake3 哈希字节数（与 file_ops 内部常量一致；本模块只需 finalize 的哈希长度）。
pub(crate) const HASH_SIZE: usize = 32;

/// typed 语义错误；C ABI 层映射为 `FC_ERR_*` 负错误码。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReceiverError {
    /// 参数非法（空 id、越界 index、长度不符、位图长度不符等）。
    Arg,
    /// 写盘/读盘失败。
    Io,
    /// receiver 不存在（或已 closed/finalize 移除）。
    NotFound,
    /// 该 `(index, attempt_id)` 已被放弃。
    Abandoned,
    /// finalize 叶子不匹配；`bad_index` 为首个坏块（-1 表示服务端未给出）。
    LeafMismatch { bad_index: i64 },
    /// finalize Merkle 树根不匹配。
    RootMismatch,
    /// finalize 文件实际尺寸/分片数与描述不符。
    SizeMismatch,
}

impl ReceiverError {
    /// C ABI 错误码。
    pub fn code(self) -> i32 {
        match self {
            ReceiverError::Arg => FC_ERR_ARG,
            ReceiverError::Io => FC_ERR_IO,
            ReceiverError::NotFound => FC_ERR_NOT_FOUND,
            ReceiverError::Abandoned => FC_ERR_ABANDONED,
            ReceiverError::LeafMismatch { .. } => FC_ERR_LEAF_MISMATCH,
            ReceiverError::RootMismatch => FC_ERR_ROOT_MISMATCH,
            ReceiverError::SizeMismatch => FC_ERR_SIZE_MISMATCH,
        }
    }
}

/// accept/flush 的返回结果（对应 C ABI `fc_accept_result`）。
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct AcceptOutcome {
    /// 已接受分片数（accepted 计数，不重复计数）。
    pub received: i64,
    /// accepted 是否已覆盖全部分片。
    pub complete: bool,
    /// 是否该向 Go 发 persisted 快照信号（dirty 且过了节流窗口）。
    pub snapshot_ready: bool,
    /// 该片此前已接受（幂等重复）。
    pub duplicate: bool,
}

/// 本会话观测统计（测试观测批量合并 / 增量校验进度用，不参与业务）。
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct FlushStats {
    /// 物理写调用次数（相邻片合并后一组算一次）。
    pub write_calls: u64,
    /// 累计写盘字节数。
    pub written_bytes: u64,
    /// 增量校验：已按文件序喂入顺序整文件 hasher 的字节数。
    pub hashed_bytes: u64,
    /// 增量校验：乱序落盘、等待前序空洞的暂存字节数。
    pub hash_wait_bytes: usize,
    /// 增量校验状态是否仍有效（false = 已回退全量 finalize）。
    pub incremental: bool,
    /// 已算出叶子的分片数。
    pub leaf_digests_ready: usize,
    /// 已接受分片数。
    pub accepted: i64,
}

/// 单会话接收器状态。字段与契约 §1 一一对应。
///
/// 可见性：`pub(crate)` 仅为让 `receiver_finalize.rs` 复用同一份状态（finalize 拆文件以
/// 控制单文件规模）；crate 外无法访问，C ABI 只走 receiver_ffi.rs。
pub(crate) struct Receiver {
    pub(crate) path: std::path::PathBuf,
    pub(crate) total_size: u64,
    pub(crate) chunk_size: u64,
    pub(crate) chunk_count: u32,
    pub(crate) accepted: Bitset,
    pub(crate) persisted: Bitset,
    pub(crate) received: i64,
    pending: BTreeMap<u32, Vec<u8>>,
    pending_bytes: usize,
    abandoned: HashSet<(u32, String)>,
    ttl: Duration,
    pub(crate) last_activity: Instant,
    last_snapshot_at: Option<Instant>,
    /// persisted 位图有 Go 未 ack 的变化。
    pub(crate) dirty: bool,
    /// receiver 已从全局表移除（finalize/close/GC）；stale Arc 的后续调用一律 NotFound。
    pub(crate) closed: bool,
    pending_cap: usize,
    snapshot_interval: Duration,
    stats: FlushStats,
    /// 增量校验状态（叶子 + 顺序整文件 hasher + 乱序暂存）。
    pub(crate) inc: IncrementalState,
}

impl Receiver {
    fn new(
        path: std::path::PathBuf,
        total_size: u64,
        chunk_size: u64,
        chunk_count: u32,
        persisted: Option<&Bitset>,
        ttl: Duration,
        pending_cap: usize,
        snapshot_interval: Duration,
        hash_wait_cap: usize,
    ) -> Self {
        // 有 persisted 位图 = 崩溃恢复/快传秒传，没有增量状态，finalize 直接走全量路径。
        let has_persisted = persisted.is_some();
        let (accepted, persisted_bits) = match persisted {
            Some(bits) => (bits.clone(), bits.clone()),
            None => (Bitset::new(chunk_count), Bitset::new(chunk_count)),
        };
        let received = accepted.count();
        Self {
            path,
            total_size,
            chunk_size,
            chunk_count,
            accepted,
            persisted: persisted_bits,
            received,
            pending: BTreeMap::new(),
            pending_bytes: 0,
            abandoned: HashSet::new(),
            ttl,
            last_activity: Instant::now(),
            last_snapshot_at: None,
            dirty: false,
            closed: false,
            pending_cap,
            snapshot_interval,
            stats: FlushStats::default(),
            inc: IncrementalState::new(chunk_count, !has_persisted, hash_wait_cap),
        }
    }

    fn matches(
        &self,
        path: &std::path::Path,
        total_size: u64,
        chunk_size: u64,
        count: u32,
    ) -> bool {
        self.path == path
            && self.total_size == total_size
            && self.chunk_size == chunk_size
            && self.chunk_count == count
    }

    /// 该 index 的合法分片长度（末片 = total_size - index*chunk_size）。
    fn expected_len(&self, index: u32) -> u64 {
        let offset = index as u64 * self.chunk_size;
        self.total_size.saturating_sub(offset).min(self.chunk_size)
    }

    fn record_abandon(&mut self, index: u32, attempt_id: String) {
        if self.abandoned.len() < MAX_ABANDONED_PER_RECEIVER {
            self.abandoned.insert((index, attempt_id));
        }
    }

    fn merge_abandons(&mut self, entries: Vec<(u32, String)>) {
        for (index, attempt) in entries {
            self.record_abandon(index, attempt);
        }
    }

    /// 快照节流：dirty 且距上次信号 ≥ snapshot_interval 时返回 true 并刷新信号时间。
    /// 只更新时间戳、保持 dirty（直到 Go snapshot_ack）。
    fn snapshot_due(&mut self, now: Instant) -> bool {
        if !self.dirty {
            return false;
        }
        let due = match self.last_snapshot_at {
            Some(at) => now.saturating_duration_since(at) >= self.snapshot_interval,
            None => true,
        };
        if due {
            self.last_snapshot_at = Some(now);
        }
        due
    }

    fn outcome(&mut self, now: Instant, duplicate: bool) -> AcceptOutcome {
        AcceptOutcome {
            received: self.received,
            complete: self.received >= self.chunk_count as i64,
            snapshot_ready: self.snapshot_due(now),
            duplicate,
        }
    }

    fn run_bytes(&self, start: u32, end: u32) -> usize {
        (start..=end)
            .map(|i| self.pending.get(&i).map_or(0, Vec::len))
            .sum()
    }

    /// 把 pending 按 index 顺序、相邻片合并成一次定位写。
    /// 每组写成功后才置 persisted / 移出 pending / 扣 pending_bytes；失败立即停止，
    /// 前面已成功的组照常落盘（契约 §3）。写成功组同时喂增量整文件 hasher。
    pub(crate) fn do_flush(&mut self) -> Result<(), ReceiverError> {
        if self.pending.is_empty() {
            return Ok(());
        }
        let now = Instant::now();
        self.last_activity = now;

        // 先按 BTreeMap 顺序把连续 index 分组
        let mut runs: Vec<(u32, u32)> = Vec::new();
        let mut keys = self.pending.keys().copied();
        if let Some(first) = keys.next() {
            let (mut start, mut end) = (first, first);
            for k in keys {
                if k == end + 1 {
                    end = k;
                } else {
                    runs.push((start, end));
                    start = k;
                    end = k;
                }
            }
            runs.push((start, end));
        }

        for (start, end) in runs {
            let offset = start as u64 * self.chunk_size;
            // 多片合并成一段连续内存做一次写：pending 上限 64 MiB，多一次拷贝换一次
            // 系统调用 + 一次句柄获取，热路径明显更划算；这同一块内存随后直接喂增量 hasher。
            let merged = if start == end {
                None
            } else {
                let mut buf = Vec::with_capacity(self.run_bytes(start, end));
                for i in start..=end {
                    buf.extend_from_slice(&self.pending[&i]);
                }
                Some(buf)
            };
            let result = match &merged {
                Some(buf) => super::file_ops::write_chunk_unchecked(&self.path, offset, buf),
                None => super::file_ops::write_chunk_unchecked(
                    &self.path,
                    offset,
                    &self.pending[&start],
                ),
            };
            self.stats.write_calls += 1;
            match result {
                Ok(()) => {
                    // 只有写盘成功才置 persisted / 移出 pending，并喂增量整文件 hasher。
                    // 写失败组保留 pending 且不进 hasher，下次重试成功后再喂。
                    let data = match merged {
                        Some(buf) => {
                            for i in start..=end {
                                self.pending.remove(&i);
                                self.persisted.set(i);
                            }
                            buf
                        }
                        None => {
                            let data = self.pending.remove(&start).expect("pending 组已存在");
                            self.persisted.set(start);
                            data
                        }
                    };
                    let written = data.len();
                    self.pending_bytes = self.pending_bytes.saturating_sub(written);
                    self.stats.written_bytes += written as u64;
                    self.dirty = true;
                    self.inc.on_written(offset, data);
                }
                // 写失败：本组及之后 pending 原样保留，persisted 不置位
                Err(()) => return Err(ReceiverError::Io),
            }
        }
        Ok(())
    }
}

// ---------------------------------------------------------------------------
// 全局注册表 / abandon 缓冲 / GC
// ---------------------------------------------------------------------------

pub(crate) fn registry() -> &'static Mutex<HashMap<String, Arc<Mutex<Receiver>>>> {
    static REGISTRY: OnceLock<Mutex<HashMap<String, Arc<Mutex<Receiver>>>>> = OnceLock::new();
    REGISTRY.get_or_init(|| Mutex::new(HashMap::new()))
}

struct AbandonBucket {
    last_touch: Instant,
    entries: VecDeque<(u32, String)>,
}

#[derive(Default)]
struct AbandonBuffer {
    buckets: HashMap<String, AbandonBucket>,
}

fn abandons() -> &'static Mutex<AbandonBuffer> {
    static ABANDONS: OnceLock<Mutex<AbandonBuffer>> = OnceLock::new();
    ABANDONS.get_or_init(|| Mutex::new(AbandonBuffer::default()))
}

fn gc_state() -> &'static Mutex<Option<Instant>> {
    static GC_STATE: OnceLock<Mutex<Option<Instant>>> = OnceLock::new();
    GC_STATE.get_or_init(|| Mutex::new(None))
}

/// 加锁并忽略毒化：receiver 状态在任意中间态都自洽（要么完整插入，要么未插入），
/// 毒化后继续用远好于让整个进程的接收器永久失败。
pub(crate) fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

/// 全局表查找（短锁，不做任何 IO）。
pub(crate) fn lookup(upload_id: &str) -> Result<Arc<Mutex<Receiver>>, ReceiverError> {
    lock(registry())
        .get(upload_id)
        .cloned()
        .ok_or(ReceiverError::NotFound)
}

/// 把 abandon 记录放进全局缓冲（receiver 尚不存在时）。超限丢最旧的 upload_id /
/// 每 id 最旧的一条（契约 §1 缓冲上限）。
fn buffer_abandon(upload_id: &str, index: u32, attempt_id: &str) {
    let now = Instant::now();
    let mut buf = lock(abandons());
    if !buf.buckets.contains_key(upload_id) && buf.buckets.len() >= ABANDON_BUFFER_MAX_IDS {
        // 4096 规模线性找最旧，只在超限时发生（低频、可忽略）
        if let Some(oldest) = buf
            .buckets
            .iter()
            .min_by_key(|(_, b)| b.last_touch)
            .map(|(id, _)| id.clone())
        {
            buf.buckets.remove(&oldest);
        }
    }
    let bucket = buf
        .buckets
        .entry(upload_id.to_string())
        .or_insert_with(|| AbandonBucket {
            last_touch: now,
            entries: VecDeque::new(),
        });
    bucket.last_touch = now;
    if bucket.entries.len() >= ABANDON_BUFFER_MAX_ENTRIES {
        bucket.entries.pop_front();
    }
    bucket.entries.push_back((index, attempt_id.to_string()));
}

/// 取走某 upload_id 的全部缓冲 abandon 记录（open 合并时调用）。
fn take_buffered_abandons(upload_id: &str) -> Vec<(u32, String)> {
    let bucket = lock(abandons()).buckets.remove(upload_id);
    match bucket {
        Some(b) => b.entries.into_iter().collect(),
        None => Vec::new(),
    }
}

/// 机会式 GC：全局节流约 60s 一次，所有 FFI 入口调用。只扫描内存状态，不做 IO。
pub(crate) fn maybe_collect_expired() {
    let now = Instant::now();
    {
        let mut last = lock(gc_state());
        if let Some(at) = *last {
            if now.saturating_duration_since(at) < GC_INTERVAL {
                return;
            }
        }
        *last = Some(now);
    }
    collect_expired(now);
}

/// 立即执行一次过期回收（测试/运维手动触发用，忽略 60s 节流）。
pub fn collect_expired_now() {
    collect_expired(Instant::now());
}

fn collect_expired(now: Instant) {
    // 1) 空闲超过 ttl 的 receiver：移除前先在会话锁内置 closed，挡住 in-flight accept。
    //    全局锁内只做内存状态扫描；会话锁用 try_lock —— 正在做写盘/校验 IO 的会话
    //    本轮跳过（下轮再收），绝不让全局锁被慢 IO 拖住（契约 §1「全局锁内不做 IO」）。
    let mut ids = Vec::new();
    {
        let mut map = lock(registry());
        for (id, arc) in map.iter() {
            let Ok(mut r) = arc.try_lock() else {
                continue; // 会话正被使用（可能正在 IO），本轮跳过
            };
            if now.saturating_duration_since(r.last_activity) > r.ttl {
                r.closed = true;
                ids.push(id.clone());
            }
        }
        for id in ids {
            map.remove(&id);
        }
    }

    // 2) 过期 abandon 缓冲
    lock(abandons())
        .buckets
        .retain(|_, b| now.saturating_duration_since(b.last_touch) <= ABANDON_BUFFER_TTL);
}

// ---------------------------------------------------------------------------
// open / exists
// ---------------------------------------------------------------------------

/// open 参数防御校验（契约 §3）。返回校验后的 chunk_count。
fn validate_open(
    upload_id: &str,
    path: &std::path::Path,
    total_size: u64,
    chunk_size: u64,
    chunk_count: u64,
) -> Result<u32, ReceiverError> {
    if upload_id.is_empty() || upload_id.len() > MAX_UPLOAD_ID_LEN {
        return Err(ReceiverError::Arg);
    }
    if path.as_os_str().is_empty() {
        return Err(ReceiverError::Arg);
    }
    if total_size == 0 {
        return Err(ReceiverError::Arg);
    }
    if chunk_size == 0 || chunk_size > MAX_CHUNK_SIZE {
        return Err(ReceiverError::Arg);
    }
    if chunk_count != total_size.div_ceil(chunk_size) || chunk_count > MAX_CHUNK_COUNT {
        return Err(ReceiverError::Arg);
    }
    Ok(chunk_count as u32)
}

/// 校验并解析 persisted 位图：NULL/空 → None；否则长度必须恰好 ceil(chunk_count/8)。
fn parse_persisted(
    persisted: Option<&[u8]>,
    chunk_count: u32,
) -> Result<Option<Bitset>, ReceiverError> {
    match persisted {
        None => Ok(None),
        Some(bytes) if bytes.is_empty() => Ok(None),
        Some(bytes) => {
            if bytes.len() != bitmap_len(chunk_count) {
                return Err(ReceiverError::Arg);
            }
            Ok(Some(Bitset::from_bytes(bytes, chunk_count)))
        }
    }
}

/// 打开/注册会话接收器。`ttl_secs = 0` 用 24h 默认。
/// 返回 `Ok(true)` = 本次新建；`Ok(false)` = 已存在（参数一致，未覆盖现有状态）。
pub fn open(
    upload_id: &str,
    path: &std::path::Path,
    total_size: u64,
    chunk_size: u64,
    chunk_count: u64,
    persisted: Option<&[u8]>,
    ttl_secs: u64,
) -> Result<bool, ReceiverError> {
    let mut tuning = Tuning::default();
    if ttl_secs != 0 {
        tuning.ttl = Duration::from_secs(ttl_secs);
    }
    open_with_tuning(
        upload_id,
        path,
        total_size,
        chunk_size,
        chunk_count,
        persisted,
        tuning,
    )
}

/// `open` 的可调版本：测试注入小 `pending_cap` / 短 `snapshot_interval` / 短 TTL /
/// 小 `hash_wait_cap`。生产路径由 [`open`] 使用 [`Tuning::default`]。
pub fn open_with_tuning(
    upload_id: &str,
    path: &std::path::Path,
    total_size: u64,
    chunk_size: u64,
    chunk_count: u64,
    persisted: Option<&[u8]>,
    tuning: Tuning,
) -> Result<bool, ReceiverError> {
    let count = validate_open(upload_id, path, total_size, chunk_size, chunk_count)?;
    let persisted_bits = parse_persisted(persisted, count)?;
    maybe_collect_expired();

    // 先创建再注册：注册用「不存在才插入」，已存在（含 closed stale 条目）不覆盖。
    // 注册成功后再合并「open 之前到达」的 abandon 缓冲 —— 这个顺序与 abandon 侧的
    // 「先落缓冲再复查 receiver」配合，闭合 abandon/open 的竞态窗口（契约 §1）。
    let receiver = Arc::new(Mutex::new(Receiver::new(
        path.to_path_buf(),
        total_size,
        chunk_size,
        count,
        persisted_bits.as_ref(),
        tuning.ttl,
        tuning.pending_cap,
        tuning.snapshot_interval,
        tuning.hash_wait_cap,
    )));
    loop {
        let existing = {
            let mut map = lock(registry());
            match map.get(upload_id) {
                None => {
                    map.insert(upload_id.to_string(), Arc::clone(&receiver));
                    None
                }
                Some(arc) => Some(Arc::clone(arc)),
            }
        };
        let Some(existing) = existing else {
            break;
        };
        let (closed, same) = {
            let r = lock(&existing);
            (r.closed, r.matches(path, total_size, chunk_size, count))
        };
        if !closed {
            return if same {
                Ok(false)
            } else {
                Err(ReceiverError::Arg)
            };
        }
        // closed 的 stale 条目（GC 与 open 的极小竞态窗口）：摘除后重试注册
        let mut map = lock(registry());
        if map
            .get(upload_id)
            .is_some_and(|cur| Arc::ptr_eq(cur, &existing))
        {
            map.remove(upload_id);
        }
    }
    let buffered = take_buffered_abandons(upload_id);
    if !buffered.is_empty() {
        lock(&receiver).merge_abandons(buffered);
    }
    Ok(true)
}

/// receiver 是否存在（closed/已移除均算不存在）。
pub fn exists(upload_id: &str) -> bool {
    maybe_collect_expired();
    match {
        let map = lock(registry());
        map.get(upload_id).cloned()
    } {
        Some(arc) => !lock(&arc).closed,
        None => false,
    }
}

// ---------------------------------------------------------------------------
// accept / flush
// ---------------------------------------------------------------------------

/// 接收一个分片：校验顺序 = receiver 存在 → index 边界 → 长度符合 →
/// attempt 是否 abandoned → 是否 duplicate。数据拷贝进 pending，调用方保留所有权。
pub fn accept(
    upload_id: &str,
    index: u32,
    data: &[u8],
    attempt_id: Option<&str>,
) -> Result<AcceptOutcome, ReceiverError> {
    maybe_collect_expired();
    let arc = lookup(upload_id)?;
    let mut r = lock(&arc);
    if r.closed {
        return Err(ReceiverError::NotFound);
    }
    if index >= r.chunk_count {
        return Err(ReceiverError::Arg);
    }
    if data.len() as u64 != r.expected_len(index) {
        return Err(ReceiverError::Arg);
    }
    // abandoned 优先于 duplicate：旧 attempt 的延迟请求必须 409，即使该片已被接受。
    // attempt_id 为 NULL/空表示不校验（兼容旧客户端）。集合通常很小，线性查避免热路径
    // 为 contains 构造 String 分配。
    if let Some(attempt) = attempt_id {
        if !attempt.is_empty() && r.abandoned.iter().any(|(i, a)| *i == index && a == attempt) {
            return Err(ReceiverError::Abandoned);
        }
    }
    let now = Instant::now();
    r.last_activity = now;

    if r.accepted.get(index) {
        // 幂等重复：received 不重复计数，但快照信号照常按 dirty/节流计算。
        return Ok(r.outcome(now, true));
    }

    // 有界内存：接受新片前，若会超上限且 pending 非空，先合并落盘。
    // flush 失败则本片不受理、pending 原样保留，客户端重试本片（契约 §1）。
    if r.pending_bytes.saturating_add(data.len()) > r.pending_cap && !r.pending.is_empty() {
        r.do_flush()?;
    }

    // 增量校验：收片时单线程算叶子（约 2–3ms/4MiB，摊到传输期），complete 不再重算。
    r.inc.record_chunk(index, data);
    r.pending.insert(index, data.to_vec());
    r.pending_bytes += data.len();
    r.accepted.set(index);
    r.received += 1;
    // 注意：accept 本身只改 accepted，不置 dirty —— Redis 只快照 persisted。
    Ok(r.outcome(now, false))
}

/// 把 pending 按 index 排序、合并相邻片后批量定位写。out 可为 NULL（C ABI 层）。
pub fn flush(upload_id: &str) -> Result<AcceptOutcome, ReceiverError> {
    maybe_collect_expired();
    let arc = lookup(upload_id)?;
    let mut r = lock(&arc);
    if r.closed {
        return Err(ReceiverError::NotFound);
    }
    let now = Instant::now();
    r.last_activity = now;
    r.do_flush()?;
    Ok(r.outcome(now, false))
}

// ---------------------------------------------------------------------------
// 位图 / 快照 ack
// ---------------------------------------------------------------------------

/// 导出 accepted 位图（MSB-first，长度 ceil(chunk_count/8)）。
pub fn bitmap_accepted(upload_id: &str) -> Result<Vec<u8>, ReceiverError> {
    maybe_collect_expired();
    let arc = lookup(upload_id)?;
    let r = lock(&arc);
    if r.closed {
        return Err(ReceiverError::NotFound);
    }
    Ok(r.accepted.as_bytes().to_vec())
}

/// 导出 persisted 位图（MSB-first，长度 ceil(chunk_count/8)）。崩溃恢复只能用它。
pub fn bitmap_persisted(upload_id: &str) -> Result<Vec<u8>, ReceiverError> {
    maybe_collect_expired();
    let arc = lookup(upload_id)?;
    let r = lock(&arc);
    if r.closed {
        return Err(ReceiverError::NotFound);
    }
    Ok(r.persisted.as_bytes().to_vec())
}

/// Go 成功 PersistBitmap 后调用：清 dirty（下个 interval 后才再发快照信号）。
pub fn snapshot_ack(upload_id: &str) -> Result<(), ReceiverError> {
    maybe_collect_expired();
    let arc = lookup(upload_id)?;
    let mut r = lock(&arc);
    if r.closed {
        return Err(ReceiverError::NotFound);
    }
    r.dirty = false;
    Ok(())
}

// ---------------------------------------------------------------------------
// abandon / close
// ---------------------------------------------------------------------------

/// 记录放弃的 `(index, attempt_id)`。receiver 不存在时进全局缓冲，open 时合并；
/// 幂等（重复记录无害）。`attempt_id` 不可空。
pub fn abandon(upload_id: &str, index: u32, attempt_id: &str) -> Result<(), ReceiverError> {
    if upload_id.is_empty() || upload_id.len() > MAX_UPLOAD_ID_LEN {
        return Err(ReceiverError::Arg);
    }
    if attempt_id.is_empty() || attempt_id.len() > MAX_ATTEMPT_ID_LEN {
        return Err(ReceiverError::Arg);
    }
    maybe_collect_expired();

    // 热路径：receiver 已存在 → 只锁会话，不碰全局缓冲（收片期间零全局锁竞争）。
    let existing = {
        let map = lock(registry());
        map.get(upload_id).cloned()
    };
    if let Some(arc) = existing {
        let mut r = lock(&arc);
        if !r.closed {
            r.record_abandon(index, attempt_id.to_string());
            return Ok(());
        }
        // 已 closed 的 stale 会话：按「不存在」处理，落缓冲等下一次 open 合并
        drop(r);
    }

    // receiver 不存在：先落缓冲，再复查。与 open 的「先注册后合并缓冲」配合，
    // 保证无论 abandon/open 谁先执行，记录最终都会进到新建的 receiver 里。
    buffer_abandon(upload_id, index, attempt_id);
    let raced = {
        let map = lock(registry());
        map.get(upload_id).cloned()
    };
    if let Some(arc) = raced {
        let buffered = take_buffered_abandons(upload_id);
        let mut r = lock(&arc);
        if r.closed {
            // 会话刚被移除：把已取出的记录放回缓冲，等下一次 open 合并
            drop(r);
            for (i, a) in buffered {
                buffer_abandon(upload_id, i, &a);
            }
            buffer_abandon(upload_id, index, attempt_id);
        } else {
            // buffered 可能已被 open 抢先合并（空列表）或仍待合并；无条件再记一次
            // 本条目即可（HashSet 幂等），避免任何交错下漏判。
            r.merge_abandons(buffered);
            r.record_abandon(index, attempt_id.to_string());
        }
    }
    Ok(())
}

/// 移除 receiver（不 flush、不写盘）。幂等，不存在返回 Ok。
pub fn close(upload_id: &str) -> Result<(), ReceiverError> {
    if upload_id.is_empty() || upload_id.len() > MAX_UPLOAD_ID_LEN {
        return Err(ReceiverError::Arg);
    }
    maybe_collect_expired();
    let removed = {
        let mut map = lock(registry());
        map.remove(upload_id)
    };
    if let Some(arc) = removed {
        lock(&arc).closed = true;
    }
    Ok(())
}

/// 查询某会话的观测统计（批量写次数 / 增量校验进度）。不存在或已移除返回 None。
pub fn flush_stats(upload_id: &str) -> Option<FlushStats> {
    let arc = {
        let map = lock(registry());
        map.get(upload_id).cloned()
    }?;
    let r = lock(&arc);
    if r.closed {
        return None;
    }
    Some(FlushStats {
        hashed_bytes: r.inc.hashed_bytes(),
        hash_wait_bytes: r.inc.wait_bytes(),
        incremental: r.inc.enabled(),
        leaf_digests_ready: r.inc.digest_ready(),
        accepted: r.received,
        ..r.stats
    })
}

// ---------------------------------------------------------------------------
// finalize
// ---------------------------------------------------------------------------
//
// finalize 的增量快路径 / 全量回退实现放在 `receiver_finalize.rs`（控制单文件规模），
// 这里只做 re-export，typed/FFI 调用方路径不变：`receiver::finalize(...)`。

pub use super::receiver_finalize::finalize;
