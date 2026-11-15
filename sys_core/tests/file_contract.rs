use std::{ffi::CString, path::PathBuf};
use sys_core::{file, *};
struct Temp(PathBuf);
impl Temp {
    fn new() -> Self {
        Self(std::env::temp_dir().join(format!("sys-core-{}", uuid::Uuid::new_v4())))
    }
}
impl Drop for Temp {
    fn drop(&mut self) {
        let c = CString::new(self.0.to_str().unwrap()).unwrap();
        fc_evict(c.as_ptr());
        let _ = std::fs::remove_file(&self.0);
    }
}
#[test]
fn typed_describe_matches_c_abi_and_finalize_for_empty_odd_and_partial_chunks() {
    for size in [0, 1, 4, 5, 12, 13, 65539] {
        let temp = Temp::new();
        let data: Vec<u8> = (0..size).map(|i| (i % 251) as u8).collect();
        std::fs::write(&temp.0, &data).unwrap();
        let desc = file::describe(&temp.0, 4).unwrap();
        let c = CString::new(temp.0.to_str().unwrap()).unwrap();
        let mut leaves = vec![0u8; desc.chunk_count as usize * 32];
        let mut root = [0u8; 32];
        let mut hash = [0u8; 32];
        assert_eq!(
            fc_describe(
                c.as_ptr(),
                4,
                leaves.as_mut_ptr(),
                desc.chunk_count as usize,
                root.as_mut_ptr(),
                hash.as_mut_ptr()
            ),
            desc.chunk_count as i64
        );
        assert_eq!(desc.merkle_root_hex, hex::encode(root));
        assert_eq!(desc.file_hash_hex, hex::encode(hash));
        assert_eq!(
            desc.leaf_hashes_hex,
            leaves.chunks(32).map(hex::encode).collect::<Vec<_>>()
        );
        assert_eq!(
            file::file_blake3_hex(&temp.0).unwrap(),
            blake3::hash(&data).to_hex().to_string()
        );
        let mut final_hash = [0; 32];
        let mut bad = -2;
        assert_eq!(
            fc_finalize(
                c.as_ptr(),
                4,
                size as u64,
                leaves.as_ptr(),
                desc.chunk_count as usize,
                root.as_ptr(),
                final_hash.as_mut_ptr(),
                &mut bad
            ),
            FC_OK
        );
        assert_eq!(final_hash, hash);
        assert_eq!(bad, -1);
    }
}
#[test]
fn full_chunk_read_rejects_changed_size_and_invalid_ranges() {
    let temp = Temp::new();
    std::fs::write(&temp.0, b"abcdef").unwrap();
    assert_eq!(file::read_chunk(&temp.0, 1, 4, 6).unwrap(), b"ef");
    for (index, chunk, total) in [
        (-1, 4, 6),
        (2, 4, 6),
        (0, 0, 6),
        (0, 4, -1),
        (0, 4, 7),
        (0, 4, 5),
    ] {
        assert!(file::read_chunk(&temp.0, index, chunk, total).is_err());
    }
    assert!(file::describe(&temp.0, 0).is_err());
    assert!(file::describe(&temp.0, (1 << 30) + 1).is_err());
}
#[test]
fn c_receiver_accepts_out_of_order_chunks_from_shared_file_reader() {
    let source = Temp::new();
    let dest = Temp::new();
    std::fs::write(&source.0, b"abcdefghijklmn").unwrap();
    let desc = file::describe(&source.0, 4).unwrap();
    let path = CString::new(dest.0.to_str().unwrap()).unwrap();
    assert_eq!(fc_preallocate(path.as_ptr(), 14), FC_OK);
    for index in [3, 1, 0, 2] {
        let data = file::read_chunk(&source.0, index, 4, 14).unwrap();
        let leaf = hex::decode(&desc.leaf_hashes_hex[index as usize]).unwrap();
        assert_eq!(
            fc_chunk_write(
                path.as_ptr(),
                index as u64 * 4,
                data.as_ptr(),
                data.len(),
                leaf.as_ptr()
            ),
            FC_OK
        );
    }
    fc_evict(path.as_ptr());
    assert_eq!(std::fs::read(&dest.0).unwrap(), b"abcdefghijklmn");
}
