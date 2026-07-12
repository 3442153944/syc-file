//! filecore：文件同步上传的 Rust 核心库（C ABI，对外无状态）。
//!
//! 服务端（Go/cgo）与桌面端（Tauri/Rust）共用；后续还将接入安卓（NDK/cgo）与
//! 鸿蒙（OHOS）。平台差异全部收敛在本文件的 cfg 分支：
//! - unix 系（Linux / Android / macOS / OHOS）：pwrite 定位写；Linux/Android 加 fallocate
//! - Windows：OVERLAPPED 偏移写 + NTFS sparse 标记
//! - 其余目标直接编译期报错，避免悄悄退化
//!
//! v2 要点（对 v1 的兼容扩展：新增 `fc_evict` 与 `FC_ERR_SIZE_MISMATCH`）：
//! - 句柄缓存：按路径缓存写句柄，消除每分片一次 open/close（Windows 上 CreateFile
//!   要过全套过滤驱动/Defender，代价远高于 Linux）。缓存只是加速层，逐出随时可重开。
//!   用全局世代号防 ABA：evict/preallocate/move 都推进世代，open 窗口横跨世代变化
//!   的句柄只用于本次写、不入缓存，杜绝“缓存里留着已改名/已删除文件的旧句柄”。
//! - 定位写：unix 用 pwrite（write_all_at），Windows 用 OVERLAPPED 偏移写
//!   （seek_write），共享句柄对不相交区域并发安全，且省掉 seek 系统调用。
//! - NTFS sparse：预分配时打 FSCTL_SET_SPARSE，消除乱序写触发的
//!   valid-data-length 同步零填充（Windows 乱序写的头号隐藏开销）。
//! - fallocate：Linux/Android 真实预留 extent（减少碎片、ENOSPC 提前暴露）；
//!   预分配总是以 set_len 收尾——fallocate 只扩不缩，必须截断旧的过大临时文件。
//! - finalize：mmap 窗口化单趟——每个窗口内 rayon 跨分片并行算叶子、
//!   update_rayon 并行推进整文件哈希，整体只顺序读一遍；mmap 失败回退流式。
//! - FFI panic 安全：所有导出包 catch_unwind（profile 必须 panic = "unwind"），
//!   panic 统一转 FC_ERR_IO，绝不跨 C ABI 展开。
//!
//! # 调用方（编排层）必须遵守的约定
//! - 删除临时文件前先调 `fc_evict(path)`，否则句柄缓存可能残留指向已删文件的句柄，
//!   同路径新会话的写入会静默丢失。
//! - `fc_finalize` / `fc_move` 期间不得有同一路径的 `fc_chunk_write` /
//!   `fc_preallocate` 在途（finalize 走 mmap，途中截断文件在 unix 上是 SIGBUS）。
//!   服务端语义天然满足：收齐所有分片才 finalize，会话 id 绑定 total/chunk 大小。

// C ABI 导出必然接收裸指针；空指针/长度均在入口显式校验，越界责任在调用方，
// 契约随 filecore.h 一起约定。这是 FFI 库的固有形态，不逐个标 unsafe fn
//（标了反而让 cbindgen/头文件失真，Go/C 调用方看不到任何差别）。
#![allow(clippy::not_unsafe_ptr_arg_deref)]

#[cfg(not(any(unix, windows)))]
compile_error!("filecore 仅支持 unix 系（Linux/Android/macOS/OHOS）与 Windows 目标");

use std::collections::HashMap;
use std::ffi::CStr;
use std::fs::{self, File, OpenOptions};
use std::io;
use std::os::raw::c_char;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::path::{Path, PathBuf};
use std::slice;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, OnceLock};

use memmap2::Mmap;
use rayon::prelude::*;

pub const FC_OK: i32 = 0;
pub const FC_ERR_IO: i32 = -1;
pub const FC_ERR_ARG: i32 = -2;
pub const FC_ERR_LEAF_MISMATCH: i32 = -3;
pub const FC_ERR_ROOT_MISMATCH: i32 = -4;
/// 文件实际尺寸/分片数与描述不符（v1 里混在 ROOT_MISMATCH，v2 拆出）。
pub const FC_ERR_SIZE_MISMATCH: i32 = -5;

const HASH_SIZE: usize = 32;
/// 句柄缓存上限，超过后整体清空（在途 Arc 不受影响，仅防止废弃会话堆积句柄）。
const HANDLE_CACHE_CAP: usize = 256;
/// chunk_size 上限：防御性参数校验，同时保证 32 位目标上 u64→usize 不截断、
/// 流式回退的分块缓冲不会异常巨大（分配失败在 Rust 里是 abort，必须挡在前面）。
const MAX_CHUNK_SIZE: u64 = 1 << 30; // 1 GiB
/// finalize 单趟扫描的窗口目标大小（会向上取整到 chunk_size 的整数倍）。
const FINALIZE_WINDOW: usize = 64 << 20; // 64 MiB

/// ABI 版本号，供宿主 smoke test 确认链接成功。
/// v2：新增 fc_evict、FC_ERR_SIZE_MISMATCH；v3：新增 fc_describe（客户端描述计算）。
#[no_mangle]
pub extern "C" fn fc_abi_version() -> i32 {
    3
}

// ---------------------------------------------------------------------------
// 通用工具
// ---------------------------------------------------------------------------

/// 把 C 字符串指针安全转成 &Path（要求 UTF-8，Go/Tauri 侧均满足）。
unsafe fn cstr_to_path<'a>(p: *const c_char) -> Option<&'a Path> {
    if p.is_null() {
        return None;
    }
    CStr::from_ptr(p).to_str().ok().map(Path::new)
}

/// FFI 边界的 panic 防护：panic 跨 C ABI 展开是 UB，统一兜成 FC_ERR_IO。
/// 依赖 profile 的 panic = "unwind"（abort 模式下 panic 直接带崩宿主进程）。
/// R 泛化以支持 i32（多数导出）与 i64（fc_describe 返回叶子数）。
fn ffi_guard<R: From<i32>, F: FnOnce() -> R>(f: F) -> R {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or_else(|_| R::from(FC_ERR_IO))
}

/// 加锁并忽略毒化：缓存内容只是 Arc<File>，任何中间状态都自洽，
/// 毒化后继续用远好于让所有后续 FFI 调用永久失败。
fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

// ---------------------------------------------------------------------------
// 写句柄缓存
// ---------------------------------------------------------------------------

fn handles() -> &'static Mutex<HashMap<PathBuf, Arc<File>>> {
    static HANDLES: OnceLock<Mutex<HashMap<PathBuf, Arc<File>>>> = OnceLock::new();
    HANDLES.get_or_init(|| Mutex::new(HashMap::new()))
}

/// 世代号：每次 evict（含 preallocate/move/finalize 内部逐出）加一。
/// cached_write_handle 在 open 前后比对世代，跨越世代的句柄不入缓存，
/// 防止“open 旧文件 → 别人 rename/删除 → 把旧句柄插回缓存”的 ABA。
static GENERATION: AtomicU64 = AtomicU64::new(0);

/// 取（或打开并缓存）某路径的写句柄。
fn cached_write_handle(path: &Path) -> io::Result<Arc<File>> {
    {
        let map = lock(handles());
        if let Some(f) = map.get(path) {
            return Ok(Arc::clone(f));
        }
    }
    let gen0 = GENERATION.load(Ordering::Acquire);
    // open 放在锁外，避免 Windows 上慢速 CreateFile 串行化所有缓存未命中
    let opened = Arc::new(OpenOptions::new().write(true).open(path)?);
    let mut map = lock(handles());
    if GENERATION.load(Ordering::Acquire) != gen0 {
        // open 期间发生过逐出（可能正是本路径被 rename/删除）：仅本次使用，不缓存
        return Ok(opened);
    }
    if map.len() >= HANDLE_CACHE_CAP {
        map.clear(); // 粗暴但安全：在途写入各自持有 Arc
        GENERATION.fetch_add(1, Ordering::AcqRel);
    }
    let entry = map
        .entry(path.to_path_buf())
        .or_insert_with(|| Arc::clone(&opened));
    Ok(Arc::clone(entry))
}

/// 从缓存中逐出并关闭某路径的句柄（若存在），并推进世代号。
/// finalize / move / 删除临时文件前必须调用。
fn evict_handle(path: &Path) {
    let mut map = lock(handles());
    map.remove(path);
    GENERATION.fetch_add(1, Ordering::AcqRel);
}

/// 平台定位写：不依赖文件游标，共享句柄对不相交区域并发安全。
#[cfg(unix)]
fn write_at(file: &File, buf: &[u8], offset: u64) -> io::Result<()> {
    use std::os::unix::fs::FileExt;
    file.write_all_at(buf, offset)
}

#[cfg(windows)]
fn write_at(file: &File, mut buf: &[u8], mut offset: u64) -> io::Result<()> {
    use std::os::windows::fs::FileExt;
    // seek_write 底层是带 OVERLAPPED 偏移的 WriteFile：
    // 偏移来自参数而非游标，游标被并发移动也不影响正确性。
    while !buf.is_empty() {
        let n = file.seek_write(buf, offset)?;
        if n == 0 {
            return Err(io::Error::new(io::ErrorKind::WriteZero, "seek_write 0"));
        }
        buf = &buf[n..];
        offset += n as u64;
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// 预分配
// ---------------------------------------------------------------------------

/// Windows：把临时文件标记为 sparse。
/// NTFS 对普通文件维护 valid data length，乱序往超前偏移写入会触发
/// 同步零填充 [VDL, offset) 的所有空洞——分片乱序上传的最大隐藏开销。
/// sparse 文件没有这个惩罚，空洞按需分配。
#[cfg(windows)]
fn set_sparse(file: &File) {
    use std::os::windows::io::AsRawHandle;
    use windows_sys::Win32::System::Ioctl::FSCTL_SET_SPARSE;
    use windows_sys::Win32::System::IO::DeviceIoControl;
    let mut returned: u32 = 0;
    unsafe {
        // 失败（如 FAT32 等不支持）不致命，退化为普通文件，仅性能差些
        DeviceIoControl(
            file.as_raw_handle() as _,
            FSCTL_SET_SPARSE,
            std::ptr::null(),
            0,
            std::ptr::null_mut(),
            0,
            &mut returned,
            std::ptr::null_mut(),
        );
    }
}

/// 预分配临时文件到 total_size 大小（逻辑占位，后续分片按偏移填入）。
/// 已存在则只调整长度，不清空——支持断点续传时复用同一临时文件。
///
/// Linux/Android：优先 fallocate 真实预留 extent，之后仍以 set_len 收尾
///（fallocate 只扩不缩，须截断同路径遗留的过大旧文件）；不支持时仅 set_len。
/// Windows：sparse 标记 + set_len（SetEndOfFile）。
///
/// 进门先逐出本路径的缓存句柄：同路径新会话必须从新文件开始。
#[no_mangle]
pub extern "C" fn fc_preallocate(path: *const c_char, total_size: u64) -> i32 {
    ffi_guard(|| {
        let path = match unsafe { cstr_to_path(path) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        evict_handle(path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() && fs::create_dir_all(parent).is_err() {
                return FC_ERR_IO;
            }
        }
        // truncate(false)：已存在则保留内容（断点续传复用），长度由末尾 set_len 统一
        let file = match OpenOptions::new()
            .create(true)
            .write(true)
            .truncate(false)
            .open(path)
        {
            Ok(f) => f,
            Err(_) => return FC_ERR_IO,
        };

        #[cfg(windows)]
        set_sparse(&file);

        // off_t 在 32 位目标上可能是 i32：装不下就跳过 fallocate，直接 set_len
        #[cfg(any(target_os = "linux", target_os = "android"))]
        if total_size > 0 && total_size <= libc::off_t::MAX as u64 {
            use std::os::unix::io::AsRawFd;
            // EOPNOTSUPP 等失败不致命，set_len 兜底
            unsafe {
                libc::fallocate(file.as_raw_fd(), 0, 0, total_size as libc::off_t);
            }
        }

        match file.set_len(total_size) {
            Ok(_) => FC_OK,
            Err(_) => FC_ERR_IO,
        }
    })
}

// ---------------------------------------------------------------------------
// 分片写入
// ---------------------------------------------------------------------------

/// 写单个分片：
/// - `expected_leaf` 非空时，先算 blake3 校验，与期望叶子哈希不一致则拒绝（不落盘）。
/// - 校验通过后按偏移定位写。句柄来自进程内缓存，不相交区域并发安全。
#[no_mangle]
pub extern "C" fn fc_chunk_write(
    path: *const c_char,
    offset: u64,
    data: *const u8,
    len: usize,
    expected_leaf: *const u8,
) -> i32 {
    ffi_guard(|| {
        let path = match unsafe { cstr_to_path(path) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        if (data.is_null() && len != 0) || len > isize::MAX as usize {
            return FC_ERR_ARG;
        }
        let buf: &[u8] = if len == 0 {
            &[]
        } else {
            unsafe { slice::from_raw_parts(data, len) }
        };

        // 早校验：分片自带 blake3，落盘前先比对。
        if !expected_leaf.is_null() {
            let expected = unsafe { slice::from_raw_parts(expected_leaf, HASH_SIZE) };
            if hash_block(buf) != expected {
                return FC_ERR_LEAF_MISMATCH;
            }
        }

        let file = match cached_write_handle(path) {
            Ok(f) => f,
            Err(_) => return FC_ERR_IO,
        };
        match write_at(&file, buf, offset) {
            Ok(_) => FC_OK,
            Err(_) => {
                // 写失败可能意味着句柄已过期（文件被外部删除/移动），逐出让下次重开
                evict_handle(path);
                FC_ERR_IO
            }
        }
    })
}

/// 单块哈希：小块单线程（避免调度开销），大块走 rayon 并行。
fn hash_block(buf: &[u8]) -> [u8; HASH_SIZE] {
    const PAR_THRESHOLD: usize = 128 * 1024; // blake3 官方建议的并行收益起点
    if buf.len() >= PAR_THRESHOLD {
        let mut h = blake3::Hasher::new();
        h.update_rayon(buf);
        *h.finalize().as_bytes()
    } else {
        *blake3::hash(buf).as_bytes()
    }
}

/// 计算一段数据的 blake3 分片叶子哈希，写入 `out32`（32 字节）。
/// 客户端与服务端共用，用于生成描述信息里的 leaf_hashes。
#[no_mangle]
pub extern "C" fn fc_hash_chunk(data: *const u8, len: usize, out32: *mut u8) -> i32 {
    ffi_guard(|| {
        if out32.is_null() || (data.is_null() && len != 0) || len > isize::MAX as usize {
            return FC_ERR_ARG;
        }
        let buf: &[u8] = if len == 0 {
            &[]
        } else {
            unsafe { slice::from_raw_parts(data, len) }
        };
        let h = hash_block(buf);
        unsafe {
            slice::from_raw_parts_mut(out32, HASH_SIZE).copy_from_slice(&h);
        }
        FC_OK
    })
}

// ---------------------------------------------------------------------------
// Merkle
// ---------------------------------------------------------------------------

/// count 个 32 字节哈希的总字节数（防乘法回绕导致 from_raw_parts 越界 UB）。
fn hashes_byte_len(count: usize) -> Option<usize> {
    match count.checked_mul(HASH_SIZE) {
        Some(n) if n <= isize::MAX as usize => Some(n),
        _ => None,
    }
}

/// 从叶子哈希数组（`leaves` 指向 count*32 字节）构造 Merkle 树根，写入 `out32`。
/// 树的构造方式必须与客户端一致，因此双端都调用本函数。
#[no_mangle]
pub extern "C" fn fc_merkle_root(leaves: *const u8, count: usize, out32: *mut u8) -> i32 {
    ffi_guard(|| {
        if out32.is_null() || (count != 0 && leaves.is_null()) {
            return FC_ERR_ARG;
        }
        let byte_len = match hashes_byte_len(count) {
            Some(n) => n,
            None => return FC_ERR_ARG,
        };
        let raw = if count == 0 {
            &[][..]
        } else {
            unsafe { slice::from_raw_parts(leaves, byte_len) }
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
    })
}

/// Merkle 树根：叶子两两合并 parent = blake3(left || right)，奇数节点原样进位到上层。
/// 空 → blake3("")；单叶子 → 该叶子本身。此规则须与客户端严格一致。
fn merkle_root(leaves: &[[u8; HASH_SIZE]]) -> [u8; HASH_SIZE] {
    if leaves.is_empty() {
        return *blake3::hash(b"").as_bytes();
    }
    let mut level = leaves.to_vec();
    while level.len() > 1 {
        let mut next = Vec::with_capacity(level.len().div_ceil(2));
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

// ---------------------------------------------------------------------------
// finalize
// ---------------------------------------------------------------------------

/// 收齐后对整份临时文件做校验：
/// 1. 按 `chunk_size` 分块算叶子哈希（mmap 窗口化单趟 + rayon 跨分片并行）；
/// 2. `expected_leaves`（count 个 32 字节，可为空）给定时逐块比对，首个不符
///    经 `out_bad_index` 返回并报 FC_ERR_LEAF_MISMATCH；
/// 3. 用重算出的叶子构造 Merkle 树根，与 `expected_root` 比对，不符报 FC_ERR_ROOT_MISMATCH；
/// 4. 顺带算出整文件 blake3（秒传/去重键，update_rayon 多核）写入 `out_file_hash32`。
///
/// `total_size` 非 0 时先做长度早校验，不符报 FC_ERR_SIZE_MISMATCH。
/// `out_bad_index` 无坏块时置 -1。mmap 失败（网络盘等）自动回退流式单趟。
///
/// 调用期间不得有同一路径的写入/预分配在途（见模块级说明）。
#[no_mangle]
pub extern "C" fn fc_finalize(
    path: *const c_char,
    chunk_size: u64,
    total_size: u64,
    expected_leaves: *const u8,
    leaf_count: usize,
    expected_root: *const u8,
    out_file_hash32: *mut u8,
    out_bad_index: *mut i64,
) -> i32 {
    ffi_guard(|| {
        let path = match unsafe { cstr_to_path(path) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        if chunk_size == 0 || chunk_size > MAX_CHUNK_SIZE || out_file_hash32.is_null() {
            return FC_ERR_ARG;
        }
        let leaves_byte_len = match hashes_byte_len(leaf_count) {
            Some(n) => n,
            None => return FC_ERR_ARG,
        };
        if !out_bad_index.is_null() {
            unsafe { *out_bad_index = -1 };
        }

        // 关闭缓存写句柄，为后续 rename 让路（在途写由编排层保证已结束）
        evict_handle(path);

        let file = match File::open(path) {
            Ok(f) => f,
            Err(_) => return FC_ERR_IO,
        };
        let actual_len = match file.metadata() {
            Ok(m) => m.len(),
            Err(_) => return FC_ERR_IO,
        };
        // 长度早校验：文件尺寸都不对，叶子/根必然不符，直接短路
        if total_size != 0 && actual_len != total_size {
            return FC_ERR_SIZE_MISMATCH;
        }

        let expected_leaves: Option<&[u8]> = if leaf_count == 0 || expected_leaves.is_null() {
            None
        } else {
            Some(unsafe { slice::from_raw_parts(expected_leaves, leaves_byte_len) })
        };
        let expected_root: Option<&[u8]> = if expected_root.is_null() {
            None
        } else {
            Some(unsafe { slice::from_raw_parts(expected_root, HASH_SIZE) })
        };

        // 空文件：mmap 会失败，单独处理
        if actual_len == 0 {
            let empty = *blake3::hash(b"").as_bytes();
            if expected_leaves.is_some() {
                return FC_ERR_SIZE_MISMATCH; // 期望有分片但文件为空
            }
            if let Some(want) = expected_root {
                if empty != want {
                    return FC_ERR_ROOT_MISMATCH;
                }
            }
            unsafe {
                slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(&empty);
            }
            return FC_OK;
        }

        let (leaves, file_hash) = match compute_leaves_and_file_hash(&file, chunk_size) {
            Ok(r) => r,
            Err(_) => return FC_ERR_IO,
        };

        // 叶子比对（顺序找首个坏块，纯 memcmp，开销可忽略）
        if let Some(exp) = expected_leaves {
            if leaves.len() != leaf_count {
                return FC_ERR_SIZE_MISMATCH; // 分片数与描述不符
            }
            for (i, leaf) in leaves.iter().enumerate() {
                if leaf[..] != exp[i * HASH_SIZE..(i + 1) * HASH_SIZE] {
                    if !out_bad_index.is_null() {
                        unsafe { *out_bad_index = i as i64 };
                    }
                    return FC_ERR_LEAF_MISMATCH;
                }
            }
        }

        // Merkle 树根校验
        if let Some(want) = expected_root {
            if merkle_root(&leaves) != want {
                return FC_ERR_ROOT_MISMATCH;
            }
        }

        unsafe {
            slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(&file_hash);
        }
        FC_OK
    })
}

/// 对非空文件按 chunk_size 分块算叶子哈希 + 整文件 blake3。
/// 优先 mmap 窗口化单趟（窗口内叶子并行 + update_rayon 推进整文件哈希），
/// mmap 失败（网络盘/32 位超大文件等）回退流式单趟。fc_finalize / fc_describe 共用。
fn compute_leaves_and_file_hash(
    file: &File,
    chunk_size: u64,
) -> io::Result<(Vec<[u8; HASH_SIZE]>, [u8; HASH_SIZE])> {
    match unsafe { Mmap::map(file) } {
        Ok(mmap) => {
            #[cfg(unix)]
            let _ = mmap.advise(memmap2::Advice::Sequential);

            let cs = chunk_size as usize;
            // 窗口 = chunk_size 的整数倍且 ≥ FINALIZE_WINDOW：整体只顺序读一遍
            let window = FINALIZE_WINDOW.div_ceil(cs).max(1) * cs;
            let mut leaves: Vec<[u8; HASH_SIZE]> = Vec::with_capacity(mmap.len().div_ceil(cs));
            let mut fh = blake3::Hasher::new();
            for win in mmap.chunks(window) {
                leaves.par_extend(win.par_chunks(cs).map(|c| *blake3::hash(c).as_bytes()));
                fh.update_rayon(win);
            }
            Ok((leaves, *fh.finalize().as_bytes()))
        }
        Err(_) => finalize_streaming(file, chunk_size),
    }
}

/// mmap 不可用时的流式回退：单趟顺序读，同时算叶子与整文件哈希。
fn finalize_streaming(
    file: &File,
    chunk_size: u64,
) -> io::Result<(Vec<[u8; HASH_SIZE]>, [u8; HASH_SIZE])> {
    use std::io::Read;
    let mut file = file;
    let mut leaves: Vec<[u8; HASH_SIZE]> = Vec::new();
    let mut file_hasher = blake3::Hasher::new();
    let mut buf = vec![0u8; chunk_size as usize];

    loop {
        let mut filled = 0usize;
        while filled < buf.len() {
            match file.read(&mut buf[filled..])? {
                0 => break,
                n => filled += n,
            }
        }
        if filled == 0 {
            break;
        }
        let block = &buf[..filled];
        file_hasher.update(block);
        leaves.push(*blake3::hash(block).as_bytes());
        if filled < buf.len() {
            break; // 最后一块（短读）
        }
    }
    Ok((leaves, *file_hasher.finalize().as_bytes()))
}

// ---------------------------------------------------------------------------
// describe（客户端侧：一趟算出上传描述信息）
// ---------------------------------------------------------------------------

/// 【v3 新增，客户端用】计算文件的上传描述信息：
/// 按 `chunk_size` 分块的叶子哈希写入 `out_leaves`（容量 `leaf_cap` 个 32 字节），
/// Merkle 树根写入 `out_root32`，整文件 blake3 写入 `out_file_hash32`。
/// mmap 窗口化单趟 + rayon 并行，与服务端 fc_finalize 的重算逐字节一致。
///
/// 返回：>= 0 实际叶子数；负数为 FC_ERR_*（`leaf_cap` 不足返回 FC_ERR_ARG，
/// 调用方按新文件大小扩容重试）。空文件返回 0，root/file_hash = blake3("")。
///
/// 注意：计算期间文件不得被并发写入（客户端对自己的源文件天然满足）。
#[no_mangle]
pub extern "C" fn fc_describe(
    path: *const c_char,
    chunk_size: u64,
    out_leaves: *mut u8,
    leaf_cap: usize,
    out_root32: *mut u8,
    out_file_hash32: *mut u8,
) -> i64 {
    ffi_guard(|| -> i64 {
        let err = |code: i32| code as i64;
        let path = match unsafe { cstr_to_path(path) } {
            Some(p) => p,
            None => return err(FC_ERR_ARG),
        };
        if chunk_size == 0
            || chunk_size > MAX_CHUNK_SIZE
            || out_root32.is_null()
            || out_file_hash32.is_null()
            || (out_leaves.is_null() && leaf_cap != 0)
        {
            return err(FC_ERR_ARG);
        }
        if hashes_byte_len(leaf_cap).is_none() {
            return err(FC_ERR_ARG);
        }

        let file = match File::open(path) {
            Ok(f) => f,
            Err(_) => return err(FC_ERR_IO),
        };
        let len = match file.metadata() {
            Ok(m) => m.len(),
            Err(_) => return err(FC_ERR_IO),
        };

        // 空文件：0 叶子，root/file_hash 均为 blake3("")（与 merkle_root(空) 一致）
        if len == 0 {
            let empty = *blake3::hash(b"").as_bytes();
            unsafe {
                slice::from_raw_parts_mut(out_root32, HASH_SIZE).copy_from_slice(&empty);
                slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(&empty);
            }
            return 0;
        }

        let (leaves, file_hash) = match compute_leaves_and_file_hash(&file, chunk_size) {
            Ok(r) => r,
            Err(_) => return err(FC_ERR_IO),
        };
        if leaves.len() > leaf_cap {
            return err(FC_ERR_ARG); // 容量不足（文件比 stat 时更大），调用方扩容重试
        }

        let root = merkle_root(&leaves);
        unsafe {
            let out = slice::from_raw_parts_mut(out_leaves, leaves.len() * HASH_SIZE);
            for (i, leaf) in leaves.iter().enumerate() {
                out[i * HASH_SIZE..(i + 1) * HASH_SIZE].copy_from_slice(leaf);
            }
            slice::from_raw_parts_mut(out_root32, HASH_SIZE).copy_from_slice(&root);
            slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(&file_hash);
        }
        leaves.len() as i64
    })
}

// ---------------------------------------------------------------------------
// move / evict
// ---------------------------------------------------------------------------

/// 原子落盘：确保目标父目录存在后 rename；跨卷 rename 失败则退化为 copy+remove。
/// rename 前先逐出缓存句柄（Windows 上避免任何共享冲突的可能）。
#[no_mangle]
pub extern "C" fn fc_move(src: *const c_char, dst: *const c_char) -> i32 {
    ffi_guard(|| {
        let src = match unsafe { cstr_to_path(src) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        let dst = match unsafe { cstr_to_path(dst) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        evict_handle(src);
        evict_handle(dst);
        if let Some(parent) = dst.parent() {
            if !parent.as_os_str().is_empty() && fs::create_dir_all(parent).is_err() {
                return FC_ERR_IO;
            }
        }
        if fs::rename(src, dst).is_ok() {
            return FC_OK;
        }
        // 跨卷等情况退化：copy 后删源（Linux 下 fs::copy 走 copy_file_range）
        if fs::copy(src, dst).is_err() {
            return FC_ERR_IO;
        }
        let _ = fs::remove_file(src);
        FC_OK
    })
}

/// 逐出并关闭某路径的缓存写句柄。
/// 编排层在删除/移动临时文件前【必须】调用（会话超时、取消、清理僵尸文件等），
/// 否则缓存可能残留指向已删文件的句柄，同路径新会话的写入会静默丢失。
#[no_mangle]
pub extern "C" fn fc_evict(path: *const c_char) -> i32 {
    ffi_guard(|| {
        let path = match unsafe { cstr_to_path(path) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        evict_handle(path);
        FC_OK
    })
}
