//! 服务端分片接收器契约测试（契约 §8 Rust 全部用例）。
//!
//! 每个用例使用唯一 upload_id，结束 close（`Session` 的 Drop 兜底），避免全局注册表
//! 状态互相干扰。临时文件用 `Temp` 管理，Drop 时先 `fc_evict` 再删除。
use std::ffi::CString;
use std::path::{Path, PathBuf};
use std::time::Duration;

use sys_core::file_core::receiver::{self, AcceptOutcome, ReceiverError};
use sys_core::{
    fc_abi_version, fc_evict, fc_merkle_root, fc_preallocate, FcAcceptResult, FC_ERR_ARG,
    FC_ERR_LEAF_MISMATCH, FC_ERR_NOT_FOUND, FC_OK,
};

// ---------------------------------------------------------------------------
// 测试夹具
// ---------------------------------------------------------------------------

struct Temp(PathBuf);

impl Temp {
    fn new() -> Self {
        Self(std::env::temp_dir().join(format!("sys-core-recv-{}", uuid::Uuid::new_v4())))
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

/// 唯一 upload_id + 结束自动 close（避免全局状态串扰）。
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

/// 用真实 C ABI 预分配临时文件（Go 侧 init 的等价动作）。
fn prealloc(path: &Path, total: u64) {
    let c = cstr(path.to_str().unwrap());
    assert_eq!(fc_preallocate(c.as_ptr(), total), FC_OK);
}

fn pattern(total: usize) -> Vec<u8> {
    (0..total).map(|i| (i % 251) as u8).collect()
}

/// 按 chunk_size 切片的期望叶子哈希。
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

fn chunk_of(data: &[u8], index: u32, chunk: usize) -> &[u8] {
    let start = index as usize * chunk;
    let end = (start + chunk).min(data.len());
    &data[start..end]
}

fn open_default(id: &str, path: &Path, total: u64, chunk: u64, count: u64) -> bool {
    receiver::open(id, path, total, chunk, count, None, 0).unwrap()
}

fn accept_ok(id: &str, index: u32, data: &[u8]) -> AcceptOutcome {
    receiver::accept(id, index, data, None).unwrap()
}

fn accept_attempt(
    id: &str,
    index: u32,
    data: &[u8],
    attempt: &str,
) -> Result<AcceptOutcome, ReceiverError> {
    receiver::accept(id, index, data, Some(attempt))
}

fn flush_ok(id: &str) -> AcceptOutcome {
    receiver::flush(id).unwrap()
}

fn bitmap(bits: &[u8]) -> String {
    bits.iter()
        .map(|b| format!("{b:08b}"))
        .collect::<Vec<_>>()
        .join(" ")
}

// ---------------------------------------------------------------------------
// 乱序 / 重复 / 边界
// ---------------------------------------------------------------------------

#[test]
fn out_of_order_and_duplicate_accept_then_finalize_ok() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(14);
    let (chunk, total) = (4usize, 14u64);
    let count = total.div_ceil(chunk as u64);
    prealloc(temp.path(), total);

    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        count
    ));
    assert!(receiver::exists(session.id()));

    // 乱序：3,1,0,2（末片 2 字节）
    for (n, index) in [3u32, 1, 0, 2].iter().enumerate() {
        let out = accept_ok(session.id(), *index, chunk_of(&data, *index, chunk));
        assert_eq!(out.received, n as i64 + 1);
        assert!(!out.duplicate);
        assert_eq!(out.complete, n == 3);
    }

    // 幂等重复：不重复计数
    let dup = accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    assert!(dup.duplicate);
    assert_eq!(dup.received, 4);
    assert!(dup.complete);

    // accepted 位图 MSB-first：4 片全收 → 0b1111_0000
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11110000"
    );
    // 未 flush 前 persisted 必须为空（不变式 1）
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "00000000"
    );

    let flushed = flush_ok(session.id());
    assert_eq!(flushed.received, 4);
    assert!(flushed.complete);
    // 4 个连续片合并成一次写
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1);
    assert_eq!(stats.written_bytes, 14);
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11110000"
    );

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    assert!(!receiver::exists(session.id()));
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
    // finalize 成功即移除 receiver
    assert_eq!(
        receiver::finalize(session.id(), None, None).unwrap_err(),
        ReceiverError::NotFound
    );
}

#[test]
fn index_bounds_and_chunk_length_validation() {
    let temp = Temp::new();
    let session = Session::new();
    prealloc(temp.path(), 10);
    assert!(open_default(session.id(), temp.path(), 10, 4, 3));

    // index 越界
    assert_eq!(
        accept_ok_err(session.id(), 3, &[0u8; 4]),
        ReceiverError::Arg
    );
    // 非末片长度必须 == chunk_size
    assert_eq!(
        accept_ok_err(session.id(), 0, &[0u8; 3]),
        ReceiverError::Arg
    );
    assert_eq!(
        accept_ok_err(session.id(), 0, &[0u8; 5]),
        ReceiverError::Arg
    );
    // 末片长度必须 == total - index*chunk
    assert_eq!(
        accept_ok_err(session.id(), 2, &[0u8; 4]),
        ReceiverError::Arg
    );
    assert!(receiver::accept(session.id(), 2, &[0u8; 2], None).is_ok());
    // 空数据
    assert_eq!(accept_ok_err(session.id(), 0, &[]), ReceiverError::Arg);

    // open 参数防御校验
    let other = Session::new();
    assert!(receiver::open(other.id(), temp.path(), 0, 4, 1, None, 0).is_err());
    assert!(receiver::open(other.id(), temp.path(), 10, 0, 3, None, 0).is_err());
    assert!(receiver::open(other.id(), temp.path(), 10, 4, 2, None, 0).is_err());
    assert!(receiver::open("", temp.path(), 10, 4, 3, None, 0).is_err());
    // persisted 位图长度必须恰好 ceil(count/8)
    assert!(receiver::open(other.id(), temp.path(), 10, 4, 3, Some(&[0u8; 2]), 0).is_err());
    assert!(receiver::open(other.id(), temp.path(), 10, 4, 3, Some(&[0u8; 1]), 0).is_ok());
}

fn accept_ok_err(id: &str, index: u32, data: &[u8]) -> ReceiverError {
    receiver::accept(id, index, data, None).unwrap_err()
}

// ---------------------------------------------------------------------------
// pending 上限 / 相邻合并 / 写失败
// ---------------------------------------------------------------------------

#[test]
fn pending_cap_triggers_flush_and_adjacent_chunks_merge_into_one_write() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(16);
    let (chunk, total) = (4usize, 16u64);
    prealloc(temp.path(), total);
    // 注入 10B pending 上限：第 3 片（8+4>10）前触发 flush
    assert!(receiver::open_with_tuning(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        4,
        None,
        Duration::from_secs(3600),
        10,
        Duration::from_secs(3600),
    )
    .unwrap());

    assert!(!accept_ok(session.id(), 0, chunk_of(&data, 0, chunk)).snapshot_ready);
    assert!(!accept_ok(session.id(), 1, chunk_of(&data, 1, chunk)).snapshot_ready);
    // 第 3 片触发：先 flush 0,1（合并成一次写），再收 2
    let out = accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert_eq!(out.received, 3);
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1, "0,1 必须合并成一次写");
    assert_eq!(stats.written_bytes, 8);
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11000000"
    );

    assert!(accept_ok(session.id(), 3, chunk_of(&data, 3, chunk)).complete);
    assert_eq!(
        receiver::flush_stats(session.id()).unwrap().write_calls,
        1,
        "未超上限不应触发 flush"
    );
    let out = flush_ok(session.id());
    assert!(out.complete);
    // 2,3 合并成第二次写
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 2);
    assert_eq!(stats.written_bytes, 16);
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11110000"
    );
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
}

#[test]
fn flush_failure_keeps_pending_and_persisted_unset() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    // 故意不预分配：写盘必然失败
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        2
    ));
    assert!(!accept_ok(session.id(), 0, chunk_of(&data, 0, chunk)).duplicate);
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));

    assert_eq!(
        receiver::flush(session.id()).unwrap_err(),
        ReceiverError::Io
    );
    // 写失败：pending 保留、accepted 不变、persisted 不置位（不变式 4）
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "00000000"
    );
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1);
    assert_eq!(stats.written_bytes, 0);

    // 补上文件后重试：pending 还在，一次合并写成功
    prealloc(temp.path(), total);
    assert!(flush_ok(session.id()).complete);
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 2);
    assert_eq!(stats.written_bytes, 8);
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11000000"
    );

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
}

// ---------------------------------------------------------------------------
// abandon
// ---------------------------------------------------------------------------

#[test]
fn abandon_buffered_before_open_is_merged_and_beats_duplicate() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);

    // open 之前 abandon：进全局缓冲，open 时合并
    receiver::abandon(session.id(), 1, "old").unwrap();
    prealloc(temp.path(), total);
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        2
    ));

    // 旧 attempt 的热路径命中 → ABANDONED
    assert_eq!(
        accept_attempt(session.id(), 1, chunk_of(&data, 1, chunk), "old").unwrap_err(),
        ReceiverError::Abandoned
    );
    // 新 attempt 不受影响
    let out = accept_attempt(session.id(), 1, chunk_of(&data, 1, chunk), "new").unwrap();
    assert_eq!(out.received, 1);
    assert!(!out.duplicate);
    // 同一 attempt 重复（duplicate）
    assert!(
        accept_attempt(session.id(), 1, chunk_of(&data, 1, chunk), "new")
            .unwrap()
            .duplicate
    );

    // receiver 已存在时 abandon：记录进会话
    receiver::abandon(session.id(), 1, "new").unwrap();
    // abandoned 优先于 duplicate
    assert_eq!(
        accept_attempt(session.id(), 1, chunk_of(&data, 1, chunk), "new").unwrap_err(),
        ReceiverError::Abandoned
    );
    // 又一个新 attempt 仍可上传/幂等
    assert!(
        accept_attempt(session.id(), 1, chunk_of(&data, 1, chunk), "new2")
            .unwrap()
            .duplicate
    );
    assert!(
        accept_attempt(session.id(), 0, chunk_of(&data, 0, chunk), "new")
            .unwrap()
            .complete
    );
    // abandon 不可空 attempt / 未越界也不 panic
    assert_eq!(
        receiver::abandon(session.id(), 0, "").unwrap_err(),
        ReceiverError::Arg
    );
    assert!(receiver::abandon(session.id(), 99, "whatever").is_ok());
}

// ---------------------------------------------------------------------------
// TTL / open 幂等 / 崩溃恢复
// ---------------------------------------------------------------------------

#[test]
fn ttl_expiry_gc_removes_receiver() {
    let temp = Temp::new();
    let session = Session::new();
    prealloc(temp.path(), 4);
    assert!(receiver::open_with_tuning(
        session.id(),
        temp.path(),
        4,
        4,
        1,
        None,
        Duration::from_millis(300),
        64 << 20,
        Duration::from_secs(1),
    )
    .unwrap());
    assert!(receiver::exists(session.id()));
    receiver::collect_expired_now();
    assert!(receiver::exists(session.id()), "未到 TTL 不应回收");

    std::thread::sleep(Duration::from_millis(450));
    receiver::collect_expired_now();
    assert!(!receiver::exists(session.id()), "空闲超过 TTL 应被回收");

    // 回收后旧会话的调用返回 NOT_FOUND
    assert_eq!(
        receiver::accept(session.id(), 0, &[0u8; 4], None).unwrap_err(),
        ReceiverError::NotFound
    );
}

#[test]
fn open_is_idempotent_and_never_overwrites_existing_state() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    prealloc(temp.path(), 8);
    assert!(open_default(session.id(), temp.path(), 8, 4, 2));
    accept_ok(session.id(), 0, chunk_of(&data, 0, 4));

    // 同参重复 open：返回 false 且不用传入位图覆盖
    let created = receiver::open(session.id(), temp.path(), 8, 4, 2, Some(&[0xFF]), 0).unwrap();
    assert!(!created);
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "10000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "00000000"
    );
    assert!(accept_ok(session.id(), 0, chunk_of(&data, 0, 4)).duplicate);

    // 参数不一致：拒绝而不是覆盖
    assert!(receiver::open(session.id(), temp.path(), 12, 4, 3, None, 0).is_err());

    // 全新 id 用 persisted 位图 open：accepted+persisted 都恢复、received=popcount
    let other = Session::new();
    assert!(receiver::open(other.id(), temp.path(), 8, 4, 2, Some(&[0b1100_0000]), 0).unwrap());
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(other.id()).unwrap()),
        "11000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(other.id()).unwrap()),
        "11000000"
    );
    assert!(accept_ok(other.id(), 0, chunk_of(&data, 0, 4)).duplicate);
    assert!(accept_ok(other.id(), 1, chunk_of(&data, 1, 4)).complete);
}

#[test]
fn crash_recovery_restores_only_persisted_chunks() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(12);
    let (chunk, total) = (4usize, 12u64);
    prealloc(temp.path(), total);
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        3
    ));

    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    flush_ok(session.id());
    // 第 3 片只进内存 pending，不得进 persisted（不变式 2）
    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11100000"
    );
    let persisted_snapshot = receiver::bitmap_persisted(session.id()).unwrap();
    assert_eq!(bitmap(&persisted_snapshot), "11000000");

    // 模拟进程重启：丢掉 receiver，用 Redis persisted 位图恢复
    receiver::close(session.id()).unwrap();
    assert!(!receiver::exists(session.id()));
    assert!(receiver::open(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        3,
        Some(persisted_snapshot.as_slice()),
        0
    )
    .unwrap());
    // accepted 只含 persisted 片，内存里仅 accepted 的片不得被恢复
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11000000"
    );
    // 补传第 3 片 → received 从 2 变 3
    let out = accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert_eq!(out.received, 3);
    assert!(out.complete);
    flush_ok(session.id());

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
}

// ---------------------------------------------------------------------------
// finalize：坏块清理 / root 不匹配 / 缺 receiver
// ---------------------------------------------------------------------------

#[test]
fn finalize_leaf_mismatch_clears_bad_chunk_then_resend_succeeds() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(12);
    let (chunk, total) = (4usize, 12u64);
    prealloc(temp.path(), total);
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        3
    ));

    let mut corrupt = chunk_of(&data, 2, chunk).to_vec();
    corrupt[0] ^= 0xFF;
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    accept_ok(session.id(), 2, &corrupt);
    flush_ok(session.id());

    let leaves = leaves_of(&data, chunk);
    let flat = leaves_flat(&leaves);
    let root = merkle_root(&leaves);
    let err = receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap_err();
    assert_eq!(err, ReceiverError::LeafMismatch { bad_index: 2 });
    // 坏块位清零、received--、dirty=true，receiver 保留（不变式 5）
    assert!(receiver::exists(session.id()));
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11000000"
    );

    // 补传正确片 → finalize 成功
    let out = accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert_eq!(out.received, 3);
    assert!(out.complete);
    flush_ok(session.id());
    let hash = receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap();
    assert_eq!(hash, file_hash(&data));
    assert!(!receiver::exists(session.id()));
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
}

#[test]
fn finalize_root_or_size_mismatch_keeps_receiver() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(12);
    let (chunk, total) = (4usize, 12u64);

    // size mismatch：文件比 total 短，且不做补齐写
    prealloc(temp.path(), 8);
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        3
    ));
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    flush_ok(session.id());
    let leaves = leaves_of(&data, chunk);
    let flat = leaves_flat(&leaves);
    let root = merkle_root(&leaves);
    assert_eq!(
        receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap_err(),
        ReceiverError::SizeMismatch
    );
    assert!(receiver::exists(session.id()));

    // root mismatch：文件补齐 + 收齐，但树根故意写错
    prealloc(temp.path(), total);
    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    flush_ok(session.id());
    let mut wrong_root = root;
    wrong_root[0] ^= 0xFF;
    assert_eq!(
        receiver::finalize(session.id(), Some(&flat), Some(&wrong_root)).unwrap_err(),
        ReceiverError::RootMismatch
    );
    assert!(receiver::exists(session.id()));
    // 正确的树根 → 成功
    assert_eq!(
        receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap(),
        file_hash(&data)
    );
    assert!(!receiver::exists(session.id()));

    // 缺 receiver：finalize/flush/bitmap/snapshot_ack 全部 NOT_FOUND
    assert_eq!(
        receiver::finalize(session.id(), None, None).unwrap_err(),
        ReceiverError::NotFound
    );
    assert_eq!(
        receiver::flush(session.id()).unwrap_err(),
        ReceiverError::NotFound
    );
    assert_eq!(
        receiver::bitmap_accepted(session.id()).unwrap_err(),
        ReceiverError::NotFound
    );
    assert_eq!(
        receiver::snapshot_ack(session.id()).unwrap_err(),
        ReceiverError::NotFound
    );
}

// ---------------------------------------------------------------------------
// snapshot_ready 节流
// ---------------------------------------------------------------------------

#[test]
fn snapshot_ready_is_throttled_by_interval_and_ack_clears_dirty() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(12);
    let (chunk, total) = (4usize, 12u64);
    let interval = Duration::from_millis(100);
    prealloc(temp.path(), total);
    assert!(receiver::open_with_tuning(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        3,
        None,
        Duration::from_secs(3600),
        64 << 20,
        interval,
    )
    .unwrap());

    // accept 本身只改 accepted → 不置 dirty、不发信号
    assert!(!accept_ok(session.id(), 0, chunk_of(&data, 0, chunk)).snapshot_ready);
    // 首次 flush 产生 persisted 变化 → 立即发信号
    let out = flush_ok(session.id());
    assert!(out.snapshot_ready);
    receiver::snapshot_ack(session.id()).unwrap();

    // ack 后 dirty 清空：再 flush 同一批（无新写）也不发信号
    assert!(!flush_ok(session.id()).snapshot_ready);

    // 节流窗口内：新写产生了 dirty 但不发信号
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    assert!(!flush_ok(session.id()).snapshot_ready);
    // 等过窗口后信号恢复（dirty 仍未被 ack）
    std::thread::sleep(interval + Duration::from_millis(40));
    assert!(flush_ok(session.id()).snapshot_ready);
    // 刚发过信号：窗口内不再重复
    assert!(!flush_ok(session.id()).snapshot_ready);

    // ack 清 dirty 后彻底安静
    receiver::snapshot_ack(session.id()).unwrap();
    std::thread::sleep(interval + Duration::from_millis(40));
    assert!(!flush_ok(session.id()).snapshot_ready);
}

// ---------------------------------------------------------------------------
// C ABI 薄封装
// ---------------------------------------------------------------------------

#[test]
fn c_abi_accept_result_bitmap_and_error_codes() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(16);
    let (chunk, total) = (4usize, 16u64);
    prealloc(temp.path(), total);
    let id = cstr(session.id());
    let path = cstr(temp.path().to_str().unwrap());

    assert_eq!(fc_abi_version(), 5);
    // open：首次 1，重复 0，非法参数 FC_ERR_ARG
    assert_eq!(
        sys_core::fc_receiver_open(
            id.as_ptr(),
            path.as_ptr(),
            total,
            chunk as u64,
            4,
            std::ptr::null(),
            0,
            0
        ),
        1
    );
    assert_eq!(
        sys_core::fc_receiver_open(
            id.as_ptr(),
            path.as_ptr(),
            total,
            chunk as u64,
            4,
            std::ptr::null(),
            0,
            0
        ),
        0
    );
    assert_eq!(
        sys_core::fc_receiver_open(
            id.as_ptr(),
            std::ptr::null(),
            total,
            chunk as u64,
            4,
            std::ptr::null(),
            0,
            0
        ),
        FC_ERR_ARG
    );
    assert_eq!(sys_core::fc_receiver_exists(id.as_ptr()), 1);
    assert_eq!(sys_core::fc_receiver_exists(std::ptr::null()), 0);

    // accept 填充 fc_accept_result（乱序 + 重复）
    for (n, index) in [3u32, 0].iter().enumerate() {
        let buf = chunk_of(&data, *index, chunk);
        let mut out = FcAcceptResult::default();
        assert_eq!(
            sys_core::fc_receiver_accept(
                id.as_ptr(),
                *index,
                buf.as_ptr(),
                buf.len(),
                std::ptr::null(),
                &mut out,
            ),
            FC_OK
        );
        assert_eq!(out.received, n as i64 + 1);
        assert_eq!(out.duplicate, 0);
        assert_eq!(out.reserved, 0);
        assert_eq!(out.complete, 0);
    }
    let buf = chunk_of(&data, 0, chunk);
    let mut dup = FcAcceptResult::default();
    assert_eq!(
        sys_core::fc_receiver_accept(
            id.as_ptr(),
            0,
            buf.as_ptr(),
            buf.len(),
            std::ptr::null(),
            &mut dup,
        ),
        FC_OK
    );
    assert_eq!(dup.duplicate, 1);
    assert_eq!(dup.received, 2);
    assert_eq!(dup.complete, 0);

    // out 不可为 NULL
    assert_eq!(
        sys_core::fc_receiver_accept(
            id.as_ptr(),
            0,
            buf.as_ptr(),
            buf.len(),
            std::ptr::null(),
            std::ptr::null_mut(),
        ),
        FC_ERR_ARG
    );
    // 未知 receiver → NOT_FOUND
    let unknown = cstr("rrust-definitely-missing");
    let mut out = FcAcceptResult::default();
    assert_eq!(
        sys_core::fc_receiver_accept(
            unknown.as_ptr(),
            0,
            buf.as_ptr(),
            buf.len(),
            std::ptr::null(),
            &mut out,
        ),
        FC_ERR_NOT_FOUND
    );
    assert_eq!(
        sys_core::fc_receiver_flush(unknown.as_ptr(), std::ptr::null_mut()),
        FC_ERR_NOT_FOUND
    );
    assert_eq!(
        sys_core::fc_receiver_snapshot_ack(unknown.as_ptr()),
        FC_ERR_NOT_FOUND
    );
    assert_eq!(
        sys_core::fc_receiver_bitmap_accepted(unknown.as_ptr(), std::ptr::null_mut(), 0),
        FC_ERR_NOT_FOUND as i64
    );
    // attempt 空 → ARG
    assert_eq!(
        sys_core::fc_receiver_abandon(id.as_ptr(), 0, std::ptr::null()),
        FC_ERR_ARG
    );

    // 位图导出（MSB-first：收 0 和 3 → 1001_0000）
    assert_eq!(
        sys_core::fc_receiver_flush(id.as_ptr(), std::ptr::null_mut()),
        FC_OK
    );
    let mut bits = [0u8; 1];
    assert_eq!(
        sys_core::fc_receiver_bitmap_accepted(id.as_ptr(), bits.as_mut_ptr(), bits.len()),
        1
    );
    assert_eq!(bits, [0b1001_0000]);
    let mut persisted = [0u8; 1];
    assert_eq!(
        sys_core::fc_receiver_bitmap_persisted(
            id.as_ptr(),
            persisted.as_mut_ptr(),
            persisted.len()
        ),
        1
    );
    assert_eq!(persisted, [0b1001_0000]);
    // cap 不足 → ARG
    assert_eq!(
        sys_core::fc_receiver_bitmap_accepted(id.as_ptr(), bits.as_mut_ptr(), 0),
        FC_ERR_ARG as i64
    );

    // close：幂等
    assert_eq!(sys_core::fc_receiver_close(id.as_ptr()), FC_OK);
    assert_eq!(sys_core::fc_receiver_close(id.as_ptr()), FC_OK);
    assert_eq!(sys_core::fc_receiver_exists(id.as_ptr()), 0);
    assert_eq!(sys_core::fc_receiver_close(std::ptr::null()), FC_ERR_ARG);
}

#[test]
fn c_abi_finalize_reports_hash_and_bad_index() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    prealloc(temp.path(), total);
    let id = cstr(session.id());
    let path = cstr(temp.path().to_str().unwrap());
    assert_eq!(
        sys_core::fc_receiver_open(
            id.as_ptr(),
            path.as_ptr(),
            total,
            chunk as u64,
            2,
            std::ptr::null(),
            0,
            0
        ),
        1
    );
    // 第 2 片故意写坏
    let mut corrupt = chunk_of(&data, 1, chunk).to_vec();
    corrupt[0] ^= 0xFF;
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, &corrupt);
    flush_ok(session.id());

    let leaves = leaves_of(&data, chunk);
    let flat = leaves_flat(&leaves);
    let root = merkle_root(&leaves);
    let mut hash = [0u8; 32];
    let mut bad: i64 = -2;
    assert_eq!(
        sys_core::fc_receiver_finalize(
            id.as_ptr(),
            flat.as_ptr(),
            leaves.len(),
            root.as_ptr(),
            hash.as_mut_ptr(),
            &mut bad,
        ),
        FC_ERR_LEAF_MISMATCH
    );
    assert_eq!(bad, 1);

    // 补传正确片后 FFI finalize 成功：哈希正确、bad_index 重置 -1
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    flush_ok(session.id());
    let mut hash = [0u8; 32];
    let mut bad: i64 = -2;
    assert_eq!(
        sys_core::fc_receiver_finalize(
            id.as_ptr(),
            flat.as_ptr(),
            leaves.len(),
            root.as_ptr(),
            hash.as_mut_ptr(),
            &mut bad,
        ),
        FC_OK
    );
    assert_eq!(hash, file_hash(&data));
    assert_eq!(bad, -1);
    assert_eq!(sys_core::fc_receiver_exists(id.as_ptr()), 0);
}
