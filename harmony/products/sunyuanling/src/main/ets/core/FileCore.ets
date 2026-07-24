import fileCoreNative from 'libfilecore.so';
import { blake3Hash, blake3MerkleRoot, Blake3Hasher, bytesToHex } from '../util/Blake3Util';
import fs, { ReadOptions } from '@ohos.file.fs';
import { appConfig } from '../app/AppConfig';

/**
 * FileCore
 * 鸿蒙端 Rust 内核门面，对标 Android core/FileCore.kt + 桌面端 chunked_uploader::file_blake3_hex。
 *
 * 优先走原生（libfilecore.so，NAPI 包装 file_lib，mmap + rayon 并行，与服务端 fc_finalize 逐字节一致）；
 * .so 缺失或调用失败时回退纯 ArkTS Blake3Util（仅性能差，正确性等同）。
 *
 * 提供：
 *  - describe(path, chunkSize): Description   一趟算出 leaf_hashes / merkle_root / file_hash
 *  - hashChunk / merkleRoot（独立可用，但 describe 已统一）
 *  - fileHashHex(path, chunkSize): string | null   整文件 blake3（同步基线/校验用）
 */

const HASH_SIZE: number = 32;

/** ABI 是否可用（加载成功 + ABI ≥ 3 含 describeFile）。 */
export const nativeAvailable: boolean = checkNative();

function checkNative(): boolean {
  try {
    const v: number = fileCoreNative.abiVersion();
    if (v >= 3) {
      console.info(`[FileCore] native lib loaded (ABI v${v})`);
      return true;
    }
    console.warn(`[FileCore] native ABI version too old (v${v}), fallback to ArkTS blake3`);
    return false;
  } catch (e) {
    console.warn(`[FileCore] native lib unavailable, fallback to ArkTS blake3: ${(e as Error)?.message ?? JSON.stringify(e)}`);
    return false;
  }
}

/** 文件上传描述信息（对标 Android ChunkedUploader.Description）。 */
export interface Description {
  totalSize: number;
  chunkSize: number;
  chunkCount: number;
  leafHashesHex: string[];
  merkleRootHex: string;
  fileHashHex: string;
}

/** 单片 blake3 hex（32 字节 hex）。原生不可用返回 null。 */
export function hashChunkHex(data: Uint8Array): string | null {
  if (!nativeAvailable) return null;
  try {
    const out: Uint8Array | null = fileCoreNative.hashChunk(data);
    if (out === null) return null;
    return bytesToHex(out);
  } catch (e) {
    return null;
  }
}

/** 由拼接叶子（n*32B）构造 Merkle 树根 hex。原生不可用返回 null。 */
export function merkleRootHex(leaves: Uint8Array): string | null {
  if (!nativeAvailable) return null;
  try {
    const out: Uint8Array | null = fileCoreNative.merkleRoot(leaves);
    if (out === null) return null;
    return bytesToHex(out);
  } catch (e) {
    return null;
  }
}

/**
 * 一趟算出文件的上传描述信息。
 *  - 优先走原生 fc_describe（mmap + rayon 并行）
 *  - 原生不可用或失败（含描述与当前文件大小不自洽——文件正被写入），回退纯 ArkTS 单趟流式
 * 与 Android 实现完全对齐。
 */
export async function describe(filePath: string, chunkSize: number = appConfig.uploadChunkSize): Promise<Description> {
  const native: Description | null = await describeNative(filePath, chunkSize);
  if (native !== null) return native;
  return describeArkTS(filePath, chunkSize);
}

/** 原生路径：调 fc_describe。失败返回 null。 */
async function describeNative(filePath: string, chunkSize: number): Promise<Description | null> {
  if (!nativeAvailable || chunkSize <= 0) return null;
  let packed: Uint8Array | null = null;
  try {
    packed = fileCoreNative.describeFile(filePath, chunkSize);
  } catch (e) {
    return null;
  }
  if (packed === null) return null;
  if (packed.length < 2 * HASH_SIZE) return null;
  if ((packed.length - 2 * HASH_SIZE) % HASH_SIZE !== 0) return null;

  let fileStat: fs.Stat;
  try {
    fileStat = fs.statSync(filePath);
  } catch (e) {
    return null;
  }
  const total: number = fileStat.size;
  const count: number = (packed.length - 2 * HASH_SIZE) / HASH_SIZE;

  // 自洽性检查：叶子数须与当前文件大小匹配（计算与 stat 之间文件可能在变）
  const expect: number = total === 0 ? 0 : Math.ceil(total / chunkSize);
  if (count !== expect) {
    console.warn(`[FileCore] describe leaves count mismatch (file changing?), fallback ArkTS path`);
    return null;
  }

  const fileHash: Uint8Array = packed.subarray(0, HASH_SIZE);
  const root: Uint8Array = packed.subarray(HASH_SIZE, 2 * HASH_SIZE);
  const leafHex: string[] = [];
  for (let i = 0; i < count; i++) {
    const off: number = 2 * HASH_SIZE + i * HASH_SIZE;
    const leaf: Uint8Array = packed.subarray(off, off + HASH_SIZE);
    leafHex.push(bytesToHex(leaf));
  }
  return {
    totalSize: total,
    chunkSize: chunkSize,
    chunkCount: count,
    leafHashesHex: leafHex,
    merkleRootHex: bytesToHex(root),
    fileHashHex: bytesToHex(fileHash),
  };
}

/** 纯 ArkTS 回退：一趟顺序读，算每片 blake3 叶子 + 整文件流式 blake3 + Merkle 树根。 */
async function describeArkTS(filePath: string, chunkSize: number): Promise<Description> {
  const file: fs.File = await fs.open(filePath, fs.OpenMode.READ_ONLY);
  try {
    const fileStat: fs.Stat = await fs.stat(file.fd);
    const total: number = fileStat.size;
    const count: number = total === 0 ? 1 : Math.ceil(total / chunkSize);
    const lastChunkSize: number = total === 0 ? 0 : (total % chunkSize === 0 ? chunkSize : total % chunkSize);

    const leavesConcat: Uint8Array = new Uint8Array(count * HASH_SIZE);
    const fileHasher: Blake3Hasher = Blake3Hasher.new();
    const bufSize: number = Math.min(chunkSize, 64 * 1024);
    const buf: ArrayBuffer = new ArrayBuffer(bufSize);
    let filePtr: number = 0;
    for (let ci: number = 0; ci < count; ci++) {
      const chunkHasher: Blake3Hasher = Blake3Hasher.new();
      const thisChunkSize: number = ci < count - 1 ? chunkSize : lastChunkSize;
      let off: number = 0;
      while (off < thisChunkSize) {
        const want: number = Math.min(bufSize, thisChunkSize - off);
        // ArkTS fs.ReadOptions: offset 是「相对当前文件指针的偏移」（API 11 起，无 position）。
        // openSync 后指针=0；每次 readSync 读完按 length 自动前进。
        const readOpts: ReadOptions = { offset: 0, length: want };
        const readLen: number = await fs.read(file.fd, buf, readOpts);
        if (readLen <= 0) break;
        const slice: Uint8Array = new Uint8Array(buf, 0, readLen);
        chunkHasher.update(slice);
        fileHasher.update(slice);
        off += readLen;
        filePtr += readLen;
      }
      const leaf: Uint8Array = chunkHasher.finalize(HASH_SIZE);
      for (let j: number = 0; j < HASH_SIZE; j++) {
        leavesConcat[ci * HASH_SIZE + j] = leaf[j];
      }
    }
    const merkleRoot: Uint8Array = blake3MerkleRoot(leavesConcat);
    const fileHash: Uint8Array = fileHasher.finalize(HASH_SIZE);
    const leafHex: string[] = [];
    for (let i: number = 0; i < count; i++) {
      const leaf: Uint8Array = new Uint8Array(HASH_SIZE);
      for (let j: number = 0; j < HASH_SIZE; j++) {
        leaf[j] = leavesConcat[i * HASH_SIZE + j];
      }
      leafHex.push(bytesToHex(leaf));
    }
    return {
      totalSize: total,
      chunkSize: chunkSize,
      chunkCount: count,
      leafHashesHex: leafHex,
      merkleRootHex: bytesToHex(merkleRoot),
      fileHashHex: bytesToHex(fileHash),
    };
  } finally {
    try { await fs.close(file); } catch (e) { /* ignore */ }
  }
}

/** 整文件 blake3 hex（同步基线/校验用）；原生不可用走 ArkTS。 */
export async function fileHashHex(filePath: string, chunkSize: number = appConfig.uploadChunkSize): Promise<string> {
  return (await describe(filePath, chunkSize)).fileHashHex;
}

/**
 * 对内存字节直接算整文件 blake3 hex（纯 ArkTS，无需落盘 / 无需真实路径）。
 * 用于同步下载校验：授权目录是 docs:// URI，native 内核 mmap 不认 URI，
 * 而下载内容本就整块在内存，直接对 buffer 算最快，且与服务端 fc 整文件哈希逐字节一致。
 */
export function hashBufferHex(data: Uint8Array): string {
  const hasher: Blake3Hasher = Blake3Hasher.new();
  hasher.update(data);
  return bytesToHex(hasher.finalize(HASH_SIZE));
}
