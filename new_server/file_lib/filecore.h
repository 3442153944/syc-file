/*
 * filecore.h —— 文件同步上传 Rust 核心库的 C ABI 声明。
 * 供 Go 通过 cgo 静态链接 libfilecore.a 调用。
 *
 * 约定：所有哈希均为 32 字节 blake3；返回码见下方 FC_* 常量，负数为错误。
 */
#ifndef FILECORE_H
#define FILECORE_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

#define FC_OK 0
#define FC_ERR_IO -1
#define FC_ERR_ARG -2
#define FC_ERR_LEAF_MISMATCH -3
#define FC_ERR_ROOT_MISMATCH -4

/* ABI 版本，链接自检用 */
int32_t fc_abi_version(void);

/* 预分配临时文件到 total_size；已存在则只调整长度，不清空（支持续传复用） */
int32_t fc_preallocate(const char *path, uint64_t total_size);

/* 写单个分片：expected_leaf 非空则先做 blake3 早校验再按 offset 定位写 */
int32_t fc_chunk_write(const char *path, uint64_t offset,
                       const uint8_t *data, size_t len,
                       const uint8_t *expected_leaf);

/* 计算一段数据的 blake3 叶子哈希 -> out32(32B) */
int32_t fc_hash_chunk(const uint8_t *data, size_t len, uint8_t *out32);

/* 从 count 个叶子哈希(leaves 指向 count*32B)构造 Merkle 树根 -> out32(32B) */
int32_t fc_merkle_root(const uint8_t *leaves, size_t count, uint8_t *out32);

/*
 * 收齐后单趟校验落盘前置：
 *  - expected_leaves(可空, leaf_count 个 32B)：逐块比对，首个坏块经 out_bad_index 返回
 *  - expected_root(可空, 32B)：Merkle 树根比对
 *  - out_file_hash32(32B)：顺带算出的整文件 blake3(秒传/去重)
 *  - out_bad_index：无坏块置 -1
 */
int32_t fc_finalize(const char *path, uint64_t chunk_size, uint64_t total_size,
                    const uint8_t *expected_leaves, size_t leaf_count,
                    const uint8_t *expected_root,
                    uint8_t *out_file_hash32, int64_t *out_bad_index);

/* 原子落盘：mkdir -p 目标父目录后 rename，跨卷退化为 copy+remove */
int32_t fc_move(const char *src, const char *dst);

#ifdef __cplusplus
}
#endif

#endif /* FILECORE_H */
