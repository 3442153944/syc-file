//! sync_core 的 Android JNI 包装。
//!
//! 对应 Kotlin 侧 `com.sunyuanling.filesync.core.SyncCore`（object，external 成员函数），
//! 因此每个 JNI 函数第二个参数是实例 JObject。所有函数：
//! - 包 catch_unwind（panic 不得跨 JNI 展开），失败一律返回 null/负值，
//!   Kotlin 侧收到 null 即回退纯 Java blake3 实现（正确性不受影响，只慢）；
//! - 哈希/描述计算直接调用 sync_core 的 C ABI 导出（同一实现，与服务端 fc_finalize 逐字节一致）；
//! - upload_planner（多路径自适应上传决策）直接调 sync_core 的 typed API（Planner/Task/Report
//!   就在本 cdylib 依赖的 rlib 里，普通函数调用即可，与 nativeHashChunk 调 fc_hash_chunk
//!   同一模式），不走 fc_planner_* 的 C ABI 声明——避免 cdylib 符号剥离问题。
//!
//! 构建：`./build.ps1`（cargo-ndk，产物进 app/src/main/jniLibs/<abi>/libsync_core_jni.so）。

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
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncCore_nativeAbiVersion(
    _env: JNIEnv,
    _this: JObject,
) -> jint {
    catch_unwind(|| sync_core::fc_abi_version()).unwrap_or(-1)
}

/// 计算一段数据的 blake3（32 字节）。失败返回 null。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncCore_nativeHashChunk(
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
        if sync_core::fc_hash_chunk(bytes.as_ptr(), bytes.len(), out.as_mut_ptr()) != sync_core::FC_OK
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
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncCore_nativeMerkleRoot(
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
        if sync_core::fc_merkle_root(ptr, count, out.as_mut_ptr()) != sync_core::FC_OK {
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
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncCore_nativeDescribeFile(
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
            let n = sync_core::fc_describe(
                c_path.as_ptr(),
                cs,
                leaves.as_mut_ptr(),
                cap,
                root.as_mut_ptr(),
                file_hash.as_mut_ptr(),
            );
            if n == sync_core::FC_ERR_ARG as i64 {
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

// ---------------------------------------------------------------- upload_planner JNI
//
// 多路径自适应上传决策核心（sync_core::upload_planner）的 JNI 包装，Kotlin 侧对应
// com.sunyuanling.filesync.core.SyncPlanner。核心只做决策不发 HTTP：平台循环
// next → 执行 → report 直到 Done/Failed；Task/Report/PersistedState 一律 JSON 字符串
// 跨边界（与 fc_planner_* 的 C ABI 同构），句柄是 Box 指针存 jlong。

use sync_core::upload_planner as planner_core;

fn planner_ref(handle: jlong) -> Option<&'static mut planner_core::Planner> {
    if handle == 0 {
        return None;
    }
    Some(unsafe { &mut *(handle as *mut planner_core::Planner) })
}

fn jstring_of(env: &mut JNIEnv, s: &str) -> jni::sys::jstring {
    env.new_string(s)
        .map(|j| j.into_raw())
        .unwrap_or(std::ptr::null_mut())
}

/// 创建 Planner。desc_json = PlannerInput JSON（节点池 + 上次持久化样本）。失败返回 0。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerNew(
    mut env: JNIEnv,
    _this: JObject,
    desc_json: JString,
) -> jlong {
    catch_unwind(AssertUnwindSafe(|| {
        let s: String = match env.get_string(&desc_json) {
            Ok(v) => v.into(),
            Err(_) => return 0,
        };
        match serde_json::from_str::<planner_core::PlannerInput>(&s) {
            Ok(input) => match planner_core::Planner::new(input) {
                Ok(p) => Box::into_raw(Box::new(p)) as jlong,
                Err(_) => 0,
            },
            Err(_) => 0,
        }
    }))
    .unwrap_or(0)
}

/// 取下一个任务（Task JSON）。终态返回 {"type":"done"} / {"type":"failed","reason":..}；
/// 句柄无效/序列化失败也返回 failed 任务，绝不返回 null（Kotlin 循环靠它退出）。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerNext(
    mut env: JNIEnv,
    _this: JObject,
    handle: jlong,
) -> jni::sys::jstring {
    let r = catch_unwind(AssertUnwindSafe(|| {
        let task = match planner_ref(handle) {
            Some(p) => p.next_task(),
            None => planner_core::Task::Failed {
                reason: "无效句柄".into(),
            },
        };
        match serde_json::to_string(&task) {
            Ok(s) => jstring_of(&mut env, &s),
            Err(_) => jstring_of(&mut env, r#"{"type":"failed","reason":"serialize"}"#),
        }
    }));
    r.unwrap_or_else(|_| jstring_of(&mut env, r#"{"type":"failed","reason":"panic"}"#))
}

/// 回传执行结果（Report JSON）。返回 0 成功，负数失败。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerReport(
    mut env: JNIEnv,
    _this: JObject,
    handle: jlong,
    report_json: JString,
) -> jint {
    let r = catch_unwind(AssertUnwindSafe(|| -> Result<i32, i32> {
        let p = planner_ref(handle).ok_or(-1)?;
        let s: String = env.get_string(&report_json).map(|v| v.into()).map_err(|_| -2)?;
        let rep: planner_core::Report = serde_json::from_str(&s).map_err(|_| -3)?;
        p.report(rep);
        Ok(0)
    }));
    r.unwrap_or(Err(-1)).unwrap_or(-1)
}

/// 接入/重接会话：首个 upload 与 404 后的重新 init 都用它（与 begin_transfer 同效，
/// 选档在探测阶段已 finalize）。missing_json = [u32..]。返回 0 成功。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerResume(
    mut env: JNIEnv,
    _this: JObject,
    handle: jlong,
    upload_id: JString,
    missing_json: JString,
) -> jint {
    let r = catch_unwind(AssertUnwindSafe(|| -> Result<i32, i32> {
        let p = planner_ref(handle).ok_or(-1)?;
        let id: String = env.get_string(&upload_id).map(|v| v.into()).map_err(|_| -2)?;
        let s: String = env.get_string(&missing_json).map(|v| v.into()).map_err(|_| -2)?;
        let missing: Vec<u32> = serde_json::from_str(&s).map_err(|_| -3)?;
        p.resume(&id, &missing);
        Ok(0)
    }));
    r.unwrap_or(Err(-1)).unwrap_or(-1)
}

/// 探测选档完成后的分片大小（platform describe 要用）；0 = 未就绪/无效句柄。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerChunkSize(
    _env: JNIEnv,
    _this: JObject,
    handle: jlong,
) -> jlong {
    catch_unwind(AssertUnwindSafe(|| match planner_ref(handle) {
        Some(p) => p.planned_chunk_size().unwrap_or(0) as jlong,
        None => 0,
    }))
    .unwrap_or(0)
}

/// 每节点当前 AIMD 窗口（JSON 的 [["id",window],..]），窗口变化日志用；无效句柄返回 null。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerWindows(
    mut env: JNIEnv,
    _this: JObject,
    handle: jlong,
) -> jni::sys::jstring {
    let r = catch_unwind(AssertUnwindSafe(|| match planner_ref(handle) {
        Some(p) => match serde_json::to_string(&p.windows()) {
            Ok(s) => jstring_of(&mut env, &s),
            Err(_) => std::ptr::null_mut(),
        },
        None => std::ptr::null_mut(),
    }));
    r.unwrap_or(std::ptr::null_mut())
}

/// 导出持久化测速样本（PersistedState JSON），平台落盘下次冷启动复用；失败返回 null。
#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerExport(
    mut env: JNIEnv,
    _this: JObject,
    handle: jlong,
) -> jni::sys::jstring {
    let r = catch_unwind(AssertUnwindSafe(|| match planner_ref(handle) {
        Some(p) => match serde_json::to_string(&p.export_state()) {
            Ok(s) => jstring_of(&mut env, &s),
            Err(_) => std::ptr::null_mut(),
        },
        None => std::ptr::null_mut(),
    }));
    r.unwrap_or(std::ptr::null_mut())
}

#[no_mangle]
pub extern "system" fn Java_com_sunyuanling_filesync_core_SyncPlanner_nativePlannerFree(
    _env: JNIEnv,
    _this: JObject,
    handle: jlong,
) {
    if handle != 0 {
        let _ = catch_unwind(AssertUnwindSafe(|| unsafe {
            drop(Box::from_raw(handle as *mut planner_core::Planner));
        }));
    }
}
