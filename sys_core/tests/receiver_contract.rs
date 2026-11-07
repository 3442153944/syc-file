//! 服务端分片接收器契约测试（契约 §8 Rust 全部用例）。
//!
//! 每个用例使用唯一 upload_id，结束 close（`Session` 的 Drop 兜底），避免全局注册表
//! 状态互相干扰。临时文件用 `Temp` 管理，Drop 时先 `fc_evict` 再删除。
use std::ffi::CString;
use std::path::{Path, PathBuf};
use std::time::Duration;

use sys_core::file_core::receiver::{
    self, last_finalize_stats, AcceptOutcome, ReceiverError, Tuning,
};
use sys_core::{fc_evict, fc_merkle_root, fc_preallocate, FC_OK};

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

    // write-through：accepted 与 persisted 同步置位（MSB-first：4 片全收 → 0b1111_0000）
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11110000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11110000"
    );

    // flush 只推进增量 hasher（顺序上传时已推进完），没有批量 IO
    let flushed = flush_ok(session.id());
    assert_eq!(flushed.received, 4);
    assert!(flushed.complete);
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 4, "每片一次定位写");
    assert_eq!(stats.written_bytes, 14);
    assert_eq!(stats.max_write_bytes, 4);
    assert_eq!(stats.hashed_bytes, 14);
    assert_eq!(stats.readback_bytes, 6, "乱序补齐回读 chunk1/chunk3");
    assert_eq!(stats.persisted, 4);
    assert_eq!(stats.leaf_digests_ready, 4);
    assert!(stats.incremental);

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    // 快路径统计：增量、零整文件读；结果与 describe / fc_finalize 对拍
    let stats = last_finalize_stats(session.id()).unwrap();
    assert!(stats.incremental);
    assert_eq!(stats.file_read_bytes, 0);
    assert_eq!(stats.hashed_bytes, 14);
    assert!(!stats.fallback_after);
    let described = sys_core::file::describe(temp.path(), chunk).unwrap();
    assert_eq!(described.file_hash_hex, hex::encode(hash));
    let c_path = cstr(temp.path().to_str().unwrap());
    let (mut final_hash, mut bad) = ([0u8; 32], -2i64);
    assert_eq!(
        sys_core::fc_finalize(
            c_path.as_ptr(),
            chunk as u64,
            total,
            std::ptr::null(),
            0,
            std::ptr::null(),
            final_hash.as_mut_ptr(),
            &mut bad,
        ),
        FC_OK
    );
    assert_eq!(final_hash, hash);
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
// write-through 直写 / 重复 / 写失败
// ---------------------------------------------------------------------------
#[test]
fn write_through_writes_each_chunk_immediately_and_duplicate_no_rewrite() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(16);
    let (chunk, total) = (4usize, 16u64);
    prealloc(temp.path(), total);
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        4
    ));

    let expected_persisted = ["10000000", "11000000", "11100000", "11110000"];
    // 逐片 write-through：accept 返回后文件对应 offset 就是该片内容（无内存池）
    for (n, index) in (0u32..4).enumerate() {
        let out = accept_ok(session.id(), index, chunk_of(&data, index, chunk));
        assert_eq!(out.received, n as i64 + 1);
        assert!(!out.duplicate);
        assert_eq!(out.complete, n == 3);
        let on_disk = std::fs::read(temp.path()).unwrap();
        let start = index as usize * chunk;
        assert_eq!(
            &on_disk[start..start + chunk],
            chunk_of(&data, index, chunk),
            "accept 后数据必须已在盘上"
        );
        assert_eq!(
            bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
            expected_persisted[n]
        );
        assert_eq!(
            bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
            expected_persisted[n]
        );
    }
    // 每片一次物理写，最大单写等于单片；顺序上传无回读
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 4);
    assert_eq!(stats.written_bytes, 16);
    assert_eq!(stats.max_write_bytes, 4);
    assert_eq!(stats.readback_bytes, 0);
    assert_eq!(stats.hashed_bytes, 16);
    assert_eq!(stats.accepted, 4);
    assert_eq!(stats.persisted, 4);

    // 重复片不重复写、不重复计数
    let dup = accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert!(dup.duplicate);
    assert_eq!(dup.received, 4);
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 4, "重复片不应重复写盘");
    assert_eq!(stats.written_bytes, 16);

    // complete 前的 flush 无批量 IO：只确认 hasher 已推进
    let out = flush_ok(session.id());
    assert!(out.complete);
    assert_eq!(receiver::flush_stats(session.id()).unwrap().write_calls, 4);
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    assert!(last_finalize_stats(session.id()).unwrap().incremental);
}

#[test]
fn write_failure_on_accept_leaves_bits_unset_then_retry_succeeds() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(8);
    let (chunk, total) = (4usize, 8u64);
    // 故意不预分配：write-through 首次写盘失败
    assert!(open_default(
        session.id(),
        temp.path(),
        total,
        chunk as u64,
        2
    ));

    assert_eq!(
        receiver::accept(session.id(), 0, chunk_of(&data, 0, chunk), None).unwrap_err(),
        ReceiverError::Io
    );
    // 写失败：不置位、不计数（write-through 严格“写成功才算收到”）
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "00000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "00000000"
    );
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 0);
    assert_eq!(stats.written_bytes, 0);
    assert_eq!(stats.hashed_bytes, 0);
    assert_eq!(stats.accepted, 0);
    assert_eq!(stats.persisted, 0);

    // 补上文件后重试本片：成功写盘并置位
    prealloc(temp.path(), total);
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.write_calls, 1);
    assert_eq!(stats.written_bytes, 4);
    assert_eq!(stats.hashed_bytes, 4);
    assert_eq!(stats.accepted, 1);
    assert_eq!(stats.persisted, 1);
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "10000000"
    );
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
        Tuning {
            ttl: Duration::from_millis(300),
            ..Tuning::default()
        },
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
        "10000000"
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
    // persisted 恢复的 receiver 没有增量状态，finalize 走全量路径
    assert!(!receiver::flush_stats(other.id()).unwrap().incremental);
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
    // write-through：收到即落盘，persisted 快照包含 0,1（Go 侧低频快照可能滞后）
    let persisted_snapshot = receiver::bitmap_persisted(session.id()).unwrap();
    assert_eq!(bitmap(&persisted_snapshot), "11000000");
    let before = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(before.persisted, 2);
    assert_eq!(before.hashed_bytes, 8);

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
    // 只恢复 persisted 片；内存里没有其它状态
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11000000"
    );
    assert_eq!(
        bitmap(&receiver::bitmap_persisted(session.id()).unwrap()),
        "11000000"
    );
    // 有 persisted 位图 = 崩溃恢复，没有增量状态 → finalize 必须走全量路径
    assert!(!receiver::flush_stats(session.id()).unwrap().incremental);
    // 补传第 3 片（write-through 直接写盘）→ received 从 2 变 3
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
    // 崩溃恢复走全量 fc_finalize（唯一会整文件重读的 complete 场景）
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(!fin.incremental);
    assert_eq!(fin.file_read_bytes, 12);
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
    // 首次 finalize 走增量快路径（O(片数) 比对）；叶子失配后 hasher 无法回滚 → 回退全量
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert!(fin.fallback_after);
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
    assert!(!receiver::flush_stats(session.id()).unwrap().incremental);

    // 补传正确片 → finalize 成功
    let out = accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    assert_eq!(out.received, 3);
    assert!(out.complete);
    flush_ok(session.id());
    let hash = receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap();
    assert_eq!(hash, file_hash(&data));
    // 补传后的 finalize 已回退全量路径（有整文件重读），语义与改造前一致
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(!fin.incremental);
    assert_eq!(fin.file_read_bytes, 12);
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
    // 增量状态未就绪（缺片）时走全量；size mismatch 在 fc_finalize 读盘前返回
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(!fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);

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
    // 收齐后走增量快路径；root 不匹配不回退（hasher 仍有效，重试仍可快路径）
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert!(!fin.fallback_after);
    // 正确的树根 → 成功
    assert_eq!(
        receiver::finalize(session.id(), Some(&flat), Some(&root)).unwrap(),
        file_hash(&data)
    );
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
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
        Tuning {
            snapshot_interval: interval,
            ..Tuning::default()
        },
    )
    .unwrap());

    // write-through 每次写盘都置 dirty：首次 accept 立即发快照信号
    assert!(accept_ok(session.id(), 0, chunk_of(&data, 0, chunk)).snapshot_ready);
    receiver::snapshot_ack(session.id()).unwrap();

    // ack 后 dirty 清空：没有新写时 flush 不发信号
    assert!(!flush_ok(session.id()).snapshot_ready);

    // 节流窗口内：新写产生 dirty 但不发信号
    assert!(!accept_ok(session.id(), 1, chunk_of(&data, 1, chunk)).snapshot_ready);
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
