//! 接收器的增量校验状态（write-through 收片）：accept 逐片算叶子，hasher 按文件序推进。
//!
//! 状态只有两类纯内存元数据（没有任何应用层数据缓冲）：
//! - `leaf_digests`：每片 accept 写盘成功后单线程算的 blake3 叶子（finalize O(片数) 比对）；
//! - `hasher` + `hashed_upto`：顺序整文件 blake3 及已推进到的分片索引。
//!
//! hasher 推进由 `Receiver` 负责：连续前缀已写盘时，当前分片直接用调用方数据喂入，
//! 其余已写盘分片用 `file_core::file::read_chunk` 从页缓存回读后喂入。
//! 增量不可用时（open 带 persisted 位图重启恢复、chunk 数超过
//! [`MAX_INCREMENTAL_CHUNKS`]、叶子失配后）单向 [`IncrementalState::fallback`]，
//! finalize 走全量 `fc_finalize`，语义不变。

use blake3::Hasher;

/// 启用增量校验的最大分片数：leaf_digests 每片 32B，256K 片约 8 MiB；再大直接回退全量
///（只多一次 finalize 重读，避免为极端 chunk 数常驻几十 MB）。
pub(crate) const MAX_INCREMENTAL_CHUNKS: u32 = 262_144;

/// 单会话增量校验状态（与 Receiver 一一对应，随 receiver 创建/销毁）。
pub(crate) struct IncrementalState {
    enabled: bool,
    /// 每片 blake3 叶子；disabled 时为空。accept 写盘成功后写入，重复片跳过。
    leaf_digests: Vec<Option<[u8; 32]>>,
    /// 顺序整文件 blake3（按文件字节序喂入）。
    hasher: Hasher,
    /// 已按文件序喂入 hasher 的分片数（下一个应喂的分片索引）。
    hashed_upto: u32,
}

impl IncrementalState {
    /// `enabled` 由 open 决定：persisted 位图恢复 / chunk 数超限时为 false（直接全量路径）。
    pub(crate) fn new(chunk_count: u32, enabled: bool) -> Self {
        let enabled = enabled && chunk_count <= MAX_INCREMENTAL_CHUNKS;
        Self {
            enabled,
            leaf_digests: if enabled {
                vec![None; chunk_count as usize]
            } else {
                Vec::new()
            },
            hasher: Hasher::new(),
            hashed_upto: 0,
        }
    }

    pub(crate) fn enabled(&self) -> bool {
        self.enabled
    }

    /// 已按文件序喂入 hasher 的分片数。
    pub(crate) fn hashed_upto(&self) -> u32 {
        self.hashed_upto
    }

    /// 已算出叶子的片数（观测/测试用）。
    pub(crate) fn digest_ready(&self) -> usize {
        self.leaf_digests.iter().filter(|d| d.is_some()).count()
    }

    /// accept 写盘成功后记录叶子（单线程 blake3，约 2–3ms/4MiB，摊到传输期）。
    /// disabled / 重复片 / 越界均安全跳过。
    pub(crate) fn record_chunk(&mut self, index: u32, data: &[u8]) {
        if !self.enabled {
            return;
        }
        if let Some(slot) = self.leaf_digests.get_mut(index as usize) {
            if slot.is_none() {
                *slot = Some(*blake3::hash(data).as_bytes());
            }
        }
    }

    /// 按文件序喂入一片数据并推进索引（只在连续前缀已写盘时调用）。
    pub(crate) fn update_hashed(&mut self, data: &[u8]) {
        if !self.enabled {
            return;
        }
        self.hasher.update(data);
        self.hashed_upto += 1;
    }

    /// 作废某片叶子（finalize 发现坏块后调用，补传时重新计算）。
    pub(crate) fn clear_digest(&mut self, index: u32) {
        if let Some(slot) = self.leaf_digests.get_mut(index as usize) {
            *slot = None;
        }
    }

    /// 单向下线：丢弃哈希状态，此后 finalize 一律走全量 `fc_finalize`。
    pub(crate) fn fallback(&mut self) {
        self.enabled = false;
        self.leaf_digests = Vec::new();
        self.hashed_upto = 0;
        self.hasher = Hasher::new();
    }

    /// finalize 快路径前置条件：增量状态有效、整文件按序喂完、叶子齐全。
    pub(crate) fn ready(&self, chunk_count: u32) -> bool {
        self.enabled
            && self.hashed_upto == chunk_count
            && !self.leaf_digests.is_empty()
            && self.leaf_digests.iter().all(Option::is_some)
    }

    /// 取某片叶子（快路径比对用）。
    pub(crate) fn leaf(&self, index: u32) -> Option<[u8; 32]> {
        self.leaf_digests.get(index as usize).copied().flatten()
    }

    /// 按 index 顺序取出全部叶子（仅在 [`ready`] 为 true 时完整）。
    pub(crate) fn leaves(&self) -> Vec<[u8; 32]> {
        self.leaf_digests.iter().filter_map(|d| *d).collect()
    }

    /// 顺序整文件 blake3 结果（仅在 [`ready`] 为 true 时代表整文件）。
    pub(crate) fn finalize_hash(&self) -> [u8; 32] {
        *self.hasher.finalize().as_bytes()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sequential_updates_hash_matches_blake3_of_concatenation() {
        let chunks: Vec<Vec<u8>> = (0..5).map(|i| vec![i as u8; 7]).collect();
        let data: Vec<u8> = chunks.iter().flat_map(|c| c.iter().copied()).collect();
        let mut state = IncrementalState::new(5, true);
        for (i, c) in chunks.iter().enumerate() {
            state.record_chunk(i as u32, c);
            state.update_hashed(c);
        }
        assert!(state.ready(5));
        assert_eq!(state.finalize_hash(), *blake3::hash(&data).as_bytes());
        assert_eq!(state.digest_ready(), 5);
        assert_eq!(state.leaves().len(), 5);
    }

    #[test]
    fn ready_requires_full_coverage_and_all_digests() {
        let chunks = vec![vec![1u8; 3], vec![2u8; 3]];
        let mut state = IncrementalState::new(2, true);
        state.record_chunk(0, &chunks[0]);
        state.update_hashed(&chunks[0]);
        assert!(!state.ready(2), "只喂了一半不应 ready");
        state.update_hashed(&chunks[1]);
        assert!(!state.ready(2), "缺叶子不应 ready");
        state.record_chunk(1, &chunks[1]);
        assert!(state.ready(2));
        // 重复 record 不覆盖已有叶子
        state.record_chunk(0, &[0u8; 3]);
        assert_eq!(state.leaf(0), Some(*blake3::hash(&chunks[0]).as_bytes()));
        assert_eq!(state.leaf(99), None);
    }

    #[test]
    fn fallback_discards_all_incremental_state() {
        let mut state = IncrementalState::new(4, true);
        state.record_chunk(0, &[7u8; 5]);
        state.update_hashed(&[7u8; 5]);
        state.fallback();
        assert!(!state.enabled());
        assert_eq!(state.hashed_upto(), 0);
        assert_eq!(state.digest_ready(), 0);
        assert!(!state.ready(4));
        // 回退后继续喂/记录被忽略，不 panic / 不改变状态
        state.update_hashed(&[0u8; 5]);
        assert_eq!(state.hashed_upto(), 0);
    }

    #[test]
    fn excessive_chunk_count_constructs_disabled() {
        let state = IncrementalState::new(MAX_INCREMENTAL_CHUNKS, true);
        assert!(state.enabled());
        let state = IncrementalState::new(MAX_INCREMENTAL_CHUNKS + 1, true);
        assert!(!state.enabled());
        assert_eq!(state.digest_ready(), 0);
    }
}
