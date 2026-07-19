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
/* v2 新增：文件实际尺寸/分片数与描述不符（v1 里混在 ROOT_MISMATCH） */
#define FC_ERR_SIZE_MISMATCH -5

/* ABI 版本，链接自检用。v2：新增 fc_evict 与 FC_ERR_SIZE_MISMATCH；v3：新增 fc_describe */
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

/*
 * 逐出并关闭某路径的进程内缓存写句柄。
 * 编排层在删除/移动临时文件前【必须】调用（会话超时、取消、清理僵尸文件），
 * 否则缓存可能残留指向已删文件的句柄，同路径新会话的写入会静默丢失。
 */
int32_t fc_evict(const char *path);

/*
 * v3 新增（客户端侧）：一趟算出文件的上传描述信息。
 * 叶子哈希写 out_leaves（容量 leaf_cap 个 32B），Merkle 树根写 out_root32，
 * 整文件 blake3 写 out_file_hash32。与服务端 fc_finalize 重算逐字节一致。
 * 返回 >=0 实际叶子数；负数为 FC_ERR_*（容量不足返回 FC_ERR_ARG，扩容重试）。
 * 空文件返回 0，root/file_hash = blake3("")。计算期间文件不得被并发写。
 */
int64_t fc_describe(const char *path, uint64_t chunk_size,
                    uint8_t *out_leaves, size_t leaf_cap,
                    uint8_t *out_root32, uint8_t *out_file_hash32);

#ifdef __cplusplus
}
#endif

#endif /* FILECORE_H */
