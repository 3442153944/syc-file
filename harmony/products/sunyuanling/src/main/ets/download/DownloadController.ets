import { appConfig } from '../app/AppConfig';
import { TokenManager } from '../token/tokenManager';
import { Request } from '../net/Request';
import { ResponseException } from '../net/Response';
import type { DownloadParams } from '../api/file/FileTypes';
import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';
import { downloadStore } from './DownloadStore';
import { DownloadStatus, DownloadItem } from './DownloadTypes';
import { NotificationHelper } from '../util/NotificationHelper';
import { cloudLadderStorage } from '../storage/CloudLadderStorage';

export { DownloadStatus, DownloadItem };

interface WaitQueueItem {
  id: number;
  params: DownloadParams;
  localPath: string;
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
  private activeCount: number = 0;
  private waitQueue: WaitQueueItem[] = [];

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

  async restorePendingDownloads(): Promise<void> {
    const pending = await downloadStore.load();
    if (pending.length === 0) return;
    for (let i = 0; i < pending.length; i++) {
      const item = pending[i];
      item.status = DownloadStatus.PENDING;
      item.receivedBytes = 0;
      this.items.push(item);
      if (item.id >= this.nextId) {
        this.nextId = item.id + 1;
      }
      const params: DownloadParams = {
        path: item.remotePath,
        name: item.name,
        device_id: item.deviceId,
      };
      this.enqueueOrRun(item.id, params, item.localPath);
    }
    this.emit();
    await downloadStore.clear();
  }

  private persist(): void {
    downloadStore.save(this.items).catch(() => {});
  }

  getItems(): DownloadItem[] {
    return this.items;
  }

  private emit(): void {
    const snapshot: DownloadItem[] = this.items.slice();
    AppStorage.set<DownloadItem[]>(STORAGE_KEY, snapshot);
    this.persist();
  }

  /**
   * 添加并启动一个下载（受 maxConcurrentDownloads 并发限制）。
   * targetUri: 用户通过 picker 选择的保存路径（file:// URI）；为空则存沙箱默认目录。
   */
  async addDownload(params: DownloadParams, localFileName: string = '', targetUri: string = ''): Promise<number> {
    const id: number = this.nextId++;
    const fileName: string = localFileName.length > 0 ? localFileName : params.name;
    let localPath: string;
    if (targetUri.length > 0) {
      localPath = targetUri;
    } else if (cloudLadderStorage.isReady()) {
      // 默认下载位置：公共目录 Download/<包名>/下载（文件管理器可见）
      const dir: string = cloudLadderStorage.downloadDir();
      await cloudLadderStorage.ensureDir(dir);
      localPath = `${dir}/${fileName}`;
    } else {
      // 未就绪兜底：落沙箱（不可见），前台 init 后新下载会落公共目录
      const dir: string = this.ctx !== null ? `${this.ctx.filesDir}/downloads` : 'downloads';
      await cloudLadderStorage.ensureDir(dir);
      localPath = `${dir}/${fileName}`;
    }

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

    this.enqueueOrRun(id, params, localPath);
    return id;
  }

  private enqueueOrRun(id: number, params: DownloadParams, localPath: string): void {
    if (this.activeCount < appConfig.maxConcurrentDownloads) {
      this.activeCount++;
      this.runDownload(id, params, localPath).catch((e: Error) => {
        console.error(`[DownloadController] run failed: ${e.message}`);
      }).finally(() => {
        this.activeCount--;
        this.processQueue();
      });
    } else {
      const waitItem: WaitQueueItem = { id, params, localPath };
      this.waitQueue.push(waitItem);
    }
  }

  private processQueue(): void {
    while (this.waitQueue.length > 0 && this.activeCount < appConfig.maxConcurrentDownloads) {
      const next = this.waitQueue.shift();
      if (next === undefined) break;
      this.activeCount++;
      this.runDownload(next.id, next.params, next.localPath).catch((e: Error) => {
        console.error(`[DownloadController] queued run failed: ${e.message}`);
      }).finally(() => {
        this.activeCount--;
        this.processQueue();
      });
    }
  }

  /** 实际执行下载（流式 Range） */
  private async runDownload(id: number, params: DownloadParams, localPath: string): Promise<void> {
    const itemIdx: number = this.findIndex(id);
    if (itemIdx < 0) return;
    this.items[itemIdx].status = DownloadStatus.RUNNING;
    this.emit();

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
      if (acceptRanges && await fs.access(localPath)) {
        const stat: fs.Stat = await fs.stat(localPath);
        received = stat.size;
      }
      // 以追加写打开（不存在则创建，异步）
      const file: fs.File = await fs.open(localPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.APPEND);
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
        await fs.write(writerFd, buf);
        received += buf.byteLength;
        const progress: number = total > 0 ? Math.floor((received / total) * 100) : 0;
        const idx: number = this.findIndex(id);
        this.items[idx].receivedBytes = received;
        this.items[idx].progress = progress;
        this.emit();
        NotificationHelper.notifyProgress(id, params.name, progress, received, total);
      }

      if (received >= total) {
        const idx: number = this.findIndex(id);
        if (this.items[idx].status !== DownloadStatus.CANCELLED) {
          this.items[idx].status = DownloadStatus.COMPLETED;
          this.items[idx].progress = 100;
          this.emit();
          NotificationHelper.notifyComplete(id, params.name);
        }
      }
    } catch (e) {
      this.failItem(id, (e as Error).message);
      NotificationHelper.notifyFailed(id, params.name, (e as Error).message);
    } finally {
      if (writerFd >= 0) {
        try { await fs.close(writerFd); } catch (e) { /* ignore */ }
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
