//! Portable file operations shared by Rust hosts and C/JNI bindings.
use std::{
    fs::File,
    io::{self, Read},
    path::Path,
};
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct Description {
    pub total_size: i64,
    pub chunk_size: i64,
    pub chunk_count: i32,
    pub leaf_hashes_hex: Vec<String>,
    pub merkle_root_hex: String,
    pub file_hash_hex: String,
}
/// The source must remain unchanged while describing and uploading.
pub fn describe(path: &Path, chunk_size: usize) -> io::Result<Description> {
    if chunk_size == 0 || chunk_size as u64 > crate::file_ops::MAX_CHUNK_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid chunk size",
        ));
    }
    let file = File::open(path)?;
    let size = file.metadata()?.len();
    let total_size = i64::try_from(size)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "file too large"))?;
    let (leaves, hash) = if size == 0 {
        (Vec::new(), *blake3::hash(b"").as_bytes())
    } else {
        crate::file_ops::compute_leaves_and_file_hash(&file, chunk_size as u64)?
    };
    let count = i32::try_from(leaves.len())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "too many chunks"))?;
    if file.metadata()?.len() != size || leaves.len() as u64 != size.div_ceil(chunk_size as u64) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "file changed while describing",
        ));
    }
    Ok(Description {
        total_size,
        chunk_size: chunk_size as i64,
        chunk_count: count,
        merkle_root_hex: hex::encode(crate::file_ops::merkle_root(&leaves)),
        file_hash_hex: hex::encode(hash),
        leaf_hashes_hex: leaves.iter().map(hex::encode).collect(),
    })
}
pub fn file_blake3_hex(path: &Path) -> io::Result<String> {
    let mut file = File::open(path)?;
    let mut hasher = blake3::Hasher::new();
    let mut buf = [0; 64 * 1024];
    loop {
        let n = file.read(&mut buf)?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(hasher.finalize().to_hex().to_string())
}
/// A short read must fail instead of uploading trailing zero padding.
pub fn read_chunk(
    path: &Path,
    index: i32,
    chunk_size: usize,
    total_size: i64,
) -> io::Result<Vec<u8>> {
    if index < 0
        || chunk_size == 0
        || chunk_size as u64 > crate::file_ops::MAX_CHUNK_SIZE
        || total_size < 0
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid chunk range",
        ));
    }
    let offset = (index as u64)
        .checked_mul(chunk_size as u64)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "chunk offset overflow"))?;
    if offset > total_size as u64 || (offset == total_size as u64 && total_size != 0) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "chunk out of bounds",
        ));
    }
    let len = (total_size as u64 - offset).min(chunk_size as u64) as usize;
    let mut file = File::open(path)?;
    if file.metadata()?.len() != total_size as u64 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "file size changed during upload",
        ));
    }
    use std::io::{Seek, SeekFrom};
    file.seek(SeekFrom::Start(offset))?;
    let mut data = vec![0; len];
    file.read_exact(&mut data)?;
    Ok(data)
}
