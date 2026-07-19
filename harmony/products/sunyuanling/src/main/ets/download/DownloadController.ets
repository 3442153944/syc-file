import { appConfig } from '../app/AppConfig';
import { TokenManager } from '../token/tokenManager';
import { Request } from '../net/Request';
import { ResponseException } from '../net/Response';
import type { DownloadParams } from '../api/file/FileTypes';
import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';

/**
 * 下载项状态
 */
export enum DownloadStatus {
  PENDING = 'pending',
  RUNNING = 'running',
  PAUSED = 'paused',
  COMPLETED = 'completed',
  FAILED = 'failed',
  CANCELLED = 'cancelled',
}

/**
 * 下载项（观察对象）
 */
export interface DownloadItem {
  /** 客户端唯一 id（自增） */
  id: number;
  fileName: string;
  /** 远端 path */
  remotePath: string;
  /** 本地保存路径（沙箱内绝对路径） */
  localPath: string;
  /** 远端文件名 */
  name: string;
  /** 设备 id */
  deviceId: string;
  totalBytes: number;
  receivedBytes: number;
  status: DownloadStatus;
  error: string;
  /** 进度 0..100 */
  progress: number;
  /** 创建时间戳 */
  createdAt: number;
}

const STORAGE_KEY = 'downloads';

export type DownloadsChangedHandler = (items: DownloadItem[]) => void;

/**
 * DownloadController
 * 鸿蒙端下载控制器，对标 Android ui/viewModel/data/DownloadController.kt。
 * 通过 @ohos.net.http 的 Range 请求流式分块下载，支持进度回调与取消。
 * 持久化由调用方自行实现（与 Android DownloadStore 类似，MVP 暂不接）。
 *
 * 状态广播：每次状态变化时刷新 AppStorage 中的 'downloads' 数组（替换引用以触发 @StorageLink）。
 */
export class DownloadController {
  private static inst: DownloadController | null = null;
  private items: DownloadItem[] = [];
  private nextId: number = 1;
  private cancelFlags: Map<number, boolean> = new Map<number, boolean>();
  private pausedFlags: Map<number, boolean> = new Map<number, boolean>();
  private ctx: common.Context | null = null;

  private constructor() {}

  static getInstance(): DownloadController {
    if (DownloadController.inst === null) {
      DownloadController.inst = new DownloadController();
    }
    return DownloadController.inst;
  }

  setContext(ctx: common.Context): void {
    this.ctx = ctx;
    AppStorage.setOrCreate<DownloadItem[]>(STORAGE_KEY, []);
  }

  getItems(): DownloadItem[] {
    return this.items;
  }

  private emit(): void {
    // 替换数组引用以触发 @StorageLink 响应式更新
    const snapshot: DownloadItem[] = this.items.slice();
    AppStorage.set<DownloadItem[]>(STORAGE_KEY, snapshot);
  }

  /** 构造下载 URL（token 放 query） */
  private buildUrl(params: DownloadParams): string {
    const base = `${appConfig.getBaseUrl()}/v1/file/download`;
    const q: string[] = [];
    q.push(`path=${encodeURIComponent(params.path)}`);
    q.push(`name=${encodeURIComponent(params.name)}`);
    if (params.device_id && params.device_id.length > 0) {
      q.push(`device_id=${encodeURIComponent(params.device_id)}`);
    }
    const token = TokenManager.getInstance().getToken();
    if (token.length > 0) {
      q.push(`token=${encodeURIComponent(token)}`);
    }
    return `${base}?${q.join('&')}`;
  }

  /**
   * 添加并启动一个下载。
   */
  async addDownload(params: DownloadParams, localFileName: string = ''): Promise<number> {
    const id: number = this.nextId++;
    const fileName: string = localFileName.length > 0 ? localFileName : params.name;
    const dir: string = this.ctx !== null
      ? `${this.ctx.filesDir}/${appConfig.downloadDir}`
      : appConfig.downloadDir;
    // 创建目录
    try { fs.mkdirSync(dir, true); } catch (e) { /* ignore if exists */ }
    const localPath: string = `${dir}/${fileName}`;

    const item: DownloadItem = {
      id,
      fileName,
      remotePath: params.path,
      localPath,
      name: params.name,
      deviceId: params.device_id,
      totalBytes: 0,
      receivedBytes: 0,
      status: DownloadStatus.PENDING,
      error: '',
      progress: 0,
      createdAt: Date.now(),
    };
    this.items.push(item);
    this.emit();

    // 异步启动下载
    this.runDownload(id, params, localPath).catch((e: Error) => {
      console.error(`[DownloadController] run failed: ${e.message}`);
    });
    return id;
  }

  /** 实际执行下载（流式 Range） */
  private async runDownload(id: number, params: DownloadParams, localPath: string): Promise<void> {
    const itemIdx: number = this.findIndex(id);
    if (itemIdx < 0) return;
    this.items[itemIdx].status = DownloadStatus.RUNNING;
    this.emit();

    const url: string = this.buildUrl(params);
    const token: string = TokenManager.getInstance().getToken();
    let received: number = 0;
    let total: number = 0;

    // 1. 探测文件大小 + 是否支持 Range
    let acceptRanges: boolean = false;
    try {
      const probe = await Request.probeDownload('/file/download', {
        'path': params.path,
        'name': params.name,
        'device_id': params.device_id,
      });
      total = probe.totalBytes;
      acceptRanges = probe.acceptRanges;
    } catch (e) {
      this.failItem(id, `probe failed: ${(e as Error).message}`);
      return;
    }

    // 2. 若已有部分文件且支持断点续传，从 received 继续
    let writerFd: number = -1;
    try {
      if (acceptRanges && fs.accessSync(localPath)) {
        const stat: fs.Stat = fs.statSync(localPath);
        received = stat.size;
      }
      // 以追加写打开（不存在则创建）
      const file: fs.File = fs.openSync(localPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.APPEND);
      writerFd = file.fd;
    } catch (e) {
      this.failItem(id, `open local failed: ${(e as Error).message}`);
      return;
    }

    this.items[this.findIndex(id)].totalBytes = total;
    this.items[this.findIndex(id)].receivedBytes = received;
    this.emit();

    // 3. 流式 Range 下载
    const chunkBytes: number = 4 * 1024 * 1024; // 4MB / chunk
    try {
      while (received < total) {
        if (this.cancelFlags.get(id) === true) {
          this.items[this.findIndex(id)].status = DownloadStatus.CANCELLED;
          this.emit();
          break;
        }
        if (this.pausedFlags.get(id) === true) {
          this.items[this.findIndex(id)].status = DownloadStatus.PAUSED;
          this.emit();
          // 暂停期间空转等待
          await this.sleep(500);
          continue;
        }

        const end: number = Math.min(received + chunkBytes - 1, total - 1);
        let buf: ArrayBuffer;
        try {
          buf = await Request.downloadRange('/file/download', {
            'path': params.path,
            'name': params.name,
            'device_id': params.device_id,
          }, received, end);
        } catch (e) {
          this.failItem(id, `range fetch failed: ${(e as Error).message}`);
          return;
        }
        fs.writeSync(writerFd, buf);
        received += buf.byteLength;
        const progress: number = total > 0 ? Math.floor((received / total) * 100) : 0;
        const idx: number = this.findIndex(id);
        this.items[idx].receivedBytes = received;
        this.items[idx].progress = progress;
        this.emit();
      }

      if (received >= total) {
        const idx: number = this.findIndex(id);
        if (this.items[idx].status !== DownloadStatus.CANCELLED) {
          this.items[idx].status = DownloadStatus.COMPLETED;
          this.items[idx].progress = 100;
          this.emit();
        }
      }
    } catch (e) {
      this.failItem(id, (e as Error).message);
    } finally {
      if (writerFd >= 0) {
        try { fs.closeSync(writerFd); } catch (e) { /* ignore */ }
      }
      this.cancelFlags.delete(id);
      this.pausedFlags.delete(id);
    }
  }

  pauseDownload(id: number): void {
    this.pausedFlags.set(id, true);
  }

  resumeDownload(id: number): void {
    this.pausedFlags.set(id, false);
    const idx: number = this.findIndex(id);
    if (idx >= 0 && this.items[idx].status === DownloadStatus.PAUSED) {
      this.items[idx].status = DownloadStatus.RUNNING;
      this.emit();
    }
  }

  cancelDownload(id: number): void {
    this.cancelFlags.set(id, true);
    this.pausedFlags.set(id, false);
    const idx: number = this.findIndex(id);
    if (idx >= 0) {
      this.items[idx].status = DownloadStatus.CANCELLED;
      this.emit();
    }
  }

  removeDownload(id: number): void {
    const idx: number = this.findIndex(id);
    if (idx >= 0) {
      this.items.splice(idx, 1);
      this.emit();
    }
  }

  private failItem(id: number, err: string): void {
    const idx: number = this.findIndex(id);
    if (idx >= 0) {
      this.items[idx].status = DownloadStatus.FAILED;
      this.items[idx].error = err;
      this.emit();
    }
  }

  private findIndex(id: number): number {
    for (let i = 0; i < this.items.length; i++) {
      if (this.items[i].id === id) return i;
    }
    return -1;
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}

export const downloadController = DownloadController.getInstance();
export { STORAGE_KEY as DOWNLOADS_STORAGE_KEY };
