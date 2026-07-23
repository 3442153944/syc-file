import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { cloudLadderStorage } from '../storage/CloudLadderStorage';

/**
 * SyncMappingStore
 * 设备私有的 folder→本地目录映射 + 启用状态，对标 Android sync/SyncMappingStore.kt。
 * 持久化到 preferences（JSON），默认映射到授权的云梯根 /同步/<name>（见 CloudLadderStorage）。
 */

export interface SyncMapping {
  folderId: number;
  localPath: string;
  enabled: boolean;
}

const PREF_NAME = 'sync_mappings';
const PREF_KEY = 'mappings';

class SyncMappingStoreClass {
  private pref: preferences.Preferences | null = null;
  private mappings: Map<number, SyncMapping> = new Map<number, SyncMapping>();

  async init(context: common.Context): Promise<void> {
    try {
      this.pref = await preferences.getPreferences(context, PREF_NAME);
      const raw = await this.pref.get(PREF_KEY, '');
      if ((raw as string).length > 0) {
        const arr = JSON.parse(raw as string) as SyncMapping[];
        for (let i = 0; i < arr.length; i++) {
          this.mappings.set(arr[i].folderId, arr[i]);
        }
      }
    } catch (e) {
      console.error(`[SyncMappingStore] init failed: ${JSON.stringify(e)}`);
    }
  }

  getMapping(folderId: number): SyncMapping | null {
    return this.mappings.get(folderId) ?? null;
  }

  isEnabled(folderId: number): boolean {
    const m = this.mappings.get(folderId);
    return m !== undefined ? m.enabled : true;
  }

  getLocalPath(folderId: number, folderName: string, filesDir: string): string {
    const m = this.mappings.get(folderId);
    if (m !== undefined && m.localPath.length > 0) return m.localPath;
    return cloudLadderStorage.syncFolderDir(folderName);
  }

  async setMapping(folderId: number, localPath: string, enabled: boolean): Promise<void> {
    const mapping: SyncMapping = { folderId, localPath, enabled };
    this.mappings.set(folderId, mapping);
    await this.persist();
  }

  async setEnabled(folderId: number, enabled: boolean): Promise<void> {
    const existing = this.mappings.get(folderId);
    if (existing !== undefined) {
      existing.enabled = enabled;
    } else {
      const mapping: SyncMapping = { folderId, localPath: '', enabled };
      this.mappings.set(folderId, mapping);
    }
    await this.persist();
  }

  private async persist(): Promise<void> {
    if (this.pref === null) return;
    try {
      const arr: SyncMapping[] = [];
      this.mappings.forEach((v) => { arr.push(v); });
      await this.pref.put(PREF_KEY, JSON.stringify(arr));
      await this.pref.flush();
    } catch (e) {
      console.error(`[SyncMappingStore] persist failed: ${JSON.stringify(e)}`);
    }
  }
}

export const syncMappingStore = new SyncMappingStoreClass();
