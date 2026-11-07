/**
 * libfilecore.so NAPI module TypeScript declarations.
 * 对应 cpp/napi_init.cpp 导出的函数。实现统一来自 sys_core。
 */

export interface FileCoreNative {
  /** ABI 版本号（与服务端 fc_abi_version 一致），≥3 表示含 describeFile。 */
  abiVersion(): number;

  /**
   * 计算一段数据的 blake3 哈希（32 字节）。失败返回 null。
   * 对标 sys_core::fc_hash_chunk。
   */
  hashChunk(data: Uint8Array): Uint8Array | null;

  /**
   * 由拼接叶子（n*32 字节）构造 Merkle 树根（32 字节）。失败返回 null。
   * 对标 sys_core::fc_merkle_root。
   */
  merkleRoot(leaves: Uint8Array): Uint8Array | null;

  /**
   * 一趟算出文件的上传描述信息。
   * 返回 packed [file_hash(32) || merkle_root(32) || leaves(n*32)]，失败返回 null。
   * 对标 sys_core::fc_describe（mmap + rayon 并行）。
   */
  describeFile(path: string, chunkSize: number): Uint8Array | null;

  /** 创建 sys_core 上传任务；input 为 upload::bridge::Start JSON，失败返回 0。 */
  startUpload(input: string): number;

  /** 取出至多一批上传事件；返回 JSON 数组字符串，失败返回 null。 */
  pollUpload(id: number): string | null;

  /** 复制请求的二进制体给 ArkTS；失败或该请求无二进制体时返回 null。 */
  takeUploadBody(id: number, request: number): Uint8Array | null;

  /** 回复 {"response":原始信封} 或 {"error":"..."}，返回 C ABI 状态码。 */
  replyUpload(id: number, request: number, result: string): number;

  /** 取消并释放上传任务。 */
  cancelUpload(id: number): void;
}

declare const fileCoreNative: FileCoreNative;
export default fileCoreNative;
