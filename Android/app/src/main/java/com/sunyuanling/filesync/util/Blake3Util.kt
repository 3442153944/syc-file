// util/Blake3Util.kt
// BLAKE3 封装：内存字节与整文件流式哈希。
// 上传描述、分片叶子和 Merkle 统一由 sys_core 计算，这里不保留第二套树实现。
//
// rctcwyvrn API：
//   Blake3.newInstance() → 实例；update(bytes) 返回 void（不可链式）；digest() → ByteArray(32)
package com.sunyuanling.filesync.util

import io.github.rctcwyvrn.blake3.Blake3

object Blake3Util {

    /** 一次性算 32 字节 blake3。 */
    fun hash(data: ByteArray): ByteArray {
        val h = Blake3.newInstance()
        h.update(data)
        return h.digest()
    }

    /** 新建流式 hasher（整文件哈希用，避免把大文件读进内存）。用法：update() 多次后 digest()。 */
    fun newHasher(): Blake3 = Blake3.newInstance()

    fun toHex(b: ByteArray): String = b.joinToString("") { "%02x".format(it) }

}
