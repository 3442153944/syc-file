// util/Blake3Util.kt
// BLAKE3 封装：分片叶子哈希、整文件流式哈希、Merkle 树根。
// 底层用 io.github.rctcwyvrn:blake3（纯 Java）。树的构造规则必须与服务端 file_lib（fc_merkle_root）
// 逐字节一致，否则两端算出的树根对不上，会触发整份重传。
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

    /**
     * Merkle 树根：叶子两两合并 parent = blake3(left ‖ right)，奇数节点原样进位到上层。
     * 空 → blake3("")；单叶子 → 该叶子本身。规则同 file_lib/src/lib.rs 的 merkle_root。
     */
    fun merkleRoot(leaves: List<ByteArray>): ByteArray {
        if (leaves.isEmpty()) return hash(ByteArray(0))
        var level = leaves
        while (level.size > 1) {
            val next = ArrayList<ByteArray>((level.size + 1) / 2)
            var i = 0
            while (i < level.size) {
                if (i + 1 < level.size) {
                    val h = Blake3.newInstance()
                    h.update(level[i])
                    h.update(level[i + 1])
                    next.add(h.digest())
                    i += 2
                } else {
                    next.add(level[i]) // 奇数节点进位
                    i += 1
                }
            }
            level = next
        }
        return level[0]
    }
}
