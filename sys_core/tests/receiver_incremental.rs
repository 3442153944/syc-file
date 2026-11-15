//! write-through 收片与增量校验专项测试（task-11）。
//!
//! 从 `receiver_contract.rs` 拆出以控制单文件规模；覆盖：
//! - accept 后立即定位写盘（读回 offset 内容一致）、无内存池、重复片不重复写；
//! - 顺序多片写穿时 hasher 顺序推进、readback_bytes==0、complete 走增量快路径；
//! - 乱序 accept 时 hasher 通过页缓存回读推进，hash 仍正确；
//! - 回读失败/坏片 → 单向 fallback，finalize 走全量；补传后成功；
//! - open(persisted) 与 chunk 数过大 → incremental=false，finalize 全量；
//! - 统计断言逐片直写、无批量。
use std::ffi::CString;
use std::path::{Path, PathBuf};

use sys_core::file_core::receiver::{self, finalize_counters, last_finalize_stats, Tuning};
use sys_core::{fc_evict, fc_merkle_root, fc_preallocate, FC_OK};

// ---------------------------------------------------------------------------
// 测试夹具
// ---------------------------------------------------------------------------

struct Temp(PathBuf);

impl Temp {
    fn new() -> Self {
        Self(std::env::temp_dir().join(format!("sys-core-wt-{}", uuid::Uuid::new_v4())))
    }

    fn path(&self) -> &Path {
        &self.0
    }
}

impl Drop for Temp {
    fn drop(&mut self) {
        let c = CString::new(self.0.to_str().unwrap()).unwrap();
        fc_evict(c.as_ptr());
        let _ = std::fs::remove_file(&self.0);
    }
}

struct Session(String);

impl Session {
    fn new() -> Self {
        Self(format!("rrust-{}", uuid::Uuid::new_v4()))
    }

    fn id(&self) -> &str {
        &self.0
    }
}

impl Drop for Session {
    fn drop(&mut self) {
        let _ = receiver::close(&self.0);
    }
}

fn cstr(s: &str) -> CString {
    CString::new(s).unwrap()
}

fn prealloc(path: &Path, total: u64) {
    let c = cstr(path.to_str().unwrap());
    assert_eq!(fc_preallocate(c.as_ptr(), total), FC_OK);
}

fn pattern(total: usize) -> Vec<u8> {
    (0..total).map(|i| (i % 251) as u8).collect()
}

fn chunk_of(data: &[u8], index: u32, chunk: usize) -> &[u8] {
    let start = index as usize * chunk;
    let end = (start + chunk).min(data.len());
    &data[start..end]
}

fn leaves_of(data: &[u8], chunk: usize) -> Vec<[u8; 32]> {
    data.chunks(chunk)
        .map(|c| *blake3::hash(c).as_bytes())
        .collect()
}

fn leaves_flat(leaves: &[[u8; 32]]) -> Vec<u8> {
    leaves.iter().flat_map(|l| l.iter().copied()).collect()
}

fn merkle_root(leaves: &[[u8; 32]]) -> [u8; 32] {
    let flat = leaves_flat(leaves);
    let mut root = [0u8; 32];
    assert_eq!(
        fc_merkle_root(flat.as_ptr(), leaves.len(), root.as_mut_ptr()),
        FC_OK
    );
    root
}

fn file_hash(data: &[u8]) -> [u8; 32] {
    *blake3::hash(data).as_bytes()
}

fn open_default(id: &str, path: &Path, total: u64, chunk: u64, count: u64) {
    receiver::open(id, path, total, chunk, count, None, 0).unwrap();
}

fn accept_ok(id: &str, index: u32, data: &[u8]) {
    receiver::accept(id, index, data, None).unwrap();
}

fn flush_ok(id: &str) {
    receiver::flush(id).unwrap();
}

fn bitmap(bits: &[u8]) -> String {
    bits.iter()
        .map(|b| format!("{b:08b}"))
        .collect::<Vec<_>>()
        .join(" ")
}

/// 确定性分片内容（不保留整文件，测试侧只维护顺序 blake3 作对照）。
fn generated_chunk(index: usize, len: usize) -> Vec<u8> {
    (0..len).map(|i| ((index * 131 + i) % 251) as u8).collect()
}

// ---------------------------------------------------------------------------
// accept 即定位写盘（无内存池）
// ---------------------------------------------------------------------------

#[test]
fn accept_writes_through_immediately_and_reports_stats() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(16);
    let (chunk, total) = (4usize, 16u64);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, 4);

    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    // accept 返回后数据已在盘上：直接读 offset 内容一致
    assert_eq!(
        &std::fs::read(temp.path()).unwrap()[..4],
        chunk_of(&data, 0, chunk)
    );
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1, "每片一次物理写");
    assert_eq!(stats.max_write_bytes, 4);
    assert_eq!(stats.written_bytes, 4);
    assert_eq!(stats.hashed_bytes, 4);
    assert_eq!(stats.readback_bytes, 0);
    assert_eq!(stats.accepted, 1);
    assert_eq!(stats.persisted, 1);
    assert!(stats.incremental);
    // accepted 与 persisted 同步置位（write-through）
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "10000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "10000000"
    );

    // 顺序收完 → 无回读、增量状态就绪，complete 走快路径
    for index in 1..4 {
        accept_ok(session.id(), index, chunk_of(&data, index, chunk));
    }
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 4);
    assert_eq!(stats.readback_bytes, 0);
    assert_eq!(stats.hashed_bytes, 16);
    assert_eq!(stats.persisted, 4);

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert_eq!(fin.hashed_bytes, 16);
}

#[test]
fn duplicate_accept_does_not_write_or_count_again() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, 2);

    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    let dup = receiver::accept(session.id(), 0, chunk_of(&data, 0, chunk), None).unwrap();
    assert!(dup.duplicate);
    assert_eq!(dup.received, 1);
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1, "重复片不应重复写盘");
    assert_eq!(stats.written_bytes, 4);
    assert_eq!(stats.hashed_bytes, 4);
    assert_eq!(stats.accepted, 1);
}

// ---------------------------------------------------------------------------
// 顺序 17 片：逐片直写、无回读、增量 complete
// ---------------------------------------------------------------------------

#[test]
fn sequential_17_chunks_has_no_readback_and_fast_finalize() {
    let temp = Temp::new();
    let session = Session::new();
    let chunk = 4 << 20; // 4 MiB
    let total = 66u64 << 20; // 66 MiB，17 片（末片 2MiB）
    let count = total.div_ceil(chunk as u64);
    assert_eq!(count, 17);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, count);

    let mut reference = blake3::Hasher::new();
    for index in 0..count {
        let len = (total - index * chunk as u64).min(chunk as u64) as usize;
        let data = generated_chunk(index as usize, len);
        reference.update(&data);
        accept_ok(session.id(), index as u32, &data);
        let stats = receiver::flush_stats(session.id()).unwrap();
        assert_eq!(stats.write_calls, index + 1, "每片立即写盘");
        assert_eq!(stats.readback_bytes, 0, "顺序上传不应回读");
        assert!(stats.max_write_bytes <= chunk);
    }
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.accepted, 17);
    assert_eq!(stats.persisted, 17);
    assert_eq!(stats.hashed_bytes, total);
    assert_eq!(stats.readback_bytes, 0);
    assert_eq!(stats.max_write_bytes, chunk);

    let before = finalize_counters();
    let hash = receiver::finalize(session.id(), None, None).unwrap();
    assert_eq!(hash, *reference.finalize().as_bytes());
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert_eq!(fin.hashed_bytes, total);
    assert!(finalize_counters().0 > before.0);
    assert!(!receiver::exists(session.id()));
}

// ---------------------------------------------------------------------------
// 乱序 accept：hasher 靠回读推进，hash 仍正确
// ---------------------------------------------------------------------------

#[test]
fn out_of_order_accept_reads_back_but_hash_is_correct() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(16);
    let (chunk, total) = (4usize, 16u64);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, 4);

    // 3 → 0：0 到齐后 hasher 推进到 1（1 还缺）；2、1 到齐后回读 2,3
    accept_ok(session.id(), 3, chunk_of(&data, 3, chunk));
    assert_eq!(receiver::flush_stats(session.id()).unwrap().hashed_bytes, 0);
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    assert_eq!(receiver::flush_stats(session.id()).unwrap().hashed_bytes, 4);
    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert_eq!(receiver::flush_stats(session.id()).unwrap().hashed_bytes, 4);
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));

    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.readback_bytes, 8, "乱序补齐需回读 2,3 两片");
    assert_eq!(stats.hashed_bytes, 16);
    assert_eq!(stats.write_calls, 4);
    assert_eq!(stats.persisted, 4);
    // 每片只写一次、内容正确
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
}

// ---------------------------------------------------------------------------
// 写失败：不置位、不计数，重试成功
// ---------------------------------------------------------------------------

#[test]
fn write_failure_leaves_bits_unset_then_retry_succeeds() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    // 故意不预分配：write-through 首次写盘失败
    open_default(session.id(), temp.path(), total, chunk as u64, 2);

    assert_eq!(
        receiver::accept(session.id(), 0, chunk_of(&data, 0, chunk), None).unwrap_err(),
        receiver::ReceiverError::Io
    );
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 0);
    assert_eq!(stats.written_bytes, 0);
    assert_eq!(stats.hashed_bytes, 0);
    assert_eq!(stats.accepted, 0);
    assert_eq!(stats.persisted, 0);
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "00000000"
    );

    // 补上文件后重试本片：成功写盘并置位
    prealloc(temp.path(), total);
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1);
    assert_eq!(stats.hashed_bytes, 4);
    assert_eq!(stats.accepted, 1);
    assert_eq!(stats.persisted, 1);
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "10000000"
    );
}

// ---------------------------------------------------------------------------
// 回读失败：单向 fallback，finalize 走全量
// ---------------------------------------------------------------------------

#[test]
fn readback_failure_falls_back_to_full_finalize() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(16);
    let (chunk, total) = (4usize, 16u64);
    // 文件先长度不足 total：写穿会按需扩展；缺口补齐时 read_chunk 的尺寸校验会失败
    prealloc(temp.path(), 8);
    open_default(session.id(), temp.path(), total, chunk as u64, 4);

    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk)); // 文件扩到 12
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    // 补 1 后 hasher 需要回读 2，但此时文件长度 12 < total 16 → 回读失败 → fallback
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert!(!stats.incremental, "回读失败必须回退全量");
    assert_eq!(stats.readback_bytes, 0);
    assert_eq!(stats.hashed_bytes, 8);

    // 收尾片后文件满长，finalize 走全量 fc_finalize 仍正确
    accept_ok(session.id(), 3, chunk_of(&data, 3, chunk));
    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(!fin.incremental);
    assert_eq!(fin.file_read_bytes, total);
}

// ---------------------------------------------------------------------------
// 坏片：fast 路径发现 → 清位 + fallback，补传后全量成功
// ---------------------------------------------------------------------------

#[test]
fn leaf_mismatch_clears_bad_chunk_and_falls_back_then_resend_succeeds() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(12);
    let (chunk, total) = (4usize, 12u64);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, 3);

    let mut corrupt = chunk_of(&data, 2, chunk).to_vec();
    corrupt[0] ^= 0xFF;
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    accept_ok(session.id(), 2, &corrupt);

    let leaves = leaves_of(&data, chunk);
    let flat = leaves_flat(&leaves);
    let root = merkle_root(&leaves);
    assert_eq!(
        receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap_err(),
        receiver::ReceiverError::LeafMismatch { bad_index: 2 }
    );
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert!(fin.fallback_after, "hasher 无法回滚 → 后续走全量");
    assert!(receiver::exists(session.id()));
    // 坏块位清零（accepted/persisted 同步），received--
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11000000"
    );
    assert_eq!(receiver::flush_stats(session.id()).unwrap().accepted, 2);
    assert!(!receiver::flush_stats(session.id()).unwrap().incremental);

    // 补传正确片后走全量路径成功
    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    let fin_hash = receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap();
    assert_eq!(fin_hash, file_hash(&data));
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(!fin.incremental);
    assert_eq!(fin.file_read_bytes, total);
}

// ---------------------------------------------------------------------------
// open(persisted) / chunk 数过大 → 全量
// ---------------------------------------------------------------------------

#[test]
fn persisted_open_and_excessive_chunk_count_disable_incremental() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    prealloc(temp.path(), total);
    // 先用一个 receiver 正常写穿两份数据
    let writer = Session::new();
    open_default(writer.id(), temp.path(), total, chunk as u64, 2);
    accept_ok(writer.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(writer.id(), 1, chunk_of(&data, 1, chunk));
    receiver::close(writer.id()).unwrap();

    // 模拟 Go 重启：带 Redis persisted 位图 open → 没有增量状态
    assert!(receiver::open(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        2,
        Some(&[0b1100_0000]),
        0
    )
    .unwrap());
    assert!(!receiver::flush_stats(session.id()).unwrap().incremental);
    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    assert!(!last_finalize_stats(session.id()).unwrap().incremental);

    // chunk 数 > 262144 时禁用增量（只为内存上限；仍可正常 open）
    let big = Temp::new();
    let many = 262_145u64;
    prealloc(big.path(), many);
    let big_session = Session::new();
    assert!(receiver::open(big_session.id(), big.path(), many, 1, many, None, 0).unwrap());
    assert!(!receiver::flush_stats(big_session.id()).unwrap().incremental);
}

// ---------------------------------------------------------------------------
// Tuning 仍可注入快照间隔（write-through 无内存池参数）
// ---------------------------------------------------------------------------

#[test]
fn snapshot_interval_tuning_still_works() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    prealloc(temp.path(), total);
    assert!(receiver::open_with_tuning(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        2,
        None,
        Tuning {
            snapshot_interval: std::time::Duration::from_millis(50),
            ..Tuning::default()
        },
    )
    .unwrap());
    assert!(accept_ok_ready(session.id(), 0, chunk_of(&data, 0, chunk)));
    receiver::snapshot_ack(session.id()).unwrap();
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    let out = receiver::flush(session.id()).unwrap();
    assert!(!out.snapshot_ready, "节流窗口内不应重复发信号");
    std::thread::sleep(std::time::Duration::from_millis(80));
    assert!(receiver::flush(session.id()).unwrap().snapshot_ready);
}

fn accept_ok_ready(id: &str, index: u32, data: &[u8]) -> bool {
    receiver::accept(id, index, data, None)
        .unwrap()
        .snapshot_ready
}
