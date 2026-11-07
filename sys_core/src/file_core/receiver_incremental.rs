//! 接收器的增量校验状态：收片时算叶子、落盘时按文件序增量喂整文件 blake3。
//!
//! 目的（方向 A）：把服务端 complete 的 finalize 全量并行 BLAKE3 爆发，摊到收片/落盘
//! 过程中（单线程、每片约 2–3ms/4MiB），complete 只做 O(片数) 比对，整文件不重读。
//!
//! 状态机：
//! - `enabled = true`：accept 时为每个新片算 blake3 叶子进 `leaf_digests`；
//!   flush 每组写盘成功后把该组数据按偏移喂给顺序 `Hasher`。
//! - 乱序落盘：`hashed_bytes` 之前必须连续。若已写组的偏移 > `hashed_bytes`，
//!   数据暂存 `hash_wait`（offset → bytes），每当某组填上 `hashed_bytes` 后按序 drain。
//! - `hash_wait` 总量超过 `hash_wait_cap`（生产 64 MiB）→ 内存优先：置 fallback、
//!   丢弃全部哈希状态；finalize 回退到旧的全量 `fc_finalize` 路径。
//! - open 传入非空 Redis persisted 位图（崩溃恢复/快传秒传）、或 chunk 数超过
//!   [`MAX_INCREMENTAL_CHUNKS`]（leaf_digests 内存可控上限）时，直接以 disabled 构造。
//!
//! 正确性不变量：`hashed_bytes == total_size` 且 `hash_wait` 为空时，
//! `Hasher::finalize()` 必须等于对同一份落盘内容按序计算出的 blake3。

use std::collections::BTreeMap;

use blake3::Hasher;

/// `hash_wait` 暂存总量上限（生产）。超过即回退全量 finalize，换取确定性内存上限。
pub(crate) const HASH_WAIT_CAP: usize = 64 << 20; // 64 MiB

/// 启用增量校验的最大分片数：leaf_digests 每片 32B，1M 片约 32 MiB，
/// 超过则不值得为极端 chunk 数常驻内存，直接回退全量 finalize（行为不变，只多一次重读）。
pub(crate) const MAX_INCREMENTAL_CHUNKS: u32 = 1 << 20;

/// 单会话增量校验状态（与 Receiver 一一对应，随 receiver 创建/销毁）。
pub(crate) struct IncrementalState {
    enabled: bool,
    /// 每片 blake3 叶子；disabled 时为空。accept 时写入，重复片跳过。
    leaf_digests: Vec<Option<[u8; 32]>>,
    /// 顺序整文件 blake3（按文件字节序喂入）。
    hasher: Hasher,
    /// 已按文件序喂入 hasher 的字节数；喂入必须从 0 开始连续。
    hashed_bytes: u64,
    /// 乱序已落盘、但前面还有空洞的组：offset → 数据。
    hash_wait: BTreeMap<u64, Vec<u8>>,
    hash_wait_bytes: usize,
    hash_wait_cap: usize,
}

impl IncrementalState {
    /// `enabled` 由 open 决定：persisted 位图恢复 / chunk 数超限时为 false（直接全量路径）。
    pub(crate) fn new(chunk_count: u32, enabled: bool, hash_wait_cap: usize) -> Self {
        let enabled = enabled && chunk_count <= MAX_INCREMENTAL_CHUNKS;
        Self {
            enabled,
            leaf_digests: if enabled {
                vec![None; chunk_count as usize]
            } else {
                Vec::new()
            },
            hasher: Hasher::new(),
            hashed_bytes: 0,
            hash_wait: BTreeMap::new(),
            hash_wait_bytes: 0,
            hash_wait_cap,
        }
    }

    pub(crate) fn enabled(&self) -> bool {
        self.enabled
    }

    pub(crate) fn hashed_bytes(&self) -> u64 {
        self.hashed_bytes
    }

    pub(crate) fn wait_bytes(&self) -> usize {
        self.hash_wait_bytes
    }

    /// 已算出叶子的片数（观测/测试用）。
    pub(crate) fn digest_ready(&self) -> usize {
        self.leaf_digests.iter().filter(|d| d.is_some()).count()
    }

    /// accept 新片时记录叶子（单线程 blake3，约 2–3ms/4MiB，摊到传输期）。
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

    /// flush 的一组数据写盘成功后调用：按偏移喂顺序 hasher，必要时暂存并 drain。
    /// 只有写成功的数据才会进到这里（写失败组 pending 保留、下次重试成功后再喂）。
    pub(crate) fn on_written(&mut self, offset: u64, data: Vec<u8>) {
        if !self.enabled {
            return;
        }
        if offset == self.hashed_bytes {
            self.hasher.update(&data);
            self.hashed_bytes += data.len() as u64;
            self.drain_wait();
        } else if offset > self.hashed_bytes {
            self.hash_wait_bytes += data.len();
            if self.hash_wait_bytes > self.hash_wait_cap {
                // 内存优先：丢弃全部哈希状态，finalize 回退全量路径
                self.fallback();
            } else {
                self.hash_wait.insert(offset, data);
            }
        } else {
            // 已哈希区间被再次落盘（正常流程只会在回退后发生）：增量哈希不再可信
            self.fallback();
        }
    }

    /// 作废某片叶子（finalize 发现坏块后调用，补传时重新计算）。
    pub(crate) fn clear_digest(&mut self, index: u32) {
        if let Some(slot) = self.leaf_digests.get_mut(index as usize) {
            *slot = None;
        }
    }

    /// 把 `hashed_bytes` 处连续的暂存组按序喂入。
    fn drain_wait(&mut self) {
        loop {
            let Some(next) = self.hash_wait.keys().next().copied() else {
                return;
            };
            if next != self.hashed_bytes {
                return;
            }
            let Some(data) = self.hash_wait.remove(&next) else {
                return;
            };
            self.hash_wait_bytes -= data.len();
            self.hasher.update(&data);
            self.hashed_bytes += data.len() as u64;
        }
    }

    /// 单向下线：丢弃哈希状态（内存优先），此后 finalize 一律走全量 `fc_finalize`。
    pub(crate) fn fallback(&mut self) {
        self.enabled = false;
        self.leaf_digests = Vec::new();
        self.hash_wait.clear();
        self.hash_wait_bytes = 0;
        self.hashed_bytes = 0;
        self.hasher = Hasher::new();
    }

    /// finalize 快路径前置条件：增量状态有效、整文件按序喂完、叶子齐全。
    pub(crate) fn ready(&self, total_size: u64) -> bool {
        self.enabled
            && self.hashed_bytes == total_size
            && self.hash_wait.is_empty()
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

    fn feed_all(state: &mut IncrementalState, chunks: &[&[u8]], order: &[usize], chunk_size: u64) {
        for &i in order {
            state.on_written(i as u64 * chunk_size, chunks[i].to_vec());
        }
    }

    #[test]
    fn sequential_feeds_hash_matches_blake3_of_concatenation() {
        let chunks: Vec<Vec<u8>> = (0..5).map(|i| vec![i as u8; 7]).collect();
        let data: Vec<u8> = chunks.iter().flat_map(|c| c.iter().copied()).collect();
        let mut state = IncrementalState::new(5, true, 64 << 20);
        for (i, c) in chunks.iter().enumerate() {
            state.record_chunk(i as u32, c);
        }
        let refs: Vec<&[u8]> = chunks.iter().map(Vec::as_slice).collect();
        feed_all(&mut state, &refs, &[0, 1, 2, 3, 4], 7);
        assert!(state.ready(data.len() as u64));
        assert_eq!(state.finalize_hash(), *blake3::hash(&data).as_bytes());
        assert_eq!(state.digest_ready(), 5);
        assert_eq!(state.leaves().len(), 5);
    }

    #[test]
    fn out_of_order_writes_are_staged_and_drained_in_file_order() {
        let chunks: Vec<Vec<u8>> = (0..6).map(|i| vec![100 + i as u8; 5]).collect();
        let data: Vec<u8> = chunks.iter().flat_map(|c| c.iter().copied()).collect();
        let mut state = IncrementalState::new(6, true, 64 << 20);
        let refs: Vec<&[u8]> = chunks.iter().map(Vec::as_slice).collect();
        for (i, c) in chunks.iter().enumerate() {
            state.record_chunk(i as u32, c);
        }
        // 3,4 先写（前面有洞 → 暂存），再 0,1,2,5（0 补洞后 drain 3,4；5 直接接上）
        feed_all(&mut state, &refs, &[3, 4], 5);
        assert_eq!(state.hashed_bytes(), 0);
        assert_eq!(state.wait_bytes(), 10);
        feed_all(&mut state, &refs, &[0, 1, 2, 5], 5);
        assert_eq!(state.hashed_bytes(), data.len() as u64);
        assert_eq!(state.wait_bytes(), 0);
        assert!(state.ready(data.len() as u64));
        assert_eq!(state.finalize_hash(), *blake3::hash(&data).as_bytes());
    }

    #[test]
    fn wait_overflow_disables_incremental_and_drops_state() {
        let mut state = IncrementalState::new(4, true, 4);
        state.record_chunk(3, &[9u8; 5]);
        state.on_written(15, vec![9u8; 5]); // 偏移 15 > 0，暂存 5B > cap 4B → fallback
        assert!(!state.enabled());
        assert_eq!(state.hashed_bytes(), 0);
        assert_eq!(state.wait_bytes(), 0);
        assert!(!state.ready(20));
        // 回退后继续喂数据被忽略，不 panic / 不改变状态
        state.on_written(0, vec![0u8; 5]);
        assert_eq!(state.hashed_bytes(), 0);
        assert_eq!(state.digest_ready(), 0);
    }

    #[test]
    fn finalize_hash_is_stable_and_ready_requires_full_coverage() {
        let chunks = vec![vec![1u8; 3], vec![2u8; 3]];
        let data: Vec<u8> = chunks.iter().flat_map(|c| c.iter().copied()).collect();
        let mut state = IncrementalState::new(2, true, 64 << 20);
        state.record_chunk(0, &chunks[0]);
        state.record_chunk(1, &chunks[1]);
        state.on_written(0, chunks[0].clone());
        assert!(!state.ready(data.len() as u64), "只喂了一半不应 ready");
        state.on_written(3, chunks[1].clone());
        assert!(state.ready(data.len() as u64));
        assert_eq!(state.finalize_hash(), *blake3::hash(&data).as_bytes());
        // 重复 record 不覆盖已有叶子
        state.record_chunk(0, &[0u8; 3]);
        assert_eq!(state.leaf(0), Some(*blake3::hash(&chunks[0]).as_bytes()));
        assert_eq!(state.leaf(99), None);
    }

    #[test]
    fn excessive_chunk_count_constructs_disabled() {
        let state = IncrementalState::new(MAX_INCREMENTAL_CHUNKS + 1, true, 64 << 20);
        assert!(!state.enabled());
        assert_eq!(state.digest_ready(), 0);
    }
}
