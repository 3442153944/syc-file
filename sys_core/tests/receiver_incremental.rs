//! 增量校验专项测试（方向 A：消除 complete 的 finalize 全核哈希爆发）。
//!
//! 从 `receiver_contract.rs` 拆出以控制单文件规模；覆盖：
//! - 乱序 accept + 乱序 flush 组 → 增量整文件 hash 仍正确，并与 blake3/describe/fc_finalize 对拍；
//! - 正常顺序下跨 64MiB pending 上限的多组 flush，complete 走增量快路径；
//! - `hash_wait` 暂存超限 → 回退全量 finalize，结果仍正确；
//! - 统计计数器断言快路径确实没有整文件重读/重算。
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
        Self(std::env::temp_dir().join(format!("sys-core-incr-{}", uuid::Uuid::new_v4())))
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

// ---------------------------------------------------------------------------
// 乱序 accept + 乱序 flush 组
// ---------------------------------------------------------------------------

#[test]
fn out_of_order_flush_groups_keep_incremental_hash_correct() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(14); // 末片 2B，覆盖非整除边界
    let (chunk, total) = (4usize, 14u64);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, 4);

    // 先落盘末片：偏移 12 > hashed 0 → 进 hash_wait 暂存
    accept_ok(session.id(), 3, chunk_of(&data, 3, chunk));
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.hashed_bytes, 0);
    assert_eq!(stats.hash_wait_bytes, 2);
    assert!(stats.incremental);

    // 再落盘 0,1：hashed 0→8，末片仍在前方等 8..12 的洞
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.hashed_bytes, 8);
    assert_eq!(stats.hash_wait_bytes, 2);

    // 补上 2：hashed 8→12 后按序 drain 末片 → 整文件喂完
    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.hashed_bytes, 14);
    assert_eq!(stats.hash_wait_bytes, 0);
    assert_eq!(stats.leaf_digests_ready, 4);
    assert_eq!(
        bitmap(&receiver::bitmap_accepted(session.id()).unwrap()),
        "11110000"
    );

    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    // 与独立计算的 blake3 / describe / fc_finalize 三方对拍
    assert_eq!(hash, file_hash(&data));
    assert_eq!(
        sys_core::file::describe(temp.path(), chunk)
            .unwrap()
            .file_hash_hex,
        hex::encode(hash)
    );
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
    // 快路径统计：整文件已增量喂完，complete 零读盘
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert_eq!(fin.hashed_bytes, 14);
}

// ---------------------------------------------------------------------------
// 跨 64MiB pending 上限的多组 flush
// ---------------------------------------------------------------------------

/// 确定性分片内容（不保留整文件，测试侧只维护顺序 blake3 作对照）。
fn generated_chunk(index: usize, len: usize) -> Vec<u8> {
    (0..len).map(|i| ((index * 131 + i) % 251) as u8).collect()
}

#[test]
fn multi_group_over_64mib_uses_incremental_fast_path() {
    let temp = Temp::new();
    let session = Session::new();
    let chunk = 4 << 20; // 4 MiB
    let total = 66u64 << 20; // 66 MiB，必然跨过 64MiB pending 上限触发多组 flush
    let count = total.div_ceil(chunk as u64);
    assert_eq!(count, 17); // 16×4 + 1×2
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, count);

    let mut reference = blake3::Hasher::new();
    for index in 0..count {
        let len = (total - index * chunk as u64).min(chunk as u64) as usize;
        let data = generated_chunk(index as usize, len);
        reference.update(&data);
        accept_ok(session.id(), index as u32, &data);
    }
    // 第 16 片触发过 pending 上限 flush；最后一片仍需显式 flush
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert!(stats.incremental);
    assert_eq!(stats.hashed_bytes, total);
    assert_eq!(stats.hash_wait_bytes, 0);
    assert_eq!(stats.write_calls, 2, "64MiB 一组 + 剩余一组");
    assert_eq!(stats.leaf_digests_ready, count as usize);

    let before = finalize_counters();
    let hash = receiver::finalize(session.id(), None, None).unwrap();
    assert_eq!(hash, *reference.finalize().as_bytes());
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(fin.incremental);
    assert_eq!(fin.file_read_bytes, 0);
    assert_eq!(fin.hashed_bytes, total);
    // 进程级计数器：快路径 +1（其它测试并发只会增加，不会减少）
    assert!(finalize_counters().0 > before.0);
    assert!(!receiver::exists(session.id()));
}

// ---------------------------------------------------------------------------
// hash_wait 超限回退
// ---------------------------------------------------------------------------

#[test]
fn hash_wait_overflow_falls_back_to_full_finalize() {
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
            hash_wait_cap: 2, // 注入 2B 暂存上限：乱序首组 4B 必然超限
            ..Tuning::default()
        },
    )
    .unwrap());

    // 先写末片：偏移 4 > hashed 0，暂存 4B > cap 2B → 回退全量
    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert!(!stats.incremental, "hash_wait 超限必须回退");
    assert_eq!(stats.hashed_bytes, 0);
    assert_eq!(stats.hash_wait_bytes, 0);

    // 回退后继续收片/落盘不受影响，finalize 走全量且结果正确
    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    flush_ok(session.id());
    let leaves = leaves_of(&data, chunk);
    let hash = receiver::finalize(
        session.id(),
        Some(&leaves_flat(&leaves)),
        Some(&merkle_root(&leaves)),
    )
    .unwrap();
    assert_eq!(hash, file_hash(&data));
    let fin = last_finalize_stats(session.id()).unwrap();
    assert!(!fin.incremental, "回退后的 finalize 走全量路径");
    assert_eq!(fin.file_read_bytes, 8);
    assert!(fin.fallback_after);
    assert_eq!(std::fs::read(temp.path()).unwrap(), data);
}

// ---------------------------------------------------------------------------
// 顺序收片时 accept 只做叶子、flush 才喂 hasher
// ---------------------------------------------------------------------------

#[test]
fn accept_computes_leaf_digests_and_flush_feeds_hasher_incrementally() {
    let temp = Temp::new();
    let session = Session::new();
    let data = pattern(12);
    let (chunk, total) = (4usize, 12u64);
    prealloc(temp.path(), total);
    open_default(session.id(), temp.path(), total, chunk as u64, 3);

    accept_ok(session.id(), 0, chunk_of(&data, 0, chunk));
    // accept 之后：叶子已算，但 hasher 未喂（只有落盘成功才喂）
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.leaf_digests_ready, 1);
    assert_eq!(stats.hashed_bytes, 0);
    // 重复 accept 不重复计算/改变状态
    assert!(
        receiver::accept(session.id(), 0, chunk_of(&data, 0, chunk), None)
            .unwrap()
            .duplicate
    );
    assert_eq!(
        receiver::flush_stats(session.id())
            .unwrap()
            .leaf_digests_ready,
        1
    );

    flush_ok(session.id());
    assert_eq!(receiver::flush_stats(session.id()).unwrap().hashed_bytes, 4);

    accept_ok(session.id(), 1, chunk_of(&data, 1, chunk));
    accept_ok(session.id(), 2, chunk_of(&data, 2, chunk));
    flush_ok(session.id());
    let stats = receiver::flush_stats(session.id()).unwrap();
    assert_eq!(stats.hashed_bytes, 12);
    assert_eq!(stats.leaf_digests_ready, 3);

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
