import { picker, fileUri } from '@kit.CoreFileKit';
import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';

/**
 * CloudLadderStorage
 * 「云梯」公共目录访问层（鸿蒙 NEXT 手机验证通过的唯一可行方案，详见 harmony/README.md）。
 *
 * 机制（QQ / DownloadFileButton 同款，无弹框、任意类型、系统「文件管理」可见、绕开 801）：
 *   1) picker.save() + pickerMode=DOWNLOAD → 无弹框拿到 Download/<bundleName> 目录 uri；
 *   2) new fileUri.FileUri(uri).path → 转成真实文件系统 path；
 *   3) picker 已授权该目录，@ohos.file.fs 直接 mkdir/open/write/read/rename。
 *
 * 目录结构：<Download/包名>/同步/<folder> + <Download/包名>/下载
 *
 * ⚠ 授权是**临时**的，应用退到后台后失效。故 init() 必须在**有 UIAbilityContext 且前台**时调
 *   （如 MainShell.aboutToAppear），拿到 path 后缓存复用；失效后需前台重新 init()。
 */

const SYNC_SUBDIR = '同步';
const DOWNLOAD_SUBDIR = '下载';

class CloudLadderStorageClass {
  getDownloadBase(): string {
    throw new Error('Method not implemented.');
  }
  getInitStatus(): string {
    throw new Error('Method not implemented.');
  }
  private rootPath: string = ''; // /storage/.../download/<bundleName>
  private rootUri: string = '';  // file://docs/.../download/<bundleName>

  /**
   * 初始化：拿公共 Download/<包名> 目录授权 → 转 path → 建 同步/、下载/ 子目录。
   * 必须在 UIAbilityContext + 前台调用。幂等：已就绪且可写则跳过，不重复拉起 picker。
   * @returns 是否就绪
   */
  async init(ctx: common.UIAbilityContext): Promise<boolean> {
    // 已就绪且授权仍有效 → 直接复用
    if (this.rootPath.length > 0) {
      try {
        if (fs.accessSync(this.rootPath)) {
          return true;
        }
      } catch (e) { /* 授权失效，往下重新拿 */ }
    }
    try {
      const options: picker.DocumentSaveOptions = new picker.DocumentSaveOptions();
      options.newFileNames = ['.ytkeep'];
      options.pickerMode = picker.DocumentPickerMode.DOWNLOAD; // 无弹框直接拿 Download/<包名> 目录
      const viewPicker: picker.DocumentViewPicker = new picker.DocumentViewPicker(ctx);
      const result: string[] = await viewPicker.save(options);
      if (result.length === 0) {
        console.warn('[CloudLadder] picker DOWNLOAD 返回空');
        return false;
      }
      this.rootUri = result[0];
      this.rootPath = new fileUri.FileUri(this.rootUri).path;
      this.ensureBaseDirs();
      console.info(`[CloudLadder] ready rootPath=${this.rootPath}`);
      return true;
    } catch (e) {
      const err = e as BusinessError;
      console.error(`[CloudLadder] init failed code=${err.code} msg=${err.message}`);
      return false;
    }
  }

  /** 是否已就绪（拿到公共目录 path）。 */
  hasRoot(): boolean {
    return this.rootPath.length > 0;
  }

  /** 公共根目录 path：/storage/.../download/<包名>。 */
  getRoot(): string {
    return this.rootPath;
  }

  /** 同步根 path：<根>/同步。 */
  syncDir(): string {
    return `${this.rootPath}/${SYNC_SUBDIR}`;
  }

  /** 同步根 path 别名（兼容旧调用）。 */
  syncRootDir(): string {
    return this.syncDir();
  }

  /** 下载目录 path：<根>/下载。 */
  downloadDir(): string {
    return `${this.rootPath}/${DOWNLOAD_SUBDIR}`;
  }

  /** 某个同步文件夹的本地目录 path：<根>/同步/<name>（不创建，仅算路径）。 */
  syncFolderDir(folderName: string): string {
    return `${this.syncDir()}/${sanitize(folderName)}`;
  }

  /** 展示用：Download/<包名>（文件管理器实际按应用名显示）。 */
  friendlyRoot(): string {
    if (this.rootPath.length === 0) return 'Download/云梯（未初始化）';
    const idx: number = this.rootPath.lastIndexOf('/');
    const name: string = idx >= 0 ? this.rootPath.substring(idx + 1) : this.rootPath;
    return `Download/${name}`;
  }

  /** 建 同步/、下载/ 两个基础子目录（幂等）。 */
  ensureBaseDirs(): void {
    if (this.rootPath.length === 0) return;
    this.mkdirs(this.syncDir());
    this.mkdirs(this.downloadDir());
  }

  /** 从根起逐层建出 同步/<name> 目录并返回其 path。 */
  ensureSyncFolderDir(folderName: string): string {
    const d: string = this.syncFolderDir(folderName);
    this.mkdirs(d);
    return d;
  }

  /** 在 baseDir 下按 relative 逐层创建目录，返回最终目录 path。 */
  mkdirpUnder(baseDir: string, relative: string): string {
    let cur: string = baseDir;
    const parts: string[] = relative.split('/');
    for (let i = 0; i < parts.length; i++) {
      const p: string = parts[i];
      if (p.length === 0) continue;
      cur = `${cur}/${p}`;
      this.mkdirs(cur);
    }
    return cur;
  }

  /** 幂等创建目录（含父级）。 */
  mkdirs(dir: string): void {
    try {
      if (!fs.accessSync(dir)) {
        fs.mkdirSync(dir, true);
      }
    } catch (e) {
      console.warn(`[CloudLadder] mkdir failed ${dir}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  /** 兼容旧调用：授权在 init 时完成，这里仅返回是否就绪。 */
  async ensureActivated(): Promise<boolean> {
    return this.rootPath.length > 0;
  }
}

/** 清洗非法文件名字符。 */
function sanitize(name: string): string {
  const trimmed: string = name.trim();
  if (trimmed.length === 0) return 'folder';
  return trimmed.replace(/[\\/:*?"<>|]/g, '_');
}

export const cloudLadderStorage = new CloudLadderStorageClass();

// ============ 二次封装：对外便捷函数 ============

/** 启动初始化（前台 + UIAbilityContext）：拿公共目录授权并建 同步/、下载/。返回是否就绪。 */
export async function initCloudStorage(ctx: common.UIAbilityContext): Promise<boolean> {
  return cloudLadderStorage.init(ctx);
}

/** 是否已拿到公共目录。 */
export function isCloudReady(): boolean {
  return cloudLadderStorage.hasRoot();
}

/** 下载目录 path（<根>/下载）。未就绪返回空串。 */
export function cloudDownloadDir(): string {
  return cloudLadderStorage.hasRoot() ? cloudLadderStorage.downloadDir() : '';
}

/** 同步根 path（<根>/同步）。未就绪返回空串。 */
export function cloudSyncDir(): string {
  return cloudLadderStorage.hasRoot() ? cloudLadderStorage.syncDir() : '';
}

/** 某同步文件夹目录 path，并确保创建。未就绪返回空串。 */
export function cloudSyncFolderDir(folderName: string): string {
  return cloudLadderStorage.hasRoot() ? cloudLadderStorage.ensureSyncFolderDir(folderName) : '';
}

/**
 * 便捷写文件到云梯目录（绝对 path，父目录会自动创建）。
 * @returns 是否成功
 */
export function writeCloudFile(absPath: string, data: ArrayBuffer | string): boolean {
  try {
    const idx: number = absPath.lastIndexOf('/');
    if (idx > 0) {
      cloudLadderStorage.mkdirs(absPath.substring(0, idx));
    }
    const f: fs.File = fs.openSync(absPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
    try {
      fs.writeSync(f.fd, data);
    } finally {
      fs.closeSync(f);
    }
    return true;
  } catch (e) {
    console.error(`[CloudLadder] writeCloudFile failed ${absPath}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    return false;
  }
}
