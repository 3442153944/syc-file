import fileCoreNative from 'libfilecore.so';
import { FileApi } from '../../api/file/FileApi';
import { ResponseException } from '../../net/Response';
import type { ApiResponseData } from '../../net/Response';
import { Request } from '../../net/Request';
import type {
  DeleteFileParams,
  UploadCompleteData,
  UploadInitData
} from '../../api/file/FileTypes';
import { appConfig } from '../../app/AppConfig';

export interface CancelSignal {
  cancelled: boolean;
}

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

interface UploadNode {
  id: string;
  url: string;
}

interface UploadPlannerState {
  nodes: Object[];
}

interface UploadStartInput {
  file: string;
  remote_dir: string;
  base_url: string;
  device_id: string;
  quick_share: boolean;
  chunk_size: number | null;
  concurrency: number | null;
  nodes: UploadNode[];
  main_node: string;
  state: UploadPlannerState;
  resume: UploadInitData | null;
}

interface UploadBody {
  kind: string;
  value?: Object | null;
}

interface UploadEvent {
  type: string;
  id?: number;
  base_url?: string;
  path?: string;
  query?: string[][];
  body?: UploadBody;
  timeout_ms?: number;
  url?: string;
  sent?: number;
  total?: number;
  upload_id?: string;
  state?: Object;
  level?: string;
  source?: string;
  message?: string;
  data?: UploadCompleteData;
  error?: string;
}

interface UploadReply {
  response?: Object;
  error?: string;
}

interface UploadPumpState {
  closed: boolean;
}

interface UploadHostCall {
  cancelled: boolean;
}

function toError(e: Object): Error {
  if (e instanceof Error) return e;
  return new Error(JSON.stringify(e));
}

function isCancelled(req: UploadRequest): boolean {
  return req.cancelSignal !== undefined && req.cancelSignal.cancelled;
}

function delay(ms: number): Promise<void> {
  return new Promise<void>((resolve: () => void): void => {
    setTimeout(resolve, ms);
  });
}

/** sys_core 负责上传状态机；ArkTS 只提供 HTTP、进度和生命周期适配。 */
export class ChunkedUploader {
  static async upload(req: UploadRequest): Promise<UploadResult> {
    if (isCancelled(req)) throw new Error('上传已取消');

    if (req.deleteBeforeUpload === true) {
      const deleteParams: DeleteFileParams = { path: req.remotePath, name: req.fileName };
      try {
        await FileApi.deleteFile(deleteParams);
      } catch (e) {
        if (e instanceof ResponseException && (e as ResponseException).isNotFound()) {
          // 覆盖上传时目标不存在属于正常情况。
        } else {
          console.warn(`[ChunkedUploader] delete-before-upload failed: ${toError(e as Object).message}`);
        }
      }
    }

    const baseUrl: string = appConfig.getBaseUrl();
    const chunkSize: number | null = req.chunkSize !== undefined && req.chunkSize > 0
      ? req.chunkSize
      : null;
    const concurrency: number | null = req.maxConcurrent !== undefined && req.maxConcurrent > 0
      ? req.maxConcurrent
      : null;
    const node: UploadNode = { id: 'active', url: baseUrl };
    const plannerState: UploadPlannerState = { nodes: [] };
    const input: UploadStartInput = {
      file: req.filePath,
      remote_dir: req.remotePath,
      base_url: baseUrl,
      device_id: req.deviceId,
      quick_share: false,
      chunk_size: chunkSize,
      concurrency: concurrency,
      nodes: [node],
      main_node: 'active',
      state: plannerState,
      resume: null,
    };
    const job: number = fileCoreNative.startUpload(JSON.stringify(input));
    if (job === 0) throw new Error('sys_core 上传任务创建失败');

    const pumpState: UploadPumpState = { closed: false };
    const calls: Map<number, UploadHostCall> = new Map<number, UploadHostCall>();
    try {
      while (true) {
        if (isCancelled(req)) throw new Error('上传已取消');
        const payload: string | null = fileCoreNative.pollUpload(job);
        if (payload === null) throw new Error('sys_core 上传事件读取失败');
        const events = JSON.parse(payload) as UploadEvent[];
        for (let i = 0; i < events.length; i++) {
          const event: UploadEvent = events[i];
          switch (event.type) {
            case 'request':
            case 'probe_share': {
              if (event.id === undefined) throw new Error('上传请求事件缺少 id');
              const call: UploadHostCall = { cancelled: false };
              calls.set(event.id, call);
              ChunkedUploader.executeHostCall(job, event, call, pumpState);
              break;
            }
            case 'cancel_request': {
              if (event.id === undefined) break;
              const call = calls.get(event.id);
              if (call !== undefined) call.cancelled = true;
              calls.delete(event.id);
              break;
            }
            case 'progress': {
              if (req.onProgress !== undefined && event.sent !== undefined && event.total !== undefined) {
                req.onProgress(event.sent, event.total);
              }
              break;
            }
            case 'session': {
              console.info(`[ChunkedUploader] session: ${event.upload_id ?? ''}`);
              break;
            }
            case 'state': {
              // 当前 HarmonyOS UploadRequest 没有持久化规划状态的入口。
              break;
            }
            case 'log': {
              const message: string = event.message ?? '';
              if (event.level === 'warn') {
                console.warn(`[sys_core] ${message}`);
              } else {
                console.info(`[sys_core] ${message}`);
              }
              break;
            }
            case 'done': {
              if (event.data === undefined) throw new Error('上传完成事件缺少数据');
              const instant: boolean = event.data.file_id === 0;
              const result: UploadResult = {
                ok: true,
                message: instant ? 'instant' : 'ok',
                data: event.data,
                instant: instant,
              };
              return result;
            }
            case 'failed': {
              throw new Error(event.error ?? 'sys_core 上传失败');
            }
            default:
              break;
          }
        }
        if (events.length === 0) await delay(10);
      }
    } finally {
      pumpState.closed = true;
      calls.forEach((call: UploadHostCall): void => {
        call.cancelled = true;
      });
      calls.clear();
      fileCoreNative.cancelUpload(job);
    }
  }

  private static async executeHostCall(
    job: number,
    event: UploadEvent,
    call: UploadHostCall,
    pumpState: UploadPumpState
  ): Promise<void> {
    if (event.id === undefined) return;
    let reply: UploadReply;
    try {
      let response: ApiResponseData<Object>;
      if (event.type === 'probe_share') {
        if (event.url === undefined) throw new Error('分享探测事件缺少 url');
        await Request.probeShare(event.url);
        response = { code: 200, message: '', data: null };
      } else {
        if (event.base_url === undefined || event.path === undefined || event.query === undefined ||
          event.body === undefined || event.timeout_ms === undefined) {
          throw new Error('上传请求事件字段不完整');
        }
        let jsonBody: Object | null = null;
        let bytes: Uint8Array | null = null;
        if (event.body.kind === 'bytes') {
          bytes = fileCoreNative.takeUploadBody(job, event.id);
          if (bytes === null) throw new Error('读取上传二进制体失败');
        } else if (event.body.kind === 'json') {
          if (event.body.value === undefined) throw new Error('上传 JSON 请求缺少 body.value');
          jsonBody = event.body.value;
        } else {
          throw new Error(`未知上传 body 类型: ${event.body.kind}`);
        }
        response = await Request.uploadEnvelope(
          event.base_url,
          event.path,
          event.query,
          jsonBody,
          bytes,
          event.timeout_ms
        );
      }
      reply = { response };
    } catch (e) {
      reply = { error: toError(e as Object).message };
    }
    if (!pumpState.closed && !call.cancelled) {
      fileCoreNative.replyUpload(job, event.id, JSON.stringify(reply));
    }
  }
}
