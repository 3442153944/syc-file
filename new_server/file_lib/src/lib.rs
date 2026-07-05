//! filecore：文件同步上传的 Rust 核心库（C ABI，无状态）。
//!
//! 设计要点：
//! - 无状态：不持有长生命周期会话句柄，所有会话/已收分片状态由 Go + Redis 管理，
//!   因此天然抗服务重启、无句柄泄漏、并发安全。
//! - 哈希统一用 blake3：分片叶子哈希、Merkle 树根、整文件哈希都用它，
//!   且与桌面端（Tauri，同为 Rust）可共用同一实现，保证两端逐字节一致。
//! - 乱序写：每个分片写入互不相交的 `[offset, offset+len)` 区域，每次调用各自持有
//!   独立文件句柄并 seek 到偏移写入，磁盘的定位写对不相交区域并发安全。
//!
//! 返回码见下方常量；负数为错误，`FC_OK`(0) 为成功。

use std::ffi::CStr;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::raw::c_char;
use std::path::Path;
use std::slice;

pub const FC_OK: i32 = 0;
pub const FC_ERR_IO: i32 = -1;
pub const FC_ERR_ARG: i32 = -2;
pub const FC_ERR_LEAF_MISMATCH: i32 = -3;
pub const FC_ERR_ROOT_MISMATCH: i32 = -4;

const HASH_SIZE: usize = 32;

/// ABI 版本号，供 Go 侧 smoke test 确认链接成功。
#[no_mangle]
pub extern "C" fn fc_abi_version() -> i32 {
    1
}

/// 把 C 字符串指针安全转成 &Path。
unsafe fn cstr_to_path<'a>(p: *const c_char) -> Option<&'a Path> {
    if p.is_null() {
        return None;
    }
    CStr::from_ptr(p).to_str().ok().map(Path::new)
}

/// 预分配临时文件到 total_size 大小（逻辑占位，后续分片按偏移填入）。
/// 已存在则只调整长度，不清空——支持断点续传时复用同一临时文件。
#[no_mangle]
pub extern "C" fn fc_preallocate(path: *const c_char, total_size: u64) -> i32 {
    let path = match unsafe { cstr_to_path(path) } {
        Some(p) => p,
        None => return FC_ERR_ARG,
    };
    if let Some(parent) = path.parent() {
        if !parent.as_os_str().is_empty() {
            if fs::create_dir_all(parent).is_err() {
                return FC_ERR_IO;
            }
        }
    }
    let file = match OpenOptions::new().create(true).write(true).open(path) {
        Ok(f) => f,
        Err(_) => return FC_ERR_IO,
    };
    match file.set_len(total_size) {
        Ok(_) => FC_OK,
        Err(_) => FC_ERR_IO,
    }
}

/// 写单个分片：
/// - `expected_leaf` 非空时，先算 blake3 校验，与期望叶子哈希不一致则拒绝（不落盘）。
/// - 校验通过后 seek 到 `offset` 写入。每次调用独立句柄，不相交区域并发安全。
#[no_mangle]
pub extern "C" fn fc_chunk_write(
    path: *const c_char,
    offset: u64,
    data: *const u8,
    len: usize,
    expected_leaf: *const u8,
) -> i32 {
    let path = match unsafe { cstr_to_path(path) } {
        Some(p) => p,
        None => return FC_ERR_ARG,
    };
    if data.is_null() && len != 0 {
        return FC_ERR_ARG;
    }
    let buf: &[u8] = if len == 0 {
        &[]
    } else {
        unsafe { slice::from_raw_parts(data, len) }
    };

    // 早校验：分片自带 blake3，落盘前先比对
    if !expected_leaf.is_null() {
        let expected = unsafe { slice::from_raw_parts(expected_leaf, HASH_SIZE) };
        let got = blake3::hash(buf);
        if got.as_bytes() != expected {
            return FC_ERR_LEAF_MISMATCH;
        }
    }

    let mut file = match OpenOptions::new().write(true).open(path) {
        Ok(f) => f,
        Err(_) => return FC_ERR_IO,
    };
    if file.seek(SeekFrom::Start(offset)).is_err() {
        return FC_ERR_IO;
    }
    match file.write_all(buf) {
        Ok(_) => FC_OK,
        Err(_) => FC_ERR_IO,
    }
}

/// 计算一段数据的 blake3 分片叶子哈希，写入 `out32`（32 字节）。
/// 客户端与服务端共用，用于生成描述信息里的 leaf_hashes。
#[no_mangle]
pub extern "C" fn fc_hash_chunk(data: *const u8, len: usize, out32: *mut u8) -> i32 {
    if out32.is_null() || (data.is_null() && len != 0) {
        return FC_ERR_ARG;
    }
    let buf: &[u8] = if len == 0 {
        &[]
    } else {
        unsafe { slice::from_raw_parts(data, len) }
    };
    let h = blake3::hash(buf);
    unsafe {
        slice::from_raw_parts_mut(out32, HASH_SIZE).copy_from_slice(h.as_bytes());
    }
    FC_OK
}

/// 从叶子哈希数组（`leaves` 指向 count*32 字节）构造 Merkle 树根，写入 `out32`。
/// 树的构造方式必须与客户端一致，因此双端都调用本函数。
#[no_mangle]
pub extern "C" fn fc_merkle_root(leaves: *const u8, count: usize, out32: *mut u8) -> i32 {
    if out32.is_null() {
        return FC_ERR_ARG;
    }
    if count != 0 && leaves.is_null() {
        return FC_ERR_ARG;
    }
    let raw = if count == 0 {
        &[][..]
    } else {
        unsafe { slice::from_raw_parts(leaves, count * HASH_SIZE) }
    };
    let mut level: Vec<[u8; HASH_SIZE]> = Vec::with_capacity(count);
    for i in 0..count {
        let mut leaf = [0u8; HASH_SIZE];
        leaf.copy_from_slice(&raw[i * HASH_SIZE..(i + 1) * HASH_SIZE]);
        level.push(leaf);
    }
    let root = merkle_root(&level);
    unsafe {
        slice::from_raw_parts_mut(out32, HASH_SIZE).copy_from_slice(&root);
    }
    FC_OK
}

/// Merkle 树根：叶子两两合并 parent = blake3(left || right)，奇数节点原样进位到上层。
/// 空 → blake3("")；单叶子 → 该叶子本身。此规则须与客户端严格一致。
fn merkle_root(leaves: &[[u8; HASH_SIZE]]) -> [u8; HASH_SIZE] {
    if leaves.is_empty() {
        return *blake3::hash(b"").as_bytes();
    }
    let mut level = leaves.to_vec();
    while level.len() > 1 {
        let mut next = Vec::with_capacity((level.len() + 1) / 2);
        let mut i = 0;
        while i < level.len() {
            if i + 1 < level.len() {
                let mut h = blake3::Hasher::new();
                h.update(&level[i]);
                h.update(&level[i + 1]);
                next.push(*h.finalize().as_bytes());
                i += 2;
            } else {
                next.push(level[i]); // 奇数节点进位
                i += 1;
            }
        }
        level = next;
    }
    level[0]
}

/// 收齐后对整份临时文件做单趟顺序校验：
/// 1. 按 `chunk_size` 逐块读，算每块叶子哈希；
/// 2. `expected_leaves`（count 个 32 字节，可为空）给定时逐块比对，首个不符
///    经 `out_bad_index` 返回并报 FC_ERR_LEAF_MISMATCH；
/// 3. 用重算出的叶子构造 Merkle 树根，与 `expected_root` 比对，不符报 FC_ERR_ROOT_MISMATCH；
/// 4. 顺带算出整文件 blake3（秒传/去重键）写入 `out_file_hash32`。
///
/// `out_bad_index` 无坏块时置 -1。
#[no_mangle]
pub extern "C" fn fc_finalize(
    path: *const c_char,
    chunk_size: u64,
    _total_size: u64,
    expected_leaves: *const u8,
    leaf_count: usize,
    expected_root: *const u8,
    out_file_hash32: *mut u8,
    out_bad_index: *mut i64,
) -> i32 {
    let path = match unsafe { cstr_to_path(path) } {
        Some(p) => p,
        None => return FC_ERR_ARG,
    };
    if chunk_size == 0 || out_file_hash32.is_null() {
        return FC_ERR_ARG;
    }
    if !out_bad_index.is_null() {
        unsafe { *out_bad_index = -1 };
    }

    let mut file = match File::open(path) {
        Ok(f) => f,
        Err(_) => return FC_ERR_IO,
    };

    let expected_leaves: Option<&[u8]> = if leaf_count == 0 || expected_leaves.is_null() {
        None
    } else {
        Some(unsafe { slice::from_raw_parts(expected_leaves, leaf_count * HASH_SIZE) })
    };

    let mut leaves: Vec<[u8; HASH_SIZE]> = Vec::new();
    let mut file_hasher = blake3::Hasher::new();
    let mut buf = vec![0u8; chunk_size as usize];
    let mut index: usize = 0;

    loop {
        // 读满一块（read 可能短读，循环补齐到 chunk_size 或 EOF）
        let mut filled = 0usize;
        while filled < buf.len() {
            match file.read(&mut buf[filled..]) {
                Ok(0) => break,
                Ok(n) => filled += n,
                Err(_) => return FC_ERR_IO,
            }
        }
        if filled == 0 {
            break;
        }
        let block = &buf[..filled];
        file_hasher.update(block);
        let leaf = blake3::hash(block);

        if let Some(exp) = expected_leaves {
            if index < leaf_count {
                let want = &exp[index * HASH_SIZE..(index + 1) * HASH_SIZE];
                if leaf.as_bytes() != want {
                    if !out_bad_index.is_null() {
                        unsafe { *out_bad_index = index as i64 };
                    }
                    return FC_ERR_LEAF_MISMATCH;
                }
            }
        }
        leaves.push(*leaf.as_bytes());
        index += 1;

        if filled < buf.len() {
            break; // 最后一块（短读）
        }
    }

    // Merkle 树根校验
    if !expected_root.is_null() {
        let root = merkle_root(&leaves);
        let want = unsafe { slice::from_raw_parts(expected_root, HASH_SIZE) };
        if root != want {
            return FC_ERR_ROOT_MISMATCH;
        }
    }

    // 整文件哈希（秒传/去重）
    let file_hash = file_hasher.finalize();
    unsafe {
        slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(file_hash.as_bytes());
    }
    FC_OK
}

/// 原子落盘：确保目标父目录存在后 rename；跨卷 rename 失败则退化为 copy+remove。
#[no_mangle]
pub extern "C" fn fc_move(src: *const c_char, dst: *const c_char) -> i32 {
    let src = match unsafe { cstr_to_path(src) } {
        Some(p) => p,
        None => return FC_ERR_ARG,
    };
    let dst = match unsafe { cstr_to_path(dst) } {
        Some(p) => p,
        None => return FC_ERR_ARG,
    };
    if let Some(parent) = dst.parent() {
        if !parent.as_os_str().is_empty() {
            if fs::create_dir_all(parent).is_err() {
                return FC_ERR_IO;
            }
        }
    }
    if fs::rename(src, dst).is_ok() {
        return FC_OK;
    }
    // 跨卷等情况退化：copy 后删源
    if fs::copy(src, dst).is_err() {
        return FC_ERR_IO;
    }
    let _ = fs::remove_file(src);
    FC_OK
}
