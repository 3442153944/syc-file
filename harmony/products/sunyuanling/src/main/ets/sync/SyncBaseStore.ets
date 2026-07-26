import { common } from '@kit.AbilityKit';
import { readTextFile, writeTextFileAtomic } from '../storage/JsonFileStore';

/**
 * SyncBaseStore
 * 同步基线台账：记录「本设备已经拿到了哪些文件、各自的 blake3 是什么」。
 * 对标 Android sync/SyncBaseStore.kt。
 *
 * ── 鸿蒙为什么**必须**有这个 ────────────────────────────────
 * scan 是全量比对，客户端得把本地清单完整报上去，少报一个服务端就重派一个。
 * Android/桌面端直接遍历真实目录就行；鸿蒙的同步目录是 picker **临时授权**的公共目录，
 * `fs.listFile` 在这种路径上并不可靠（授权只验证过读写，没验证过枚举），
 * 一旦枚举失败，清单就是空的 → 服务端每轮把所有文件重派一遍
 * （现象：每次切前台全量重下，虽然 hash 相同被跳过，但空转一整轮）。
 *
 * 所以清单以**本台账为准**：每成功落一个文件就记一笔，收到 delete 就抹一笔。
 * 磁盘遍历退化成「有就用、没有就算了」的补充手段（捡外部塞进来的文件）。
 *
 * 存储用**沙盒里的 JSON 文件**而不是 preferences：台账体量随文件数线性增长
 * （几百个文件轻松上百 KB），preferences 是给小配置项用的、value 长度上限文档说法不一，
 * 压进去既可能被截断又要每次重写整串。详见 storage/JsonFileStore。
 */

export interface BaseEntry {
  /** folder 内相对路径，**含文件名**（与服务端 rel 语义一致） */
  rel: string;
  hash: string;
  size: number;
  /** 秒 */
  mtime: number;
}

/** 持久化用的扁平行（Map 不能直接 JSON 化）。 */
interface BaseRow {
  folderId: number;
  rel: string;
  hash: string;
  size: number;
  mtime: number;
}

const BASE_FILE = 'sync_baseline.json';

class SyncBaseStoreClass {
  private filePath: string = '';
  /** folderId → (rel → entry) */
  private byFolder: Map<number, Map<string, BaseEntry>> = new Map<number, Map<string, BaseEntry>>();
  private dirty: boolean = false;
  private saveTimer: number = -1;

  async init(ctx: common.Context): Promise<void> {
    this.filePath = `${ctx.filesDir}/${BASE_FILE}`;
    const raw: string = await readTextFile(this.filePath);
    if (raw.length > 0) {
      try {
        const rows = JSON.parse(raw) as BaseRow[];
        for (let i = 0; i < rows.length; i++) {
          const r: BaseRow = rows[i];
          const entry: BaseEntry = { rel: r.rel, hash: r.hash, size: r.size, mtime: r.mtime };
          this.folderMap(r.folderId).set(r.rel, entry);
        }
      } catch (e) {
        // JSON 损坏：当作没有台账，下一轮同步会把它重建起来（比用半截数据安全）
        console.error(`[SyncBase] 台账解析失败，按空台账处理: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      }
    }
    console.info(`[云梯Boot] baseline loaded folders=${this.byFolder.size} total=${this.totalCount()}`);
  }

  private folderMap(folderId: number): Map<string, BaseEntry> {
    let m: Map<string, BaseEntry> | undefined = this.byFolder.get(folderId);
    if (m === undefined) {
      m = new Map<string, BaseEntry>();
      this.byFolder.set(folderId, m);
    }
    return m;
  }

  totalCount(): number {
    let n: number = 0;
    this.byFolder.forEach((m: Map<string, BaseEntry>) => { n += m.size; });
    return n;
  }

  countOf(folderId: number): number {
    const m: Map<string, BaseEntry> | undefined = this.byFolder.get(folderId);
    return m === undefined ? 0 : m.size;
  }

  /** 落盘成功后记一笔。rel 含文件名。 */
  set(folderId: number, rel: string, hash: string, size: number, mtime: number): void {
    if (rel.length === 0 || hash.length === 0) return;
    const entry: BaseEntry = { rel, hash, size, mtime };
    this.folderMap(folderId).set(rel, entry);
    this.scheduleSave();
  }

  get(folderId: number, rel: string): BaseEntry | null {
    const m: Map<string, BaseEntry> | undefined = this.byFolder.get(folderId);
    if (m === undefined) return null;
    return m.get(rel) ?? null;
  }

  /** 收到 delete、或本地文件确认已不存在时抹掉。 */
  remove(folderId: number, rel: string): void {
    const m: Map<string, BaseEntry> | undefined = this.byFolder.get(folderId);
    if (m === undefined) return;
    if (m.delete(rel)) {
      this.scheduleSave();
    }
  }

  /** 某 folder 的全部基线条目。 */
  snapshot(folderId: number): BaseEntry[] {
    const out: BaseEntry[] = [];
    const m: Map<string, BaseEntry> | undefined = this.byFolder.get(folderId);
    if (m === undefined) return out;
    m.forEach((v: BaseEntry) => { out.push(v); });
    return out;
  }

  /** 清空某 folder（用户重新映射目录时用）。 */
  clearFolder(folderId: number): void {
    this.byFolder.delete(folderId);
    this.scheduleSave();
  }

  /** 防抖持久化：一轮同步几百个文件时不该每笔都落盘。 */
  private scheduleSave(): void {
    this.dirty = true;
    if (this.saveTimer !== -1) return;
    this.saveTimer = setTimeout(() => {
      this.saveTimer = -1;
      if (this.dirty) {
        this.dirty = false;
        this.persist();
      }
    }, 1500);
  }

  private persist(): void {
    if (this.filePath.length === 0) return;
    const rows: BaseRow[] = [];
    this.byFolder.forEach((m: Map<string, BaseEntry>, folderId: number) => {
      m.forEach((v: BaseEntry) => {
        const row: BaseRow = { folderId, rel: v.rel, hash: v.hash, size: v.size, mtime: v.mtime };
        rows.push(row);
      });
    });
    writeTextFileAtomic(this.filePath, JSON.stringify(rows))
      .catch((e: Error) => { console.error(`[SyncBase] persist failed: ${e.message}`); });
  }
}

export const syncBaseStore = new SyncBaseStoreClass();
