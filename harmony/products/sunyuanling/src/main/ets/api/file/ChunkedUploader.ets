import fs, { ReadOptions } from '@ohos.file.fs';
import { FileApi } from '../../api/file/FileApi';
import { SessionGoneException, ChunkVerifyException, ResponseException } from '../../net/Response';
import type { UploadInitData, UploadCompleteData, UploadInitParams } from '../../api/file/FileTypes';
import { describe as fileCoreDescribe, type Description } from '../../core/FileCore';
import { appConfig } from '../../app/AppConfig';

// ===== 内部接口（ArkTS 不允许把对象字面量当类型用） =====
export interface CancelSignal {
  cancelled: boolean;
}
interface MutableNumber { value: number; }
interface MutableString { value: string; }

export interface UploadRequest {
  filePath: string;
  remotePath: string;
  fileName: string;
  deviceId: string;
  chunkSize?: number;
  maxConcurrent?: number;
  deleteBeforeUpload?: boolean;
  onProgress?: (sent: number, total: number) => void;
  cancelSignal?: CancelSignal;
}

export interface UploadResult {
  ok: boolean;
  message: string;
  data?: UploadCompleteData;
  instant?: boolean;
}

// ===== 模块级辅助函数 =====
async function runPool(
  tasks: number[],
  worker: (item: number, index: number) => Promise<void>,
  concurrency: number
): Promise<void> {
  if (tasks.length === 0) return;
  let cursor: number = 0;
  const runners: Promise<void>[] = [];
  const n: number = Math.min(concurrency, tasks.length);
  const runOne: () => Promise<void> = async (): Promise<void> => {
    while (cursor < tasks.length) {
      const idx: number = cursor;
      cursor += 1;
      await worker(tasks[idx], idx);
    }
  };
  for (let i = 0; i < n; i++) {
    runners.push(runOne());
  }
  await Promise.all(runners);
}

function rangeArray(n: number): number[] {
  const arr: number[] = [];
  for (let i = 0; i < n; i++) arr.push(i);
  return arr;
}

function toError(e: Object): Error {
  if (e instanceof Error) return e;
  return new Error(JSON.stringify(e));
}

/**
 * ChunkedUploader
 * 分片上传器，对标 Android api/file/ChunkedUploader.kt + 桌面端 chunked_uploader.rs。
 *
 * 流程（描述计算走 FileCore Rust 内核，无内核时回退纯 ArkTS Blake3Util）：
 *   描述 → init →（秒传则完成）→ 并发补传缺失分片 → complete。
 *
 * 失败处理：SessionGone 自动重新 init 一次；ChunkVerify 重传该片；其余失败抛出。
 */
export class ChunkedUploader {
  static async upload(req: UploadRequest): Promise<UploadResult> {
    const chunkSize: number = req.chunkSize !== undefined && req.chunkSize > 0
      ? req.chunkSize
      : appConfig.uploadChunkSize;
    const concurrency: number = req.maxConcurrent !== undefined && req.maxConcurrent > 0
      ? req.maxConcurrent
      : appConfig.maxConcurrentUploads;

    // 1. 算描述（走 Rust 内核 fc_describe，无则回退纯 ArkTS blake3）
    const desc: Description = await fileCoreDescribe(req.filePath, chunkSize);

    // 2. delete-before-upload（同步覆盖上传前置，服务端 init 拒收已存在文件）
    if (req.deleteBeforeUpload === true) {
      try {
        await FileApi.deleteFile({ path: req.remotePath, name: req.fileName });
      } catch (e) {
        if (e instanceof ResponseException && (e as ResponseException).isNotFound()) {
          // 文件不存在，正常情况
        } else {
          console.warn(`[ChunkedUploader] delete-before-upload failed: ${toError(e as Object).message}`);
        }
      }
    }

    // 3. init
    const initDesc: UploadInitParams = {
      path: req.remotePath,
      name: req.fileName,
      total_size: desc.totalSize,
      chunk_size: desc.chunkSize,
      chunk_count: desc.chunkCount,
      merkle_root: desc.merkleRootHex,
      file_hash: desc.fileHashHex,
      leaf_hashes: desc.leafHashesHex,
      device_id: req.deviceId,
    };
    let initResp: UploadInitData = (await FileApi.uploadInit(initDesc)).data as UploadInitData;

    // 秒传：init 阶段已复制落盘 + 同步派发，无会话——不能调 complete，就地合成结果
    if (initResp.instant) {
      if (req.onProgress !== undefined) {
        req.onProgress(desc.totalSize, desc.totalSize);
      }
      const r: UploadResult = { ok: true, message: 'instant', instant: true };
      return r;
    }

    // 4. 并发上传缺失分片
    let missing: number[] = initResp.missing !== undefined && initResp.missing !== null
      ? initResp.missing
      : rangeArray(desc.chunkCount);

    const totalSentCounter: MutableNumber = { value: 0 };
    const uploadIdHolder: MutableString = { value: initResp.upload_id };

    const uploadOne: (idx: number) => Promise<void> = async (idx: number): Promise<void> => {
      if (req.cancelSignal !== undefined && req.cancelSignal.cancelled) return;
      // 读出该片字节：每次 openSync 拿到独立 fd（指针=0），offset=chunkStart 作为绝对位置读取
      // ArkTS fs.ReadOptions 自 API 11 起仅 offset+length（offset 是「相对当前指针的偏移」，
      // 新 fd 指针=0，故 offset 等同绝对位置）。
      const thisChunkSize: number = idx < desc.chunkCount - 1 ? desc.chunkSize : lastChunkLen(desc, idx);
      const chunkBuf: ArrayBuffer = new ArrayBuffer(thisChunkSize);
      const readPos: number = idx * desc.chunkSize;
      const chunkFile: fs.File = fs.openSync(req.filePath, fs.OpenMode.READ_ONLY);
      try {
        const readOpts: ReadOptions = { offset: readPos, length: thisChunkSize };
        const readLen: number = fs.readSync(chunkFile.fd, chunkBuf, readOpts);
        const chunkData: Uint8Array = new Uint8Array(chunkBuf, 0, readLen);
        try {
          await FileApi.uploadChunk(uploadIdHolder.value, idx, chunkData);
          totalSentCounter.value += thisChunkSize;
          if (req.onProgress !== undefined) {
            req.onProgress(totalSentCounter.value, desc.totalSize);
          }
        } catch (e) {
          if (e instanceof SessionGoneException) {
            // 重新 init 一次（描述不变）
            const reInit: UploadInitData = (await FileApi.uploadInit(initDesc)).data as UploadInitData;
            uploadIdHolder.value = reInit.upload_id;
            const newMissing: number[] = reInit.missing !== undefined && reInit.missing !== null
              ? reInit.missing
              : [];
            totalSentCounter.value = 0;
            await runPool(newMissing, uploadOne, concurrency);
            return;
          }
          if (e instanceof ChunkVerifyException) {
            // 重试该片一次
            try {
              await FileApi.uploadChunk(uploadIdHolder.value, idx, chunkData);
              totalSentCounter.value += thisChunkSize;
              if (req.onProgress !== undefined) {
                req.onProgress(totalSentCounter.value, desc.totalSize);
              }
            } catch (e2) {
              throw toError(e2 as Object);
            }
          } else {
            throw toError(e as Object);
          }
        }
      } finally {
        try { fs.closeSync(chunkFile); } catch (e) { /* ignore */ }
      }
    };

    try {
      await runPool(missing, uploadOne, concurrency);
    } catch (e) {
      throw toError(e as Object);
    }

    // 5. complete
    const completeResp: UploadCompleteData = (await FileApi.uploadComplete({
      upload_id: uploadIdHolder.value,
      device_id: req.deviceId,
    })).data as UploadCompleteData;

    const finalResult: UploadResult = { ok: true, message: 'ok', data: completeResp };
    return finalResult;
  }
}

/** 末片长度（totalSize 不是 chunkSize 整数倍时） */
function lastChunkLen(desc: Description, idx: number): number {
  const rem: number = desc.totalSize - idx * desc.chunkSize;
  return rem < desc.chunkSize ? rem : desc.chunkSize;
}
