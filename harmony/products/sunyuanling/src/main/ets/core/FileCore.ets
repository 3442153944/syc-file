import fileCoreNative from 'libfilecore.so';
import fs from '@ohos.file.fs';
import { appConfig } from '../app/AppConfig';

/** HarmonyOS 的 sys_core NAPI 门面；文件描述和哈希不在 ArkTS 另存实现。 */
const HASH_SIZE: number = 32;

export const nativeAvailable: boolean = checkNative();

function checkNative(): boolean {
  try {
    const version: number = fileCoreNative.abiVersion();
    if (version >= 3) {
      console.info(`[FileCore] sys_core loaded (ABI v${version})`);
      return true;
    }
    console.error(`[FileCore] sys_core ABI too old: v${version}`);
    return false;
  } catch (e) {
    console.error(`[FileCore] sys_core unavailable: ${(e as Error)?.message ?? JSON.stringify(e)}`);
    return false;
  }
}

function bytesToHex(bytes: Uint8Array): string {
  let out: string = '';
  for (let i = 0; i < bytes.length; i++) {
    out += bytes[i].toString(16).padStart(2, '0');
  }
  return out;
}

function unavailable(): Error {
  return new Error('sys_core 文件内核不可用，请重新构建 HarmonyOS 原生库');
}

export interface Description {
  totalSize: number;
  chunkSize: number;
  chunkCount: number;
  leafHashesHex: string[];
  merkleRootHex: string;
  fileHashHex: string;
}

export function hashChunkHex(data: Uint8Array): string | null {
  if (!nativeAvailable) return null;
  try {
    const out: Uint8Array | null = fileCoreNative.hashChunk(data);
    return out === null ? null : bytesToHex(out);
  } catch (e) {
    return null;
  }
}

export function merkleRootHex(leaves: Uint8Array): string | null {
  if (!nativeAvailable) return null;
  try {
    const out: Uint8Array | null = fileCoreNative.merkleRoot(leaves);
    return out === null ? null : bytesToHex(out);
  } catch (e) {
    return null;
  }
}

export async function describe(
  filePath: string,
  chunkSize: number = appConfig.uploadChunkSize
): Promise<Description> {
  if (!nativeAvailable || chunkSize <= 0) throw unavailable();
  let packed: Uint8Array | null = null;
  try {
    packed = fileCoreNative.describeFile(filePath, chunkSize);
  } catch (e) {
    throw new Error(`sys_core 文件描述失败: ${(e as Error)?.message ?? JSON.stringify(e)}`);
  }
  if (packed === null || packed.length < 2 * HASH_SIZE ||
    (packed.length - 2 * HASH_SIZE) % HASH_SIZE !== 0) {
    throw new Error('sys_core 文件描述失败');
  }

  const fileStat: fs.Stat = fs.statSync(filePath);
  const total: number = fileStat.size;
  const count: number = (packed.length - 2 * HASH_SIZE) / HASH_SIZE;
  const expected: number = total === 0 ? 0 : Math.ceil(total / chunkSize);
  if (count !== expected) throw new Error('描述期间文件尺寸发生变化');

  const leafHex: string[] = [];
  for (let i = 0; i < count; i++) {
    const offset: number = 2 * HASH_SIZE + i * HASH_SIZE;
    leafHex.push(bytesToHex(packed.subarray(offset, offset + HASH_SIZE)));
  }
  return {
    totalSize: total,
    chunkSize: chunkSize,
    chunkCount: count,
    leafHashesHex: leafHex,
    merkleRootHex: bytesToHex(packed.subarray(HASH_SIZE, 2 * HASH_SIZE)),
    fileHashHex: bytesToHex(packed.subarray(0, HASH_SIZE)),
  };
}

export async function fileHashHex(
  filePath: string,
  chunkSize: number = appConfig.uploadChunkSize
): Promise<string> {
  return (await describe(filePath, chunkSize)).fileHashHex;
}

export function hashBufferHex(data: Uint8Array): string {
  const hash: string | null = hashChunkHex(data);
  if (hash === null) throw unavailable();
  return hash;
}
