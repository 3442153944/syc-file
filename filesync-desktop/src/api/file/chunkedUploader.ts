// api/file/chunkedUploader.ts
// Web 模式分片上传编排（与 Rust chunked_uploader.rs 逐字节一致）：
// describe（blake3 叶子+整文件哈希+Merkle 树根）→ init →（秒传则 complete）→ 乱序并发补传 → complete。
//
// 哈希规则（与 file_lib/README.md 一致）：叶子=blake3(分片字节)；parent=blake3(left‖right)；
// 奇数节点原样进位；空→blake3("")；单叶子→该叶子本身。
// 哈希实现用 @noble/hashes（纯 TS，无 WASM，双端逐字节一致）。
import { blake3 } from '@noble/hashes/blake3'
import { bytesToHex } from '@noble/hashes/utils'
import { httpPost, httpPostRawBytes } from '../http'
import type {
  UploadInitData,
  UploadChunkData,
  UploadCompleteData,
} from './fileTypes'

/** 默认分片 4MiB。 */
export const DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024

/** 默认并发分片数。 */
export const DEFAULT_CONCURRENCY = 3

/** 单片重传次数。 */
const CHUNK_RETRY = 3

/** 会话过期（404）：调用方需重新 init。 */
export class SessionGoneError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'SessionGoneError'
  }
}

/** 分片校验失败（422）：该片需重传。 */
export class ChunkVerifyError extends Error {
  index: number
  constructor(index: number, message: string) {
    super(message)
    this.name = 'ChunkVerifyError'
    this.index = index
  }
}

export interface UploadOptions {
  chunkSize: number
  concurrency: number
  /** 本机设备 id（Web 模式从 localStorage 取）。空则不传。 */
  deviceId: string
}

interface Description {
  totalSize: number
  chunkSize: number
  chunkCount: number
  leafHashesHex: string[]
  merkleRootHex: string
  fileHashHex: string
}

/** Merkle 树根：叶子两两合并 parent = blake3(left ‖ right)，奇数节点原样进位。
 *  空 → blake3("")；单叶子 → 该叶子本身。规则同 file_lib 的 merkle_root。 */
function merkleRootHex(leaves: Uint8Array[]): string {
  if (leaves.length === 0) return bytesToHex(blake3(new Uint8Array(0)))
  let level = leaves
  while (level.length > 1) {
    const next: Uint8Array[] = []
    for (let i = 0; i < level.length; ) {
      if (i + 1 < level.length) {
        const merged = new Uint8Array(level[i].length + level[i + 1].length)
        merged.set(level[i], 0)
        merged.set(level[i + 1], level[i].length)
        next.push(blake3(merged))
        i += 2
      } else {
        next.push(level[i]) // 奇数节点进位
        i += 1
      }
    }
    level = next
  }
  return bytesToHex(level[0])
}

/** 一趟顺序读 File：算每片 blake3 叶子 + 整文件流式 blake3 + Merkle 树根。 */
async function describe(file: File, chunkSize: number): Promise<Description> {
  const totalSize = file.size
  const chunkCount = totalSize === 0 ? 1 : Math.ceil(totalSize / chunkSize)
  const leaves: Uint8Array[] = []
  const leafHexes: string[] = []
  const hasher = blake3.create()

  for (let i = 0; i < chunkCount; i++) {
    const start = i * chunkSize
    const end = Math.min(start + chunkSize, totalSize)
    const buf = new Uint8Array(await file.slice(start, end).arrayBuffer())
    hasher.update(buf)
    const leaf = blake3(buf)
    leaves.push(leaf)
    leafHexes.push(bytesToHex(leaf))
  }

  return {
    totalSize,
    chunkSize,
    chunkCount,
    leafHashesHex: leafHexes,
    merkleRootHex: merkleRootHex(leaves),
    fileHashHex: bytesToHex(hasher.digest()),
  }
}

/**
 * 上传整份文件（Web 模式）。
 * @param onProgress (已发送字节, 总字节)
 * @returns 成功时的完成数据（秒传也算成功）
 */
export async function uploadChunked(
  file: File,
  remoteDir: string,
  options: UploadOptions = {
    chunkSize: DEFAULT_CHUNK_SIZE,
    concurrency: DEFAULT_CONCURRENCY,
    deviceId: '',
  },
  onProgress: (sent: number, total: number) => void = () => {},
): Promise<UploadCompleteData> {
  const desc = await describe(file, options.chunkSize)
  onProgress(0, desc.totalSize)

  // SessionGone 自动重新 init 一次
  let result: UploadCompleteData
  try {
    result = await runOnce(file, remoteDir, options, desc, onProgress)
  } catch (e) {
    if (e instanceof SessionGoneError) {
      result = await runOnce(file, remoteDir, options, desc, onProgress)
    } else {
      throw e
    }
  }
  return result
}

async function runOnce(
  file: File,
  remoteDir: string,
  options: UploadOptions,
  desc: Description,
  onProgress: (sent: number, total: number) => void,
): Promise<UploadCompleteData> {
  const init = await callInit(file.name, remoteDir, desc)

  if (init.instant) {
    onProgress(desc.totalSize, desc.totalSize)
    return completeUpload(init.upload_id, options.deviceId)
  }

  const missing: number[] =
    init.missing.length > 0 ? init.missing : Array.from({ length: desc.chunkCount }, (_, i) => i)
  let sent = (desc.chunkCount - missing.length) * desc.chunkSize
  onProgress(Math.min(sent, desc.totalSize), desc.totalSize)

  // 并发上传：n 路并发，任一失败抛出（含 SessionGone/ChunkVerify）
  await runConcurrent(missing, options.concurrency, async (index) => {
    const start = index * options.chunkSize
    const end = Math.min(start + options.chunkSize, desc.totalSize)
    const data = new Uint8Array(await file.slice(start, end).arrayBuffer())
    await uploadChunkWithRetry(init.upload_id, index, data)
    sent = Math.min(sent + data.length, desc.totalSize)
    onProgress(sent, desc.totalSize)
  })

  return completeUpload(init.upload_id, options.deviceId)
}

/** 简易并发池：最多 concurrency 个任务并行，任一抛错立即Reject整个 Promise。 */
async function runConcurrent<T>(
  items: T[],
  concurrency: number,
  fn: (item: T) => Promise<void>,
): Promise<void> {
  const limit = Math.max(1, concurrency)
  let cursor = 0
  let rejected = false
  const workers: Promise<void>[] = []
  const runWorker = async (): Promise<void> => {
    while (cursor < items.length) {
      if (rejected) return
      const idx = cursor++
      try {
        await fn(items[idx])
      } catch (e) {
        rejected = true
        throw e
      }
    }
  }
  for (let i = 0; i < Math.min(limit, items.length); i++) {
    workers.push(runWorker())
  }
  await Promise.all(workers)
}

async function uploadChunkWithRetry(uploadId: string, index: number, data: Uint8Array): Promise<void> {
  let lastErr: unknown = null
  for (let attempt = 0; attempt < CHUNK_RETRY; attempt++) {
    const env = await httpPostRawBytes<UploadChunkData>(
      '/file/upload/chunk',
      { upload_id: uploadId, index: String(index) },
      data,
    )
    if (env.code === 200) return
    if (env.code === 404) throw new SessionGoneError(env.message)
    if (env.code === 422) {
      lastErr = new ChunkVerifyError(index, env.message)
      continue
    }
    throw new Error(`分片 ${index} 失败: ${env.message}`)
  }
  throw lastErr ?? new Error(`分片 ${index} 上传失败`)
}

async function callInit(
  name: string,
  remoteDir: string,
  desc: Description,
): Promise<UploadInitData> {
  const data = await httpPost<UploadInitData>('/file/upload/init', {
    path: remoteDir,
    name,
    total_size: desc.totalSize,
    chunk_size: desc.chunkSize,
    chunk_count: desc.chunkCount,
    merkle_root: desc.merkleRootHex,
    file_hash: desc.fileHashHex,
    leaf_hashes: desc.leafHashesHex,
  })
  return data
}

async function completeUpload(uploadId: string, deviceId: string): Promise<UploadCompleteData> {
  return httpPost<UploadCompleteData>('/file/upload/complete', {
    upload_id: uploadId,
    device_id: deviceId,
  })
}
