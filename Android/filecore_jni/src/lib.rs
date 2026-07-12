//! filecore 的 Android JNI 包装。
//!
//! 对应 Kotlin 侧 `com.sunyuanling.filesync.core.FileCore`（object，external 成员函数），
//! 因此每个 JNI 函数第二个参数是实例 JObject。所有函数：
//! - 包 catch_unwind（panic 不得跨 JNI 展开），失败一律返回 null/负值，
//!   Kotlin 侧收到 null 即回退纯 Java blake3 实现（正确性不受影响，只慢）；
//! - 直接调用 filecore 的 C ABI 导出（同一实现，与服务端 fc_finalize 逐字节一致）。
//!
//! 构建：`./build.ps1`（cargo-ndk，产物进 app/src/main/jniLibs/<abi>/libfilecore_jni.so）。

use std::ffi::CString;
use std::panic::{catch_unwind, AssertUnwindSafe};

use jni::objects::{JByteArray, JObject, JString};
use jni::sys::{jbyteArray, jint, jlong};
use jni::JNIEnv;

const HASH_SIZE: usize = 32;

fn jnull() -> jbyteArray {
    std::ptr::null_mut()
}

#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_FileCore_nativeAbiVersion(
    _env: JNIEnv,
    _this: JObject,
) -> jint {
    catch_unwind(|| filecore::fc_abi_version()).unwrap_or(-1)
}

/// 计算一段数据的 blake3（32 字节）。失败返回 null。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_FileCore_nativeHashChunk(
    env: JNIEnv,
    _this: JObject,
    data: JByteArray,
) -> jbyteArray {
    catch_unwind(AssertUnwindSafe(|| {
        let bytes = match env.convert_byte_array(&data) {
            Ok(b) => b,
            Err(_) => return jnull(),
        };
        let mut out = [0u8; HASH_SIZE];
        if filecore::fc_hash_chunk(bytes.as_ptr(), bytes.len(), out.as_mut_ptr()) != filecore::FC_OK
        {
            return jnull();
        }
        env.byte_array_from_slice(&out)
            .map(|a| a.into_raw())
            .unwrap_or_else(|_| jnull())
    }))
    .unwrap_or_else(|_| jnull())
}

/// 从拼接叶子（n*32 字节）构造 Merkle 树根。长度非 32 倍数或失败返回 null。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_FileCore_nativeMerkleRoot(
    env: JNIEnv,
    _this: JObject,
    leaves: JByteArray,
) -> jbyteArray {
    catch_unwind(AssertUnwindSafe(|| {
        let bytes = match env.convert_byte_array(&leaves) {
            Ok(b) => b,
            Err(_) => return jnull(),
        };
        if bytes.len() % HASH_SIZE != 0 {
            return jnull();
        }
        let count = bytes.len() / HASH_SIZE;
        let mut out = [0u8; HASH_SIZE];
        let ptr = if count == 0 {
            std::ptr::null()
        } else {
            bytes.as_ptr()
        };
        if filecore::fc_merkle_root(ptr, count, out.as_mut_ptr()) != filecore::FC_OK {
            return jnull();
        }
        env.byte_array_from_slice(&out)
            .map(|a| a.into_raw())
            .unwrap_or_else(|_| jnull())
    }))
    .unwrap_or_else(|_| jnull())
}

/// 一趟算出文件描述，打包返回 `[file_hash(32) || merkle_root(32) || leaves(n*32)]`。
/// 失败（IO/参数/文件消失）返回 null，由 Kotlin 回退纯 Java 路径。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_FileCore_nativeDescribeFile(
    mut env: JNIEnv,
    _this: JObject,
    path: JString,
    chunk_size: jlong,
) -> jbyteArray {
    catch_unwind(AssertUnwindSafe(|| {
        if chunk_size <= 0 {
            return jnull();
        }
        let cs = chunk_size as u64;
        let path_str: String = match env.get_string(&path) {
            Ok(s) => s.into(),
            Err(_) => return jnull(),
        };
        let c_path = match CString::new(path_str.clone()) {
            Ok(c) => c,
            Err(_) => return jnull(),
        };

        // 容量按当前文件大小估算；描述计算与 stat 之间文件变大则扩容重试一次
        let mut cap = match std::fs::metadata(&path_str) {
            Ok(m) => (m.len().div_ceil(cs) as usize) + 8,
            Err(_) => return jnull(),
        };
        for _ in 0..2 {
            let mut leaves = vec![0u8; cap * HASH_SIZE];
            let mut root = [0u8; HASH_SIZE];
            let mut file_hash = [0u8; HASH_SIZE];
            let n = filecore::fc_describe(
                c_path.as_ptr(),
                cs,
                leaves.as_mut_ptr(),
                cap,
                root.as_mut_ptr(),
                file_hash.as_mut_ptr(),
            );
            if n == filecore::FC_ERR_ARG as i64 {
                cap *= 2; // 文件比 stat 时更大：扩容重试
                continue;
            }
            if n < 0 {
                return jnull();
            }
            let count = n as usize;
            let mut packed = Vec::with_capacity(2 * HASH_SIZE + count * HASH_SIZE);
            packed.extend_from_slice(&file_hash);
            packed.extend_from_slice(&root);
            packed.extend_from_slice(&leaves[..count * HASH_SIZE]);
            return env
                .byte_array_from_slice(&packed)
                .map(|a| a.into_raw())
                .unwrap_or_else(|_| jnull());
        }
        jnull()
    }))
    .unwrap_or_else(|_| jnull())
}
