/**
 * DownloadTypes
 * 下载域共享类型（避免 DownloadController ↔ DownloadStore 循环依赖）。
 */

export enum DownloadStatus {
  PENDING = 'pending',
  RUNNING = 'running',
  PAUSED = 'paused',
  COMPLETED = 'completed',
  FAILED = 'failed',
  CANCELLED = 'cancelled',
}

export interface DownloadItem {
  id: number;
  fileName: string;
  remotePath: string;
  localPath: string;
  name: string;
  deviceId: string;
  totalBytes: number;
  receivedBytes: number;
  status: DownloadStatus;
  error: string;
  progress: number;
  createdAt: number;
}
