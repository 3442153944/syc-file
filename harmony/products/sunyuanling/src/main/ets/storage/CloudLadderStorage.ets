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
 *   （如 MainShell.aboutToAppear / Ability.onForeground），拿到 path 后缓存复用；失效后需前台重新 init()。
 * ⚠ 因此后台同步**不能**直接写这里：写不进去。后台一律落沙盒暂存（见 storage/SyncStagingStore），
 *   回到前台重新授权后再 flush 搬进来。isPublicWritable() 就是这个「现在能不能直接写」的判据。
 * ⚠ 所有文件 IO 一律用**异步** fs（Promise 版），避免在主线程做同步大文件读写导致 appfreeze 闪退。
 */

const SYNC_SUBDIR = '同步';
const DOWNLOAD_SUBDIR = '下载';
/** 重取授权失败后的冷却，避免 picker 被连续拉起。 */
const REACQUIRE_COOLDOWN_MS = 30000;

class CloudLadderStorageClass {
  private rootPath: string = ''; // /storage/.../download/<bundleName>
  private rootUri: string = '';  // file://docs/.../download/<bundleName>
  /** init 去重：前台回归 + MainShell 冷启动可能并发调用，避免重复拉起 picker。 */
  private initInFlight: Promise<boolean> | null = null;
  /** 缓存的 UIAbilityContext，供 ensureWritable() 就地重取授权（Ability onCreate 注入）。 */
  private uiCtx: common.UIAbilityContext | null = null;
  /** 上次重取授权失败的时刻，用于冷却，避免 picker 被疯狂拉起。 */
  private lastReacquireFailAt: number = 0;

  /**
   * 初始化：拿公共 Download/<包名> 目录授权 → 转 path → 建 同步/、下载/ 子目录。
   * 必须在 UIAbilityContext + 前台调用。幂等：已就绪且可写则跳过，不重复拉起 picker；
   * 并发调用共用同一次 in-flight 结果。
   */
  async init(ctx: common.UIAbilityContext): Promise<boolean> {
    if (this.initInFlight !== null) return this.initInFlight;
    this.initInFlight = this.doInit(ctx);
    try {
      return await this.initInFlight;
    } finally {
      this.initInFlight = null;
    }
  }

  private async doInit(ctx: common.UIAbilityContext): Promise<boolean> {
    // 已就绪且授权仍有效 → 直接复用
    if (this.rootPath.length > 0 && await this.pathExists(this.rootPath)) {
      return true;
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
      await this.ensureBaseDirs();
      console.info(`[CloudLadder] ready rootPath=${this.rootPath}`);
      return true;
    } catch (e) {
      const err = e as BusinessError;
      console.error(`[CloudLadder] init failed code=${err.code} msg=${err.message}`);
      return false;
    }
  }

  /** 是否已就绪（拿到公共目录 path）。 */
  isReady(): boolean {
    return this.rootPath.length > 0;
  }

  /** 是否已拿到公共目录 path（同步快照）。 */
  isPublicWritable(): boolean {
    return this.rootPath.length > 0;
  }

  /**
   * 确保公共目录可用：有缓存 path 就直接用，没有才去 picker 重取（带失败冷却）。
   *
   * ⚠ 这里**故意不去判断「应用是否在前台」**。曾经用一个 foreground 布尔来猜，
   *   结果任何系统弹框（通知授权、定位授权、picker 自身）都会让 Ability 触发 onBackground，
   *   把这个布尔打翻 → 明明人在前台，却整轮往沙盒里写。
   *   能不能写，交给真正的 fs.open 去回答：写成功就是能写，抛错了再退沙盒
   *   （见 SyncEngine.attemptDownload 的降级分支）。这样没有竞态、且自我修正。
   */
  async ensureWritable(): Promise<boolean> {
    if (this.rootPath.length > 0) return true;
    if (this.uiCtx === null) return false;
    // 失败冷却：picker 连续拉起既扰民也没意义
    const now: number = Date.now();
    if (now - this.lastReacquireFailAt < REACQUIRE_COOLDOWN_MS) return false;
    console.info('[CloudLadder] 无可用授权目录，重取中...');
    const ok: boolean = await this.init(this.uiCtx);
    if (!ok) {
      this.lastReacquireFailAt = now;
      console.warn('[CloudLadder] 重取授权失败，本轮落沙盒暂存');
    }
    return ok;
  }

  /** 注入 UIAbilityContext（Ability onCreate 调），供 ensureWritable() 就地重取授权。 */
  setContext(ctx: common.UIAbilityContext): void {
    this.uiCtx = ctx;
  }

  /** 回到前台：清掉重取冷却，让下一次写/flush 能立刻再试一次 picker。 */
  onForegroundHint(): void {
    this.lastReacquireFailAt = 0;
  }

  /**
   * 标记授权已失效（实际写公共目录失败时调用）：清掉缓存的 path。
   * 恢复走 ensureWritable()（前台可就地重取），不再依赖前后台切换。
   */
  invalidate(): void {
    console.warn('[CloudLadder] authorization invalidated');
    this.rootPath = '';
    this.rootUri = '';
  }

  /** 公共根目录 path：/storage/.../download/<包名>。 */
  root(): string {
    return this.rootPath;
  }

  /** 同步根 path：<根>/同步（仅拼字符串，不做 IO）。 */
  syncDir(): string {
    return `${this.rootPath}/${SYNC_SUBDIR}`;
  }

  /** 下载目录 path：<根>/下载（仅拼字符串，不做 IO）。 */
  downloadDir(): string {
    return `${this.rootPath}/${DOWNLOAD_SUBDIR}`;
  }

  /** 某同步文件夹的本地目录 path：<根>/同步/<name>（仅拼字符串，不创建）。 */
  syncFolderPath(folderName: string): string {
    return `${this.syncDir()}/${sanitize(folderName)}`;
  }

  /** 展示用：Download/<包名>（文件管理器实际按应用名显示）。 */
  friendlyRoot(): string {
    if (this.rootPath.length === 0) return 'Download/云梯（未初始化）';
    const idx: number = this.rootPath.lastIndexOf('/');
    const name: string = idx >= 0 ? this.rootPath.substring(idx + 1) : this.rootPath;
    return `Download/${name}`;
  }

  /** 建 同步/、下载/ 两个基础子目录（幂等，异步）。 */
  async ensureBaseDirs(): Promise<void> {
    if (this.rootPath.length === 0) return;
    await this.ensureDir(this.syncDir());
    await this.ensureDir(this.downloadDir());
  }

  /** 建出 同步/<name> 目录并返回其 path（异步）。 */
  async ensureSyncFolder(folderName: string): Promise<string> {
    const d: string = this.syncFolderPath(folderName);
    await this.ensureDir(d);
    return d;
  }

  /**
   * 清掉挡在目标路径上的「类型不对」的东西，返回是否做了清理。
   *
   * 为什么需要：曾经把 relative_path（含文件名）当目录用，导致每个文件都被写成
   * `<folder>/a.png/a.png`——即每个文件外面套了一层同名目录。那个 bug 修了，
   * 但**磁盘上的残留还在**，于是：scan 把 a.png 报成目录 → 服务端比对 trunk 认为不一致
   * → 重派 download → 写 `<folder>/a.png` 又被那个目录挡住 → 落沙盒 → flush 也被挡
   * → 下一轮 scan 重来，永远不收敛。
   *
   * download_only 客户端以服务端 trunk 为准，所以本地类型不对的直接清掉。
   *
   * @param wantDir true=期望这里是目录（挡路的文件删掉）；false=期望是文件（挡路的目录整棵删掉）
   */
  async clearBlockingPath(path: string, wantDir: boolean): Promise<boolean> {
    let stat: fs.Stat;
    try {
      stat = await fs.stat(path);
    } catch (e) {
      return false;   // 不存在，没什么可清
    }
    const isDir: boolean = stat.isDirectory();
    if (isDir === wantDir) return false;   // 类型对得上，不动
    try {
      if (isDir) {
        await fs.rmdir(path);   // 递归删整棵（历史 bug 残留的同名目录）
      } else {
        await fs.unlink(path);
      }
      console.warn(`[CloudLadder] 清理类型不符的残留: ${path}（期望${wantDir ? '目录' : '文件'}）`);
      return true;
    } catch (e) {
      console.error(`[CloudLadder] 清理残留失败 ${path}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      return false;
    }
  }

  /** 幂等创建目录（含所有父级，异步；fs.mkdir recursion=true 自动建父目录）。 */
  async ensureDir(dir: string): Promise<void> {
    try {
      await fs.mkdir(dir, true);
    } catch (e) {
      // 已存在（EEXIST）等属正常，忽略
    }
  }

  /** 异步判断路径是否存在。 */
  private async pathExists(p: string): Promise<boolean> {
    try {
      return await fs.access(p);
    } catch (e) {
      return false;
    }
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
  return cloudLadderStorage.isReady();
}

/** 此刻能否直接写公共目录（已授权 + 在前台）。为 false 时应落沙盒暂存。 */
export function isCloudPublicWritable(): boolean {
  return cloudLadderStorage.isPublicWritable();
}

/** 下载目录 path（<根>/下载）。未就绪返回空串。 */
export function cloudDownloadDir(): string {
  return cloudLadderStorage.isReady() ? cloudLadderStorage.downloadDir() : '';
}

/** 同步根 path（<根>/同步）。未就绪返回空串。 */
export function cloudSyncDir(): string {
  return cloudLadderStorage.isReady() ? cloudLadderStorage.syncDir() : '';
}

/** 某同步文件夹目录 path（仅拼串，不创建）。未就绪返回空串。 */
export function cloudSyncFolderPath(folderName: string): string {
  return cloudLadderStorage.isReady() ? cloudLadderStorage.syncFolderPath(folderName) : '';
}

/** 建出某同步文件夹目录并返回 path（异步）。未就绪返回空串。 */
export async function ensureCloudSyncFolder(folderName: string): Promise<string> {
  if (!cloudLadderStorage.isReady()) return '';
  return cloudLadderStorage.ensureSyncFolder(folderName);
}

/** 幂等创建目录（含父级，异步）。 */
export async function ensureCloudDir(dir: string): Promise<void> {
  await cloudLadderStorage.ensureDir(dir);
}

/**
 * 便捷写文件到云梯目录（绝对 path，父目录会自动创建，全异步不阻塞主线程）。
 * @returns 是否成功
 */
export async function writeCloudFile(absPath: string, data: ArrayBuffer | string): Promise<boolean> {
  try {
    const idx: number = absPath.lastIndexOf('/');
    if (idx > 0) {
      await cloudLadderStorage.ensureDir(absPath.substring(0, idx));
    }
    const f: fs.File = await fs.open(absPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
    try {
      await fs.write(f.fd, data);
    } finally {
      await fs.close(f.fd);
    }
    return true;
  } catch (e) {
    console.error(`[CloudLadder] writeCloudFile failed ${absPath}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    return false;
  }
}
