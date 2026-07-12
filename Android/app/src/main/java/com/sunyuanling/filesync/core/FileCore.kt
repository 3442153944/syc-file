// core/FileCore.kt
// filecore Rust 内核的 Kotlin 门面（JNI）。
//
// 底层 .so 由 Android/filecore_jni 构建（cargo-ndk，产物在 app/src/main/jniLibs/）,
// 与服务端 file_lib 共用同一实现——叶子/树根/整文件哈希与 fc_finalize 逐字节一致，
// 且 mmap+rayon 多核并行，大文件描述计算远快于纯 Java blake3。
//
// 可用性：.so 缺失（未跑 NDK 构建/不支持的 ABI）或加载失败时 available=false，
// 调用方（ChunkedUploader/SyncEngine）自动回退 Blake3Util 纯 Java 路径，仅性能差。
package com.sunyuanling.filesync.core

import android.util.Log
import com.sunyuanling.filesync.api.file.ChunkedUploader
import com.sunyuanling.filesync.util.Blake3Util
import java.io.File

object FileCore {

    private const val TAG = "FileCore"
    private const val HASH_SIZE = 32

    /** 原生库是否可用（加载成功且 ABI ≥ 3，即含 fc_describe）。 */
    val available: Boolean = try {
        System.loadLibrary("filecore_jni")
        val v = nativeAbiVersion()
        Log.i(TAG, "filecore 原生库已加载 (ABI v$v)")
        v >= 3
    } catch (t: Throwable) {
        Log.w(TAG, "filecore 原生库不可用，回退纯 Java blake3: ${t.message}")
        false
    }

    // ---- JNI（实现见 Android/filecore_jni/src/lib.rs，失败返回 null）----
    private external fun nativeAbiVersion(): Int
    private external fun nativeHashChunk(data: ByteArray): ByteArray?
    private external fun nativeMerkleRoot(leaves: ByteArray): ByteArray?
    private external fun nativeDescribeFile(path: String, chunkSize: Long): ByteArray?

    /** 一段数据的 blake3（32B）；原生不可用/失败返回 null，调用方回退 Blake3Util.hash。 */
    fun hashChunk(data: ByteArray): ByteArray? =
        if (available) runCatching { nativeHashChunk(data) }.getOrNull() else null

    /** 由拼接叶子（n*32B）构造 Merkle 树根；失败返回 null。 */
    fun merkleRoot(packedLeaves: ByteArray): ByteArray? =
        if (available) runCatching { nativeMerkleRoot(packedLeaves) }.getOrNull() else null

    /**
     * 一趟算出文件的上传描述（叶子/树根/整文件哈希），mmap+rayon 原生并行。
     * 返回 null 表示原生不可用或计算失败（含描述与当前文件大小不自洽——文件正被写入），
     * 调用方回退 [ChunkedUploader] 的纯 Java 单趟流式实现。
     */
    fun describe(file: File, chunkSize: Int): ChunkedUploader.Description? {
        if (!available || chunkSize <= 0) return null
        val packed = runCatching {
            nativeDescribeFile(file.absolutePath, chunkSize.toLong())
        }.getOrNull() ?: return null
        if (packed.size < 2 * HASH_SIZE || (packed.size - 2 * HASH_SIZE) % HASH_SIZE != 0) return null

        val fileHash = packed.copyOfRange(0, HASH_SIZE)
        val root = packed.copyOfRange(HASH_SIZE, 2 * HASH_SIZE)
        val count = (packed.size - 2 * HASH_SIZE) / HASH_SIZE

        // 自洽性检查：叶子数须与当前文件大小匹配（计算与 stat 之间文件可能在变）
        val total = file.length()
        val expect = if (total == 0L) 0 else ((total + chunkSize - 1) / chunkSize).toInt()
        if (count != expect) {
            Log.w(TAG, "describe 叶子数与文件大小不自洽（文件正被写入？），回退 Java 路径: ${file.name}")
            return null
        }

        val leafHex = ArrayList<String>(count)
        for (i in 0 until count) {
            val off = 2 * HASH_SIZE + i * HASH_SIZE
            leafHex.add(Blake3Util.toHex(packed.copyOfRange(off, off + HASH_SIZE)))
        }
        return ChunkedUploader.Description(
            totalSize = total,
            chunkSize = chunkSize,
            chunkCount = count,
            leafHashesHex = leafHex,
            merkleRootHex = Blake3Util.toHex(root),
            fileHashHex = Blake3Util.toHex(fileHash)
        )
    }

    /** 整文件 blake3 hex（同步基线/校验用）；原生不可用返回 null。 */
    fun fileHashHex(file: File, chunkSize: Int = ChunkedUploader.DEFAULT_CHUNK_SIZE): String? =
        describe(file, chunkSize)?.fileHashHex
}
