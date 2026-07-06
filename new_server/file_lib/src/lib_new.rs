//! filecore：文件同步上传的 Rust 核心库（C ABI，对外无状态）。
//!
//! 本版优化要点（ABI 与旧版完全兼容，仅新增可选导出 `fc_evict`）：
//! - 句柄缓存：内部按路径缓存写句柄，消除每分片一次 open/close（Windows 上
//!   CreateFile 要过全套过滤驱动/Defender，代价远高于 Linux）。对外仍无状态：
//!   缓存只是加速层，丢了随时可重开，重启后一切照常。
//! - 定位写：Linux 用 pwrite（write_all_at），Windows 用 OVERLAPPED 偏移写
//!   （seek_write），共享句柄对不相交区域并发安全，且省掉 seek 系统调用。
//! - NTFS sparse：预分配时打 FSCTL_SET_SPARSE，消除乱序写触发的
//!   valid-data-length 同步零填充（Windows 乱序写的头号隐藏开销）。
//! - Linux fallocate：真实预留 extent，减少碎片、ENOSPC 提前暴露。
//! - finalize：mmap 免拷贝 + rayon 跨分片并行算叶子 + blake3 update_rayon
//!   并行算整文件哈希；mmap 失败自动回退流式单趟。
//! - FFI panic 安全：所有导出包 catch_unwind，panic 统一转 FC_ERR_IO。
//!
//! Cargo.toml 需要：
//! ```toml
//! [dependencies]
//! blake3  = { version = "1", features = ["rayon"] }
//! memmap2 = "0.9"
//! rayon   = "1"
//! once_cell = "1"
//!
//! [target.'cfg(target_os = "linux")'.dependencies]
//! libc = "0.2"
//!
//! [target.'cfg(windows)'.dependencies]
//! windows-sys = { version = "0.59", features = [
//!     "Win32_Foundation", "Win32_System_IO", "Win32_System_Ioctl",
//! ] }
//!
//! [profile.release]
//! lto = "fat"
//! codegen-units = 1
//! ```

use std::collections::HashMap;
use std::ffi::CStr;
use std::fs::{self, File, OpenOptions};
use std::io;
use std::os::raw::c_char;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::path::{Path, PathBuf};
use std::slice;
use std::sync::{Arc, Mutex};

use memmap2::Mmap;
use once_cell::sync::Lazy;
use rayon::prelude::*;

pub const FC_OK: i32 = 0;
pub const FC_ERR_IO: i32 = -1;
pub const FC_ERR_ARG: i32 = -2;
pub const FC_ERR_LEAF_MISMATCH: i32 = -3;
pub const FC_ERR_ROOT_MISMATCH: i32 = -4;

const HASH_SIZE: usize = 32;
/// 句柄缓存上限，超过后整体清空（在途 Arc 不受影响，仅防止废弃会话堆积句柄）。
const HANDLE_CACHE_CAP: usize = 256;

/// ABI 版本号，供 Go 侧 smoke test 确认链接成功。
/// 本版行为兼容 v1，新增 fc_evict，版本号提到 2。
#[no_mangle]
pub extern "C" fn fc_abi_version() -> i32 {
    2
}

// ---------------------------------------------------------------------------
// 通用工具
// ---------------------------------------------------------------------------

/// 把 C 字符串指针安全转成 &Path。
unsafe fn cstr_to_path<'a>(p: *const c_char) -> Option<&'a Path> {
    if p.is_null() {
        return None;
    }
    CStr::from_ptr(p).to_str().ok().map(Path::new)
}

/// FFI 边界的 panic 防护：panic 跨 C ABI 展开是 UB，统一兜成 FC_ERR_IO。
fn ffi_guard<F: FnOnce() -> i32>(f: F) -> i32 {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or(FC_ERR_IO)
}

// ---------------------------------------------------------------------------
// 写句柄缓存
// ---------------------------------------------------------------------------

static HANDLES: Lazy<Mutex<HashMap<PathBuf, Arc<File>>>> =
    Lazy::new(|| Mutex::new(HashMap::new()));

/// 取（或打开并缓存）某路径的写句柄。
/// 竞态下可能出现两次 open，后到者复用先入者的缓存项，多余句柄随 Arc 丢弃，无害。
fn cached_write_handle(path: &Path) -> io::Result<Arc<File>> {
    {
        let map = HANDLES.lock().unwrap();
        if let Some(f) = map.get(path) {
            return Ok(Arc::clone(f));
        }
    }
    // open 放在锁外，避免 Windows 上慢速 CreateFile 串行化所有缓存未命中
    let opened = Arc::new(OpenOptions::new().write(true).open(path)?);
    let mut map = HANDLES.lock().unwrap();
    if map.len() >= HANDLE_CACHE_CAP {
        map.clear(); // 粗暴但安全：在途写入各自持有 Arc
    }
    let entry = map
        .entry(path.to_path_buf())
        .or_insert_with(|| Arc::clone(&opened));
    Ok(Arc::clone(entry))
}

/// 从缓存中逐出并关闭某路径的句柄（若存在）。
/// finalize / move 前必须调用：虽然 std 在 Windows 上以
/// FILE_SHARE_READ|WRITE|DELETE 打开、理论上不挡 rename，但及时关闭最干净。
fn evict_handle(path: &Path) {
    let mut map = HANDLES.lock().unwrap();
    map.remove(path);
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
    // 偏移来自参数而非游标，游标被移动也不影响并发正确性。
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
        // 失败（如 FAT32/ReFS 配置差异）不致命，退化为普通文件，仅性能差些
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
/// Linux：优先 fallocate 真实预留 extent（减少碎片、ENOSPC 提前暴露），
///        文件系统不支持时回退 set_len（稀疏）。
/// Windows：set_len（SetEndOfFile）+ sparse 标记，消除乱序写零填充。
#[no_mangle]
pub extern "C" fn fc_preallocate(path: *const c_char, total_size: u64) -> i32 {
    ffi_guard(|| {
        let path = match unsafe { cstr_to_path(path) } {
            Some(p) => p,
            None => return FC_ERR_ARG,
        };
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() && fs::create_dir_all(parent).is_err() {
                return FC_ERR_IO;
            }
        }
        let file = match OpenOptions::new().create(true).write(true).open(path) {
            Ok(f) => f,
            Err(_) => return FC_ERR_IO,
        };

        #[cfg(windows)]
        set_sparse(&file);

        #[cfg(target_os = "linux")]
        if total_size > 0 {
            use std::os::unix::io::AsRawFd;
            let r = unsafe {
                libc::fallocate(file.as_raw_fd(), 0, 0, total_size as libc::off_t)
            };
            if r == 0 {
                return FC_OK;
            }
            // EOPNOTSUPP 等：回退 set_len
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
        if data.is_null() && len != 0 {
            return FC_ERR_ARG;
        }
        let buf: &[u8] = if len == 0 {
            &[]
        } else {
            unsafe { slice::from_raw_parts(data, len) }
        };

        // 早校验：分片自带 blake3，落盘前先比对。
        // 大分片（>=128KiB）用 rayon 并行哈希，blake3 树形结构可近线性加速。
        if !expected_leaf.is_null() {
            let expected = unsafe { slice::from_raw_parts(expected_leaf, HASH_SIZE) };
            let got = hash_block(buf);
            if got != expected {
                return FC_ERR_LEAF_MISMATCH;
            }
        }

        let file = match cached_write_handle(path) {
            Ok(f) => f,
            Err(_) => return FC_ERR_IO,
        };
        match write_at(&file, buf, offset) {
            Ok(_) => FC_OK,
            Err(_) => FC_ERR_IO,
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
        if out32.is_null() || (data.is_null() && len != 0) {
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

/// 从叶子哈希数组（`leaves` 指向 count*32 字节）构造 Merkle 树根，写入 `out32`。
/// 树的构造方式必须与客户端一致，因此双端都调用本函数。
#[no_mangle]
pub extern "C" fn fc_merkle_root(leaves: *const u8, count: usize, out32: *mut u8) -> i32 {
    ffi_guard(|| {
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

// ---------------------------------------------------------------------------
// finalize
// ---------------------------------------------------------------------------

/// 收齐后对整份临时文件做校验：
/// 1. 按 `chunk_size` 分块算叶子哈希（mmap + rayon 跨分片并行）；
/// 2. `expected_leaves`（count 个 32 字节，可为空）给定时逐块比对，首个不符
///    经 `out_bad_index` 返回并报 FC_ERR_LEAF_MISMATCH；
/// 3. 用重算出的叶子构造 Merkle 树根，与 `expected_root` 比对，不符报 FC_ERR_ROOT_MISMATCH；
/// 4. 顺带算出整文件 blake3（秒传/去重键，update_rayon 多核）写入 `out_file_hash32`。
///
/// `total_size` 非 0 时先做长度早校验，不符直接报 FC_ERR_ROOT_MISMATCH。
/// `out_bad_index` 无坏块时置 -1。mmap 失败（网络盘等）自动回退流式单趟。
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
        if chunk_size == 0 || out_file_hash32.is_null() {
            return FC_ERR_ARG;
        }
        if !out_bad_index.is_null() {
            unsafe { *out_bad_index = -1 };
        }

        // 关闭缓存写句柄：确保之前的偏移写全部进入同一视图，也为后续 rename 让路
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
            return FC_ERR_ROOT_MISMATCH;
        }

        let expected_leaves: Option<&[u8]> = if leaf_count == 0 || expected_leaves.is_null() {
            None
        } else {
            Some(unsafe { slice::from_raw_parts(expected_leaves, leaf_count * HASH_SIZE) })
        };
        let expected_root: Option<&[u8]> = if expected_root.is_null() {
            None
        } else {
            Some(unsafe { slice::from_raw_parts(expected_root, HASH_SIZE) })
        };

        // 空文件：mmap 会失败，单独处理
        if actual_len == 0 {
            let empty = *blake3::hash(b"").as_bytes();
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

        let (leaves, file_hash, rc) = match unsafe { Mmap::map(&file) } {
            Ok(mmap) => {
                #[cfg(unix)]
                let _ = mmap.advise(memmap2::Advice::Sequential);

                let cs = chunk_size as usize;
                // 跨分片并行算叶子；每片内部单线程即可（片间并行已吃满核）
                let leaves: Vec<[u8; HASH_SIZE]> = mmap
                    .par_chunks(cs)
                    .map(|c| *blake3::hash(c).as_bytes())
                    .collect();

                // 整文件哈希：blake3 树形并行，页缓存已热，第二趟接近内存带宽
                let mut fh = blake3::Hasher::new();
                fh.update_rayon(&mmap);
                (leaves, *fh.finalize().as_bytes(), FC_OK)
            }
            // mmap 失败：回退旧的流式单趟（正确性优先）
            Err(_) => match finalize_streaming(&file, chunk_size) {
                Ok((l, h)) => (l, h, FC_OK),
                Err(_) => (Vec::new(), [0u8; HASH_SIZE], FC_ERR_IO),
            },
        };
        if rc != FC_OK {
            return rc;
        }

        // 叶子比对（顺序找首个坏块，纯 memcmp，开销可忽略）
        if let Some(exp) = expected_leaves {
            let n = leaves.len().min(leaf_count);
            for i in 0..n {
                if leaves[i] != exp[i * HASH_SIZE..(i + 1) * HASH_SIZE] {
                    if !out_bad_index.is_null() {
                        unsafe { *out_bad_index = i as i64 };
                    }
                    return FC_ERR_LEAF_MISMATCH;
                }
            }
        }

        // Merkle 树根校验
        if let Some(want) = expected_root {
            let root = merkle_root(&leaves);
            if root != want {
                return FC_ERR_ROOT_MISMATCH;
            }
        }

        unsafe {
            slice::from_raw_parts_mut(out_file_hash32, HASH_SIZE).copy_from_slice(&file_hash);
        }
        FC_OK
    })
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

/// 【新增，可选】逐出并关闭某路径的缓存写句柄。
/// Go 侧在会话超时/取消、准备删除临时文件前调用，可立即释放句柄；
/// 不调用也不影响正确性（缓存到达上限会整体清空，进程退出全部释放）。
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