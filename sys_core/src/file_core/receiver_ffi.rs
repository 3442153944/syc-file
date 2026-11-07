//! 服务端分片接收器的薄 C ABI 包装（契约 §2）。
//!
//! 只做 CStr/CString 转换、参数校验、`catch_unwind` 与结构体填充；
//! 全部语义在 `receiver.rs`（typed API，供 Rust 测试直接调用）。
//!
//! 所有入口 panic 统一兜成 `FC_ERR_IO`（依赖 profile `panic = "unwind"`）。
#![allow(clippy::not_unsafe_ptr_arg_deref)]

use std::ffi::CStr;
use std::os::raw::c_char;
use std::path::Path;
use std::slice;

use super::file_ops::{ffi_guard, FC_ERR_ARG, FC_ERR_LEAF_MISMATCH, FC_OK};
use super::receiver::{self, AcceptOutcome, ReceiverError};

/// blake3 哈希字节数（与 file_ops / receiver 保持一致）。
const HASH_SIZE: usize = 32;

/// C ABI `fc_accept_result`（`#[repr(C)]`，字段顺序固定，见 `sys_core.h`）。
#[repr(C)]
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct FcAcceptResult {
    /// 已接受分片数（accepted 计数）。
    pub received: i64,
    /// 1 = accepted 已覆盖全部分片。
    pub complete: i32,
    /// 1 = dirty 且距上次信号 ≥ 快照间隔。
    pub snapshot_ready: i32,
    /// 1 = 该片此前已接受（幂等重复）。
    pub duplicate: i32,
    /// 恒 0。
    pub reserved: i32,
}

impl FcAcceptResult {
    fn from_outcome(outcome: AcceptOutcome) -> Self {
        Self {
            received: outcome.received,
            complete: outcome.complete as i32,
            snapshot_ready: outcome.snapshot_ready as i32,
            duplicate: outcome.duplicate as i32,
            reserved: 0,
        }
    }
}

/// C 字符串 → &str（要求 UTF-8；NULL 或非法 UTF-8 返回 None）。
unsafe fn cstr<'a>(p: *const c_char) -> Option<&'a str> {
    if p.is_null() {
        return None;
    }
    CStr::from_ptr(p).to_str().ok()
}

/// 可空 C 字符串：NULL → None；非 NULL 但非 UTF-8 → FC_ERR_ARG。
fn optional_cstr<'a>(p: *const c_char) -> Result<Option<&'a str>, i32> {
    if p.is_null() {
        return Ok(None);
    }
    match unsafe { CStr::from_ptr(p) }.to_str() {
        Ok(s) => Ok(Some(s)),
        Err(_) => Err(FC_ERR_ARG),
    }
}

/// C 的 (ptr,len) → 切片。`len == 0` 时允许 NULL（Go 侧空切片传 nil+0）。
fn bytes<'a>(p: *const u8, len: usize) -> Result<&'a [u8], i32> {
    if len == 0 {
        return Ok(&[]);
    }
    if p.is_null() || len > isize::MAX as usize {
        return Err(FC_ERR_ARG);
    }
    Ok(unsafe { slice::from_raw_parts(p, len) })
}

/// 可空哈希数组：count==0 → None；否则要求非 NULL 且 count*32 不溢出。
fn hashes<'a>(p: *const u8, count: usize) -> Result<Option<&'a [u8]>, i32> {
    if count == 0 {
        return Ok(None);
    }
    let len = match count.checked_mul(HASH_SIZE) {
        Some(n) if n <= isize::MAX as usize => n,
        _ => return Err(FC_ERR_ARG),
    };
    if p.is_null() {
        return Err(FC_ERR_ARG);
    }
    Ok(Some(unsafe { slice::from_raw_parts(p, len) }))
}

fn write_out(out: *mut FcAcceptResult, outcome: AcceptOutcome) {
    if !out.is_null() {
        // out 由调用方分配（Go 栈/堆），用 ptr::write 覆盖，不要求已初始化
        unsafe { std::ptr::write(out, FcAcceptResult::from_outcome(outcome)) };
    }
}

// ---------------------------------------------------------------------------
// open / exists
// ---------------------------------------------------------------------------

/// 打开/注册会话接收器。返回 1 = 新建；0 = 已存在（参数一致，未覆盖）；负 = FC_ERR_*。
#[no_mangle]
pub extern "C" fn fc_receiver_open(
    upload_id: *const c_char,
    path: *const c_char,
    total_size: u64,
    chunk_size: u64,
    chunk_count: u64,
    persisted_bitmap: *const u8,
    bitmap_len: usize,
    ttl_secs: u64,
) -> i32 {
    ffi_guard(|| {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        let path = match unsafe { cstr(path) } {
            Some(s) if !s.is_empty() => s,
            _ => return FC_ERR_ARG,
        };
        let persisted = match bytes(persisted_bitmap, bitmap_len) {
            Ok(b) if b.is_empty() => None,
            Ok(b) => Some(b),
            Err(code) => return code,
        };
        match receiver::open(
            id,
            Path::new(path),
            total_size,
            chunk_size,
            chunk_count,
            persisted,
            ttl_secs,
        ) {
            Ok(true) => 1,
            Ok(false) => 0,
            Err(e) => e.code(),
        }
    })
}

/// 1 = 存在；0 = 不存在（或参数非法）。
#[no_mangle]
pub extern "C" fn fc_receiver_exists(upload_id: *const c_char) -> i32 {
    ffi_guard(|| match unsafe { cstr(upload_id) } {
        Some(id) => i32::from(receiver::exists(id)),
        None => 0,
    })
}

// ---------------------------------------------------------------------------
// accept / flush
// ---------------------------------------------------------------------------

/// 接收一个分片（write-through：直接把调用方数据定位写到临时文件 offset，不拷贝/不缓存；
/// 调用方保留所有权）。写成功才置 accepted+persisted 并推进增量 hasher。
#[no_mangle]
pub extern "C" fn fc_receiver_accept(
    upload_id: *const c_char,
    index: u32,
    data: *const u8,
    len: usize,
    attempt_id: *const c_char,
    out: *mut FcAcceptResult,
) -> i32 {
    ffi_guard(|| {
        if out.is_null() {
            return FC_ERR_ARG;
        }
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        let buf = match bytes(data, len) {
            Ok(b) => b,
            Err(code) => return code,
        };
        let attempt = match optional_cstr(attempt_id) {
            Ok(a) => a,
            Err(code) => return code,
        };
        match receiver::accept(id, index, buf, attempt) {
            Ok(outcome) => {
                write_out(out, outcome);
                FC_OK
            }
            Err(e) => e.code(),
        }
    })
}

/// write-through 没有 pending 批量 IO：只把增量 hasher 推进到已落盘的连续前缀并刷新活动时间。
/// out 可为 NULL。
#[no_mangle]
pub extern "C" fn fc_receiver_flush(upload_id: *const c_char, out: *mut FcAcceptResult) -> i32 {
    ffi_guard(|| {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        match receiver::flush(id) {
            Ok(outcome) => {
                write_out(out, outcome);
                FC_OK
            }
            Err(e) => e.code(),
        }
    })
}

// ---------------------------------------------------------------------------
// 位图导出 / 快照 ack
// ---------------------------------------------------------------------------

/// 导出 accepted 位图（MSB-first）。返回写入字节数；不存在返回 FC_ERR_NOT_FOUND。
#[no_mangle]
pub extern "C" fn fc_receiver_bitmap_accepted(
    upload_id: *const c_char,
    out: *mut u8,
    cap: usize,
) -> i64 {
    ffi_guard(|| -> i64 {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG as i64,
        };
        let bits = match receiver::bitmap_accepted(id) {
            Ok(bits) => bits,
            Err(e) => return e.code() as i64,
        };
        if out.is_null() || cap < bits.len() || bits.len() > isize::MAX as usize {
            return FC_ERR_ARG as i64;
        }
        unsafe { slice::from_raw_parts_mut(out, bits.len()).copy_from_slice(&bits) };
        bits.len() as i64
    })
}

/// 导出 persisted 位图（MSB-first）。返回写入字节数；不存在返回 FC_ERR_NOT_FOUND。
#[no_mangle]
pub extern "C" fn fc_receiver_bitmap_persisted(
    upload_id: *const c_char,
    out: *mut u8,
    cap: usize,
) -> i64 {
    ffi_guard(|| -> i64 {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG as i64,
        };
        let bits = match receiver::bitmap_persisted(id) {
            Ok(bits) => bits,
            Err(e) => return e.code() as i64,
        };
        if out.is_null() || cap < bits.len() || bits.len() > isize::MAX as usize {
            return FC_ERR_ARG as i64;
        }
        unsafe { slice::from_raw_parts_mut(out, bits.len()).copy_from_slice(&bits) };
        bits.len() as i64
    })
}

/// Go 成功 PersistBitmap 后调用：清 dirty。不存在返回 FC_ERR_NOT_FOUND。
#[no_mangle]
pub extern "C" fn fc_receiver_snapshot_ack(upload_id: *const c_char) -> i32 {
    ffi_guard(|| {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        match receiver::snapshot_ack(id) {
            Ok(()) => FC_OK,
            Err(e) => e.code(),
        }
    })
}

// ---------------------------------------------------------------------------
// finalize
// ---------------------------------------------------------------------------

/// 校验并移除 receiver（write-through 下没有待刷数据）。
/// 正常路径为增量校验的 O(片数) 比对（不重读整文件）；重启/快传恢复、chunk 数过大、
/// 叶子失配后等异常回退全量 fc_finalize（语义不变）。
/// FC_OK 时 receiver 已移除；LEAF_MISMATCH 时写 `*out_bad_index`、清该位、保留 receiver。
#[no_mangle]
pub extern "C" fn fc_receiver_finalize(
    upload_id: *const c_char,
    expected_leaves: *const u8,
    leaf_count: usize,
    expected_root: *const u8,
    out_file_hash32: *mut u8,
    out_bad_index: *mut i64,
) -> i32 {
    ffi_guard(|| {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        if out_file_hash32.is_null() {
            return FC_ERR_ARG;
        }
        if !out_bad_index.is_null() {
            unsafe { *out_bad_index = -1 };
        }
        let leaves = match hashes(expected_leaves, leaf_count) {
            Ok(v) => v,
            Err(code) => return code,
        };
        let root = if expected_root.is_null() {
            None
        } else {
            Some(unsafe { slice::from_raw_parts(expected_root, HASH_SIZE) })
        };
        match receiver::finalize(id, leaves, root) {
            Ok(hash) => {
                unsafe {
                    slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(&hash);
                }
                FC_OK
            }
            Err(ReceiverError::LeafMismatch { bad_index }) => {
                if !out_bad_index.is_null() {
                    unsafe { *out_bad_index = bad_index };
                }
                FC_ERR_LEAF_MISMATCH
            }
            Err(e) => e.code(),
        }
    })
}

// ---------------------------------------------------------------------------
// abandon / close
// ---------------------------------------------------------------------------

/// 记录放弃的 (index, attempt_id)；receiver 不存在时进全局缓冲，open 时合并。幂等。
#[no_mangle]
pub extern "C" fn fc_receiver_abandon(
    upload_id: *const c_char,
    index: u32,
    attempt_id: *const c_char,
) -> i32 {
    ffi_guard(|| {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        let attempt = match optional_cstr(attempt_id) {
            Ok(Some(s)) if !s.is_empty() => s,
            _ => return FC_ERR_ARG,
        };
        match receiver::abandon(id, index, attempt) {
            Ok(()) => FC_OK,
            Err(e) => e.code(),
        }
    })
}

/// 移除 receiver（不 flush、不写盘；幂等，不存在返回 FC_OK）。
#[no_mangle]
pub extern "C" fn fc_receiver_close(upload_id: *const c_char) -> i32 {
    ffi_guard(|| {
        let id = match unsafe { cstr(upload_id) } {
            Some(s) => s,
            None => return FC_ERR_ARG,
        };
        match receiver::close(id) {
            Ok(()) => FC_OK,
            Err(e) => e.code(),
        }
    })
}
