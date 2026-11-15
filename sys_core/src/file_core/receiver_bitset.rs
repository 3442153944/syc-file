//! MSB-first 位图，receiver 的 accepted/persisted 共用。
//!
//! 字节序与 Redis `SETBIT` 一致：offset i 对应 byte `i/8` 的第 `7-(i%8)` 位，
//! 长度 `ceil(chunk_count/8)`。独立成文件便于控制单文件规模。

/// MSB-first 位图。
#[derive(Clone)]
pub(crate) struct Bitset {
    bytes: Vec<u8>,
    len: u32,
}

/// ceil(count/8)：位图字节数。
pub(crate) fn bitmap_len(count: u32) -> usize {
    (count as usize).div_ceil(8)
}

impl Bitset {
    pub(crate) fn new(len: u32) -> Self {
        Self {
            bytes: vec![0; bitmap_len(len)],
            len,
        }
    }

    /// 从外部字节恢复（Redis persisted 位图）。长度由调用方先校验；
    /// chunk_count 之外的尾部无效位统一清零，保证 count/popcount 正确。
    pub(crate) fn from_bytes(bytes: &[u8], len: u32) -> Self {
        let mut out = vec![0u8; bitmap_len(len)];
        let n = out.len().min(bytes.len());
        out[..n].copy_from_slice(&bytes[..n]);
        if let Some(last) = out.last_mut() {
            let used = (len % 8) as u32;
            if used != 0 {
                *last &= 0xFFu8 << (8 - used);
            }
        }
        Self { bytes: out, len }
    }

    pub(crate) fn get(&self, index: u32) -> bool {
        if index >= self.len {
            return false;
        }
        let byte = self.bytes[index as usize / 8];
        byte & (0x80 >> (index % 8)) != 0
    }

    pub(crate) fn set(&mut self, index: u32) {
        if index < self.len {
            self.bytes[index as usize / 8] |= 0x80 >> (index % 8);
        }
    }

    pub(crate) fn clear(&mut self, index: u32) {
        if index < self.len {
            self.bytes[index as usize / 8] &= !(0x80 >> (index % 8));
        }
    }

    pub(crate) fn count(&self) -> i64 {
        self.bytes.iter().map(|b| b.count_ones() as i64).sum()
    }

    pub(crate) fn as_bytes(&self) -> &[u8] {
        &self.bytes
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bitset_uses_msb_first_bit_order_like_redis_setbit() {
        let mut bits = Bitset::new(12);
        bits.set(0);
        bits.set(3);
        assert!(bits.get(0));
        assert!(!bits.get(1));
        assert!(bits.get(3));
        assert_eq!(bits.as_bytes(), &[0b1001_0000, 0x00]);
        bits.set(8);
        bits.set(11);
        assert_eq!(bits.as_bytes(), &[0b1001_0000, 0b1001_0000]);
        bits.clear(3);
        assert_eq!(bits.as_bytes(), &[0b1000_0000, 0b1001_0000]);
        assert_eq!(bits.count(), 3);
        // 越界不 panic、不置位
        bits.set(99);
        assert_eq!(bits.count(), 3);
    }

    #[test]
    fn bitset_from_bytes_masks_tail_bits_and_counts_only_valid_chunks() {
        let bits = Bitset::from_bytes(&[0xFF, 0xFF], 12);
        assert_eq!(bits.count(), 12);
        assert_eq!(bits.as_bytes(), &[0xFF, 0b1111_0000]);
    }
}
