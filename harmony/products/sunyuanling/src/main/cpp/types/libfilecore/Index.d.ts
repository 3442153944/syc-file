/**
 * libfilecore.so NAPI module TypeScript declarations.
 * 对应 cpp/napi_init.cpp 导出的函数。失败返回 null，调用方需回退纯 ArkTS 实现。
 */

export interface FileCoreNative {
  /** ABI 版本号（与服务端 fc_abi_version 一致），≥3 表示含 describeFile。 */
  abiVersion(): number;

  /**
   * 计算一段数据的 blake3 哈希（32 字节）。失败返回 null。
   * 对标 file_lib::fc_hash_chunk。
   */
  hashChunk(data: Uint8Array): Uint8Array | null;

  /**
   * 由拼接叶子（n*32 字节）构造 Merkle 树根（32 字节）。失败返回 null。
   * 对标 file_lib::fc_merkle_root。
   */
  merkleRoot(leaves: Uint8Array): Uint8Array | null;

  /**
   * 一趟算出文件的上传描述信息。
   * 返回 packed [file_hash(32) || merkle_root(32) || leaves(n*32)]，失败返回 null。
   * 对标 file_lib::fc_describe（mmap + rayon 并行）。
   */
  describeFile(path: string, chunkSize: number): Uint8Array | null;
}

declare const fileCoreNative: FileCoreNative;
export default fileCoreNative;
