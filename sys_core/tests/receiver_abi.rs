//! 服务端分片接收器的 C ABI 测试（从 receiver_contract.rs 拆出以控制单文件规模）。
//!
//! 覆盖：fc_accept_result 填充、位图导出 MSB-first、错误码映射、finalize 坏块/成功路径。
use std::ffi::CString;
use std::path::{Path, PathBuf};

use sys_core::file_core::receiver;
use sys_core::{
    fc_abi_version, fc_evict, fc_merkle_root, fc_preallocate, FcAcceptResult, FC_ERR_ARG,
    FC_ERR_LEAF_MISMATCH, FC_ERR_NOT_FOUND, FC_OK,
};

struct Temp(PathBuf);

impl Temp {
    fn new() -> Self {
        Self(std::env::temp_dir().join(format!("sys-core-abi-{}", uuid::Uuid::new_v4())))
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

fn accept_ok(id: &str, index: u32, data: &[u8]) {
    receiver::accept(id, index, data, None).unwrap();
}

fn flush_ok(id: &str) {
    receiver::flush(id).unwrap();
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
