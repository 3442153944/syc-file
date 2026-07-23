import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { DownloadItem, DownloadStatus } from './DownloadTypes';

/**
 * DownloadStore
 * 下载任务持久化，对标 Android ui/viewModel/data/DownloadStore.kt。
 * 将未完成的下载任务序列化到 preferences，app 重启后可恢复。
 */
const PREF_NAME = 'download_store';
const PREF_KEY = 'pending_downloads';

export class DownloadStore {
  private static inst: DownloadStore | null = null;
  private pref: preferences.Preferences | null = null;

  private constructor() {}

  static getInstance(): DownloadStore {
    if (DownloadStore.inst === null) {
      DownloadStore.inst = new DownloadStore();
    }
    return DownloadStore.inst;
  }

  async init(context: common.Context): Promise<void> {
    try {
      this.pref = await preferences.getPreferences(context, PREF_NAME);
    } catch (e) {
      console.error(`[DownloadStore] init failed: ${JSON.stringify(e)}`);
    }
  }

  async save(items: DownloadItem[]): Promise<void> {
    if (this.pref === null) return;
    const pending: DownloadItem[] = [];
    for (let i = 0; i < items.length; i++) {
      const s = items[i].status;
      if (s === DownloadStatus.PENDING || s === DownloadStatus.RUNNING || s === DownloadStatus.PAUSED) {
        pending.push(items[i]);
      }
    }
    try {
      await this.pref.put(PREF_KEY, JSON.stringify(pending));
      await this.pref.flush();
    } catch (e) {
      console.error(`[DownloadStore] save failed: ${JSON.stringify(e)}`);
    }
  }

  async load(): Promise<DownloadItem[]> {
    if (this.pref === null) return [];
    try {
      const raw = await this.pref.get(PREF_KEY, '');
      if ((raw as string).length === 0) return [];
      return JSON.parse(raw as string) as DownloadItem[];
    } catch (e) {
      console.error(`[DownloadStore] load failed: ${JSON.stringify(e)}`);
      return [];
    }
  }

  async clear(): Promise<void> {
    if (this.pref === null) return;
    try {
      await this.pref.delete(PREF_KEY);
      await this.pref.flush();
    } catch (e) { /* ignore */ }
  }
}

export const downloadStore = DownloadStore.getInstance();
