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
 *
 * ── ⭐ 为什么台账还要在公共目录留一份镜像 ─────────────────────
 * 安卓的台账（`<ExternalStorage>/FileSync/sync_base.json`）和同步数据在**同一块存储**上，
 * 而且清单本来就来自磁盘遍历——台账丢了顶多多算几次 hash，清单照样是完整的。
 * 鸿蒙这边是**两块独立的存储**：数据在 Download/<包名>（公共），台账在沙盒 filesDir。
 * 沙盒会被「重装 / 清除数据 / DevEco 每次重新安装调试包」单独清掉，而公共目录里的文件还在，
 * 于是台账归零 → 清单为空 → 服务端 `HandleScan` 认为本设备什么都没有 → **全量重派整个 trunk**
 * （靠 `alreadyHave` 的 hash 比对才没真的下载，但每个文件都要重算一遍 blake3，白烧一轮）。
 *
 * 所以台账必须**和数据放在一起**：`<Download/包名>/同步/.yt_baseline.json` 是权威副本，
 * 沙盒那份是「公共目录还没授权时也能用」的快速副本。沙盒空而镜像在 → 用镜像重建（`attachMirror`）。
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
/** 公共同步目录里的镜像文件名（与数据同生共死，见类注释）。 */
export const BASE_MIRROR_NAME = '.yt_baseline.json';

class SyncBaseStoreClass {
  private filePath: string = '';
  /** 公共目录镜像路径（未授权时为空串）。 */
  private mirrorPath: string = '';
  /** folderId → (rel → entry) */
  private byFolder: Map<number, Map<string, BaseEntry>> = new Map<number, Map<string, BaseEntry>>();
  private dirty: boolean = false;
  private saveTimer: number = -1;

  async init(ctx: common.Context): Promise<void> {
    this.filePath = `${ctx.filesDir}/${BASE_FILE}`;
    const raw: string = await readTextFile(this.filePath);
    this.ingest(raw, '沙盒台账');
    console.info(`[云梯Boot] baseline loaded folders=${this.byFolder.size} total=${this.totalCount()}`);
  }

  /**
   * 绑定公共目录镜像。**必须在公共目录拿到授权之后调**（幂等，可重复调）。
   *
   * - 内存里已有台账 → 把镜像补写一遍（老版本升级上来时镜像还不存在）；
   * - 内存里是空的 → 从镜像重建，并回写沙盒。这一步就是「重装/清数据后不再全量重拉」的关键。
   */
  async attachMirror(mirrorPath: string): Promise<void> {
    if (mirrorPath.length === 0) return;
    this.mirrorPath = mirrorPath;
    if (this.totalCount() > 0) {
      this.dirty = true;
      await this.flushNow();
      return;
    }
    const raw: string = await readTextFile(mirrorPath);
    if (raw.length === 0) return;
    this.ingest(raw, '公共目录镜像');
    const n: number = this.totalCount();
    if (n > 0) {
      console.info(`[云梯Boot] baseline restored from mirror total=${n}（沙盒台账已丢失，用公共目录镜像重建）`);
      this.dirty = true;
      await this.flushNow();   // 回写沙盒，下次没授权也能用
    }
  }

  /** 是否已绑定镜像（未授权公共目录时为 false）。 */
  hasMirror(): boolean {
    return this.mirrorPath.length > 0;
  }

  /** 解析一份台账 JSON 并并入内存（损坏按空处理）。 */
  private ingest(raw: string, source: string): void {
    if (raw.length === 0) return;
    try {
      const rows = JSON.parse(raw) as BaseRow[];
      for (let i = 0; i < rows.length; i++) {
        const r: BaseRow = rows[i];
        const entry: BaseEntry = { rel: r.rel, hash: r.hash, size: r.size, mtime: r.mtime };
        this.folderMap(r.folderId).set(r.rel, entry);
      }
    } catch (e) {
      // JSON 损坏：当作没有台账，下一轮同步会把它重建起来（比用半截数据安全）
      console.error(`[SyncBase] ${source}解析失败，按空台账处理: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
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
        this.persist().catch((e: Error) => { console.error(`[SyncBase] persist failed: ${e.message}`); });
      }
    }, 1500);
  }

  /**
   * 立刻落盘（吃掉防抖窗口）。
   *
   * 后台被冻结/杀进程是常态，1.5s 的防抖窗口里攒的账一丢，下次上线清单就少一截。
   * 所以**上报 scan 前、引擎 stop 时、退到后台时**都要强制刷一次。
   */
  async flushNow(): Promise<void> {
    if (this.saveTimer !== -1) {
      clearTimeout(this.saveTimer);
      this.saveTimer = -1;
    }
    if (!this.dirty) return;
    this.dirty = false;
    try {
      await this.persist();
    } catch (e) {
      console.error(`[SyncBase] flushNow failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  /** 沙盒 + 公共目录镜像双写。镜像写不进去（授权失效）只是降级，不算失败。 */
  private async persist(): Promise<void> {
    if (this.filePath.length === 0) return;
    const rows: BaseRow[] = [];
    this.byFolder.forEach((m: Map<string, BaseEntry>, folderId: number) => {
      m.forEach((v: BaseEntry) => {
        const row: BaseRow = { folderId, rel: v.rel, hash: v.hash, size: v.size, mtime: v.mtime };
        rows.push(row);
      });
    });
    const text: string = JSON.stringify(rows);
    await writeTextFileAtomic(this.filePath, text);
    if (this.mirrorPath.length > 0) {
      const ok: boolean = await writeTextFileAtomic(this.mirrorPath, text);
      if (!ok) {
        console.warn('[SyncBase] 台账镜像写入失败（公共目录授权可能已失效），本轮只留沙盒副本');
      }
    }
  }
}

export const syncBaseStore = new SyncBaseStoreClass();
