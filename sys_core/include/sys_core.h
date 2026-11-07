/*
 * sys_core.h —— 文件同步上传 Rust 核心库的 C ABI 声明。
 * 供 Go 通过 cgo 静态链接 libsync_core.a 调用。
 *
 * 约定：所有哈希均为 32 字节 blake3；返回码见下方 FC_* 常量，负数为错误。
 */
#ifndef SYNC_CORE_H
#define SYNC_CORE_H

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
/* v5 新增：服务端分片接收器系列错误码 */
#define FC_ERR_NOT_FOUND -6 /* receiver 不存在 */
#define FC_ERR_ABANDONED -7 /* 该 (index, attempt_id) 已被放弃 */

/* ABI 版本，链接自检用。v2：新增 fc_evict 与 FC_ERR_SIZE_MISMATCH；v3：新增 fc_describe；
 * v4：新增 fc_sys_snapshot/fc_free_string；v5：新增服务端接收器 fc_receiver_* 系列 */
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

/*
 * v4 新增：采一次进程 Top-N（按 cpu/mem/连接数加权评分排序）+ 端口/连接明细，
 * 序列化成 JSON 字符串写入 *out_json（NUL 结尾，UTF-8）。
 * 调用方读完 JSON 后【必须】调 fc_free_string 释放——Go 和 Rust 分配器不同，
 * 不能用 C.free() 直接释放这块内存。
 */
int32_t fc_sys_snapshot(uint32_t top_n, char **out_json);

/* 释放 fc_sys_snapshot / fc_upload_poll 返回的字符串。空指针安全。 */
void fc_free_string(char *s);

/* -------------------------------------------------------------------------
 * v5 新增：服务端分片接收器（VM 单 Go 进程、单实例部署设计，进程内状态）。
 * Go 侧只做 HTTP/鉴权/会话元数据，分片缓存、接收位图、abandoned 判断、
 * 批量定位写、最终校验全部在 Rust 内完成；热路径不访问 Redis。
 * 所有字符串要求 UTF-8，负数为 FC_ERR_*；全部入口 catch_unwind。
 * ------------------------------------------------------------------------- */

/* 单次 accept/flush 的结果（Rust #[repr(C)]，字段顺序固定） */
typedef struct fc_accept_result {
    int64_t received;       /* 已接受分片数（accepted 计数） */
    int32_t complete;       /* 1 = accepted 已覆盖全部分片 */
    int32_t snapshot_ready; /* 1 = dirty 且距上次信号 >= 快照间隔（约 1s） */
    int32_t duplicate;      /* 1 = 该片此前已接受（幂等重复） */
    int32_t reserved;       /* 恒 0 */
} fc_accept_result;

/* 打开/注册会话接收器。
 * 返回 1 = 本次新建；0 = 已存在（参数一致，未覆盖）；负 = FC_ERR_*。
 * persisted_bitmap 可为 NULL/len=0（首次上传）；为 Redis 中持久化位图（重启恢复用）。
 * 只用该位图初始化 accepted+persisted；内存里仅 accepted（未 flush）的片永不进快照。
 * ttl_secs = 0 时用 24h 默认。 */
int32_t fc_receiver_open(const char *upload_id, const char *path,
                         uint64_t total_size, uint64_t chunk_size, uint64_t chunk_count,
                         const uint8_t *persisted_bitmap, size_t bitmap_len,
                         uint64_t ttl_secs);

/* 1 = 存在；0 = 不存在 */
int32_t fc_receiver_exists(const char *upload_id);

/* 接收一个分片（数据由 Rust 拷贝进 pending，调用方保留所有权）。
 * 校验顺序：receiver 存在 → index 边界 → 长度符合（末片=total-index*chunk）→
 *           attempt 是否 abandoned（attempt_id 可 NULL/空=不校验）→ 是否 duplicate。
 * 成功时填充 out（不可为 NULL）。duplicate=1 时 received/complete 照常填。 */
int32_t fc_receiver_accept(const char *upload_id, uint32_t index,
                           const uint8_t *data, size_t len,
                           const char *attempt_id, fc_accept_result *out);

/* 把 pending 按 index 排序、合并相邻片后批量定位写（不传 expected_leaf，不做提前 BLAKE3）。
 * 每组写成功后才置 persisted、移出 pending、扣 pending_bytes。out 可为 NULL。 */
int32_t fc_receiver_flush(const char *upload_id, fc_accept_result *out);

/* 导出位图到调用方缓冲区（cap 必须 >= ceil(chunk_count/8)，否则 FC_ERR_ARG）。
 * 返回写入字节数；不存在返回 FC_ERR_NOT_FOUND。字节序与 Redis SETBIT 一致（MSB-first）。 */
int64_t fc_receiver_bitmap_accepted(const char *upload_id, uint8_t *out, size_t cap);
int64_t fc_receiver_bitmap_persisted(const char *upload_id, uint8_t *out, size_t cap);

/* Go 成功 PersistBitmap 后调用：清 dirty。receiver 不存在返回 FC_ERR_NOT_FOUND。 */
int32_t fc_receiver_snapshot_ack(const char *upload_id);

/* flush 全部 pending → 复用 fc_finalize 做叶子/Merkle/整文件校验。
 * - FC_OK：receiver 已移除（Go 随后 fc_move）；out_file_hash32 写整文件哈希。
 * - FC_ERR_LEAF_MISMATCH：*out_bad_index=首个坏块，清该位，保留 receiver。
 * - 其它错误：保留 receiver；flush IO 失败返回 FC_ERR_IO。
 * receiver 不存在返回 FC_ERR_NOT_FOUND。expected_leaves/expected_root 语义同 fc_finalize。 */
int32_t fc_receiver_finalize(const char *upload_id,
                             const uint8_t *expected_leaves, size_t leaf_count,
                             const uint8_t *expected_root,
                             uint8_t *out_file_hash32, int64_t *out_bad_index);

/* 记录放弃的 (index, attempt_id)。receiver 不存在时进全局缓冲，open 时合并。
 * attempt_id 不可空；返回 FC_OK（幂等）。 */
int32_t fc_receiver_abandon(const char *upload_id, uint32_t index, const char *attempt_id);

/* 移除 receiver（不 flush、不写盘；幂等，不存在返回 FC_OK）。 */
int32_t fc_receiver_close(const char *upload_id);

/* 外部运行时上传桥（feature=upload）。所有决策和文件读取由 sys_core 执行，
 * 宿主只泵送事件、执行 HTTP 并取回二进制请求体。 */
int32_t fc_upload_bridge_version(void);
uint64_t fc_upload_start(const char *input_json);
char *fc_upload_poll(uint64_t id);
int32_t fc_upload_take_body(uint64_t id, uint64_t request,
                            uint8_t **out_data, size_t *out_len);
void fc_upload_body_free(uint8_t *data, size_t len);
int32_t fc_upload_reply(uint64_t id, uint64_t request, const char *result_json);
void fc_upload_cancel(uint64_t id);

#ifdef __cplusplus
}
#endif

#endif /* SYNC_CORE_H */
