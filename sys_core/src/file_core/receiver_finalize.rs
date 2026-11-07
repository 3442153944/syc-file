//! 服务端接收器的 complete 收尾：增量快路径 + 全量回退路径。
//!
//! 正常上传（绝大多数情况）走**增量快路径**：accept 时已算好每片叶子、收片过程中已按
//! 文件序喂完整文件 blake3（乱序片从页缓存回读补齐），这里只做「stat 长度 → O(片数)
//! 叶子比对 → Merkle 根比对 → hasher.finalize()」，不重读/重算整文件，消除 complete 的
//! CPU 峰值（465MB 从 ~0.66 CPU 秒 × 多核降到毫秒级）。
//!
//! 只有重启/快传恢复（open 带 persisted 位图）、chunk 数过大、叶子失配后、增量状态
//! 不可用等异常场景才回退旧的全量 `fc_finalize` 路径（语义完全一致，只是多一次整文件读）。
//!
//! 独立成文件便于控制单文件规模；`Receiver` 字段由 receiver.rs 以 crate 内可见性提供。

use std::sync::Arc;

use super::file_ops::{
    fc_finalize, merkle_root, FC_ERR_ARG, FC_ERR_LEAF_MISMATCH, FC_ERR_ROOT_MISMATCH,
    FC_ERR_SIZE_MISMATCH, FC_OK,
};
use super::receiver::{
    lock, lookup, maybe_collect_expired, registry, Receiver, ReceiverError, HASH_SIZE,
};
use super::receiver_observe::{record_finalize, FinalizeStats};

/// flush 全部 pending 后校验并移除 receiver（Go 随后 fc_move）。
///
/// 正常路径（增量快路径，O(片数)）不重读/重算整文件；异常/恢复场景自动回退全量
/// `fc_finalize`，错误语义与改造前完全一致。
///
/// - `Ok(hash)`：receiver 已从全局表移除。
/// - `Err(LeafMismatch)`：清该 index 的 accepted/persisted 位（received--、dirty=true），
///   保留 receiver，客户端补传该片后可再次 finalize。
/// - 其它错误：保留 receiver。
///
/// `expected_leaves` 为 N 个 32B 连续哈希或 None；`expected_root` 32B 或 None，
/// 语义与 `fc_finalize` 一致（空 leaves 等价 None）。
pub fn finalize(
    upload_id: &str,
    expected_leaves: Option<&[u8]>,
    expected_root: Option<&[u8]>,
) -> Result<[u8; HASH_SIZE], ReceiverError> {
    let expected_leaves = expected_leaves.filter(|bytes| !bytes.is_empty());
    let leaf_count = match expected_leaves {
        None => 0,
        Some(bytes) => {
            if bytes.len() % HASH_SIZE != 0 || bytes.len() / HASH_SIZE > u32::MAX as usize {
                return Err(ReceiverError::Arg);
            }
            bytes.len() / HASH_SIZE
        }
    };
    if let Some(root) = expected_root {
        if root.len() != HASH_SIZE {
            return Err(ReceiverError::Arg);
        }
    }
    maybe_collect_expired();
    let arc = lookup(upload_id)?;

    // 会话锁内完成校验 + 状态清理；IO 期间只持该会话的锁，其它会话并行。
    // write-through 没有待刷数据：只需把增量 hasher 尽量推进到连续前缀（通常已推进完）。
    let mut receiver = lock(&arc);
    if receiver.closed {
        return Err(ReceiverError::NotFound);
    }
    receiver.last_activity = std::time::Instant::now();
    receiver.advance_hasher(None);
    let (result, stats) = receiver.finalize_locked(expected_leaves, leaf_count, expected_root);

    if result.is_ok() {
        // 先置 closed，再释放会话锁、从全局表移除；stale Arc 的后续调用得到 NotFound。
        receiver.closed = true;
        drop(receiver);
        let mut map = lock(registry());
        if map.get(upload_id).is_some_and(|cur| Arc::ptr_eq(cur, &arc)) {
            map.remove(upload_id);
        }
    }
    record_finalize(upload_id, stats);
    result
}

impl Receiver {
    /// 选择增量快路径或全量回退，并生成本次 finalize 的路径统计。
    fn finalize_locked(
        &mut self,
        expected_leaves: Option<&[u8]>,
        leaf_count: usize,
        expected_root: Option<&[u8]>,
    ) -> (Result<[u8; HASH_SIZE], ReceiverError>, FinalizeStats) {
        let fast = self.inc.ready(self.chunk_count);
        let (result, file_read_bytes) = if fast {
            // 快路径只 stat 一次，不读内容
            (
                self.finalize_incremental(expected_leaves, leaf_count, expected_root),
                0,
            )
        } else {
            let result = self.finalize_full(expected_leaves, leaf_count, expected_root);
            // fc_finalize 先 stat：size mismatch 直接返回不读盘；成功/叶子/根失配会整读。
            let read = match &result {
                Err(ReceiverError::SizeMismatch)
                | Err(ReceiverError::Io)
                | Err(ReceiverError::Arg) => 0,
                _ => self.total_size,
            };
            (result, read)
        };
        let stats = FinalizeStats {
            incremental: fast,
            file_read_bytes,
            hashed_bytes: if fast { self.stats.hashed_bytes } else { 0 },
            fallback_after: !self.inc.enabled(),
        };
        (result, stats)
    }

    /// 增量快路径：stat 长度 → O(片数) 叶子比对 → Merkle 根比对 → 顺序 hasher 结果。
    /// 全程不重读整文件（只有一次 metadata 调用）。
    fn finalize_incremental(
        &mut self,
        expected_leaves: Option<&[u8]>,
        leaf_count: usize,
        expected_root: Option<&[u8]>,
    ) -> Result<[u8; HASH_SIZE], ReceiverError> {
        // 与 fc_finalize 一致：先做磁盘长度早校验
        match std::fs::metadata(&self.path) {
            Ok(meta) if meta.len() == self.total_size => {}
            Ok(_) => return Err(ReceiverError::SizeMismatch),
            Err(_) => return Err(ReceiverError::Io),
        }
        if let Some(expected) = expected_leaves {
            if leaf_count != self.chunk_count as usize {
                return Err(ReceiverError::SizeMismatch);
            }
            for index in 0..self.chunk_count {
                let Some(digest) = self.inc.leaf(index) else {
                    // ready() 已保证叶子齐全；走到这里说明状态被破坏，按 IO 错误保守处理
                    return Err(ReceiverError::Io);
                };
                let start = index as usize * HASH_SIZE;
                if digest[..] != expected[start..start + HASH_SIZE] {
                    self.clear_bad_chunk(index);
                    // hasher 里已经包含该片旧内容、无法回滚：后续 finalize 一律走全量路径
                    self.inc.fallback();
                    return Err(ReceiverError::LeafMismatch {
                        bad_index: index as i64,
                    });
                }
            }
        }
        if let Some(root) = expected_root {
            if merkle_root(&self.inc.leaves())[..] != root[..] {
                return Err(ReceiverError::RootMismatch);
            }
        }
        Ok(self.inc.finalize_hash())
    }

    /// 全量回退路径：与改造前完全一致，调 `file_ops::fc_finalize` 重读整文件。
    /// 这里的 LEAF_MISMATCH 不强制再置 fallback：若增量状态本仍有效，补传会覆盖坏片，
    /// 但 hasher 已包含坏片字节无法回滚——快路径叶子失配时已主动 fallback，全量路径下
    /// 增量状态仍在时补传后由「重写已哈希区间」场景保守处理。
    fn finalize_full(
        &mut self,
        expected_leaves: Option<&[u8]>,
        leaf_count: usize,
        expected_root: Option<&[u8]>,
    ) -> Result<[u8; HASH_SIZE], ReceiverError> {
        let path_str = match self.path.to_str() {
            Some(s) => s,
            None => return Err(ReceiverError::Arg),
        };
        let c_path = match std::ffi::CString::new(path_str) {
            Ok(c) => c,
            Err(_) => return Err(ReceiverError::Arg),
        };
        let mut out_hash = [0u8; HASH_SIZE];
        let mut bad_index: i64 = -1;
        let code = fc_finalize(
            c_path.as_ptr(),
            self.chunk_size,
            self.total_size,
            expected_leaves.map_or(std::ptr::null(), |b| b.as_ptr()),
            leaf_count,
            expected_root.map_or(std::ptr::null(), |b| b.as_ptr()),
            out_hash.as_mut_ptr(),
            &mut bad_index,
        );
        match code {
            FC_OK => Ok(out_hash),
            FC_ERR_LEAF_MISMATCH => {
                if bad_index >= 0 && (bad_index as u64) < self.chunk_count as u64 {
                    self.clear_bad_chunk(bad_index as u32);
                }
                Err(ReceiverError::LeafMismatch { bad_index })
            }
            FC_ERR_ROOT_MISMATCH => Err(ReceiverError::RootMismatch),
            FC_ERR_SIZE_MISMATCH => Err(ReceiverError::SizeMismatch),
            FC_ERR_ARG => Err(ReceiverError::Arg),
            _ => Err(ReceiverError::Io),
        }
    }

    /// 坏块清理（契约不变式 5）：清该 index 的 accepted/persisted、received--、dirty=true，
    /// 并作废该片叶子（补传后重新计算）。
    fn clear_bad_chunk(&mut self, index: u32) {
        if self.accepted.get(index) {
            self.received -= 1;
        }
        self.accepted.clear(index);
        self.persisted.clear(index);
        // persisted 位清了：需要下一次快照把「缺片」也持久化
        self.dirty = true;
        self.inc.clear_digest(index);
    }
}
