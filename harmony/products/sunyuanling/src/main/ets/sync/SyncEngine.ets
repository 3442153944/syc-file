import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';
import { deviceInfoSvc } from '../util/DeviceInfo';
import { wsManager, WsState, WsMessage } from '../net/WebSocketManager';
import { SyncApi } from '../api/sync/SyncApi';
import type { SyncFolderInfo, WsTaskCreatedEvent, WsConflictEvent, WsMessageEnvelope, SyncTaskInfo, ScanItemDto, SyncScanParams } from '../api/sync/SyncTypes';
import { Request } from '../net/Request';
import { ResponseException } from '../net/Response';
import { fileHashHex } from '../core/FileCore';
import { syncMappingStore } from './SyncMappingStore';
import { syncBaseStore, BASE_MIRROR_NAME } from './SyncBaseStore';
import type { BaseEntry } from './SyncBaseStore';
import { flushLog, FlushEventKind } from '../storage/FlushLog';
import { cloudLadderStorage } from '../storage/CloudLadderStorage';
import { syncStagingStore } from '../storage/SyncStagingStore';
import type { StagedEntry } from '../storage/SyncStagingStore';

/**
 * SyncEngine
 * 鸿蒙端同步引擎（download_only），对标 Android sync/SyncEngine.kt + 桌面端 sync_engine.rs。
 *
 * 与 Android/桌面端的关键差异（鸿蒙无 root、无文件监听能力）：
 *  - **只执行不探测**：处理 S→C 的 task_created（download/delete/mkdir），不监听本地变更、不上报 file_changed。
 *  - **冲突策略**：默认 accept_server（download_only 客户端没有 watcher 能把本地分叉回放重传）。
 *  - **离线追赶**：WS Connected 时拉本设备 pending 任务 + scan 全量清单比对补派（对标 Android Phase2）。
 *
 * 落盘去向（关键）：
 *  - 前台且已拿到 picker 授权 → 直接写公共目录 <授权根>/同步/<folder>/（文件管理器可见）；
 *  - 后台（picker 授权失效，但 DATA_TRANSFER 长时任务仍有网络）→ 落沙盒暂存 SyncStagingStore，
 *    回到前台重新授权后由 flushStaging() 搬进公共目录。
 *  这样后台同步不会因为写不进共享目录而整轮失败/中断。
 *
 * 抗中断（同步初始化/主动拉取偶发中断的根因与对策）：
 *  - **流式下载**：走 Range 分片边下边写，不再整文件读进内存（大文件整块 buffer 是 OOM/闪退主因）；
 *  - **并发有界**：pending 任务过去是 for 循环里全量并发 fire，几十个任务同时下载会撑爆内存/连接，
 *    现统一进 taskQueue，最多 MAX_CONCURRENT_TASKS 个在跑；
 *  - **不再因未授权整轮 return**：追赶轮次不再依赖公共目录就绪（有沙盒兜底）。
 *
 * ⚠ 校验必须**先写文件再 fileHashHex(path)**（native 子线程 mmap，回退纯 ArkTS 异步分片），
 *   绝不能对整块 buffer 在主线程同步算 blake3——大文件会 THREAD_BLOCK 卡死闪退。
 */

/** 同时在跑的同步任务上限（内存与连接数保护）。 */
const MAX_CONCURRENT_TASKS = 2;
/** 流式下载分片大小，与 DownloadController 保持一致。 */
const DOWNLOAD_CHUNK_BYTES = 4 * 1024 * 1024;
/** 单个文件下载的最大尝试次数（传输被掐是常态，不能一次失败就放弃整个文件）。 */
const MAX_DOWNLOAD_ATTEMPTS = 3;
/** 沙盒里还有货时的归位重试间隔。沙盒是中转不是存储，越快搬空越好。 */
const FLUSH_RETRY_INTERVAL_MS = 5000;
/**
 * 每个同步 folder 根下的哨兵文件，两个用途：
 *  ① 枚举自检：它一定在，`fs.listFile` 列不出它 = 这个目录上的枚举不可信（见 canTrustEmptyListing）；
 *  ② 「本设备同步过这个 folder」的标记。
 */
const SENTINEL_NAME = '.ytsync';

/** 本次下载的落盘去向。 */
interface WriteTarget {
  path: string;
  /** true = 落在沙盒暂存区，待前台 flush 搬入公共目录 */
  staged: boolean;
}

/**
 * relative_path 拆解结果。
 *
 * ⚠ 服务端（internal/sync/scan.go relFromPath）和 Android（t.relativePath）里，
 *   relative_path 是**含文件名的完整相对路径**（如 `sub/dir/a.png`），不是目录！
 *   本类内部一律拆成 dir（不含文件名）+ name 再用，别直接拿 relative_path 当目录拼。
 */
interface RelParts {
  /** 目录部分，folder 根下为空串 */
  dir: string;
  /** 叶子名（文件名或目录名） */
  name: string;
}

/** 单次下载尝试的结果。 */
interface DownloadAttempt {
  ok: boolean;
  /** true = 重试也没用（目标目录不可写等），直接放弃 */
  fatal: boolean;
  error: string;
  path: string;
  staged: boolean;
  /** 成功时的整文件 blake3 hex */
  hash: string;
}

class SyncEngineClass {
  private ctx: common.Context | null = null;
  private started: boolean = false;
  private folders: SyncFolderInfo[] = [];
  private unregMsg: (() => void) | null = null;
  private unregState: (() => void) | null = null;
  private inFlightTaskIds: Set<number> = new Set<number>();
  private catchUpRunning: boolean = false;
  /** 进程内 hash 缓存（key: path|size|mtime），避免同会话多次重连时全量重算 blake3。 */
  private hashCache: Map<string, string> = new Map<string, string>();
  /** 有界任务队列（见类注释「并发有界」）。 */
  private taskQueue: WsTaskCreatedEvent[] = [];
  private queuedTaskIds: Set<number> = new Set<number>();
  private activeTaskCount: number = 0;
  /** 归位重试表 id；-1 = 没挂表（沙盒已空）。 */
  private flushTimerId: number = -1;

  /** 在 EntryAbility onCreate / MainShell aboutToAppear 调用 */
  start(context: common.Context): void {
    if (this.started) return;
    console.info('[云梯Boot] SyncEngine.start');
    this.ctx = context;
    this.started = true;
    wsManager.registerAuthListener();

    this.unregMsg = wsManager.onMessage((msg: WsMessage) => this.onMessage(msg));
    this.unregState = wsManager.onState((s: WsState, _: string) => {
      console.info(`[云梯Boot] ws state=${s}`);
      if (s === WsState.CONNECTED) {
        this.onConnected().catch((e: Error) => {
          console.error(`[SyncEngine] onConnected failed: ${e.message}`);
        });
      }
    });

    // 异步加载 folder 列表，并尝试连接 WS
    this.refreshFolders().catch((e: Error) => {
      console.warn(`[SyncEngine] refresh folders failed: ${e.message}`);
    });
    wsManager.connect().catch((e: Error) => {
      console.warn(`[SyncEngine] ws connect failed: ${e.message}`);
    });
  }

  /** 停止：断 WS、解注册 */
  stop(): void {
    if (!this.started) return;
    // 台账里可能还压着防抖窗口内的账，进程随时会被回收，先落盘
    syncBaseStore.flushNow().catch((e: Error) => {
      console.warn(`[SyncEngine] baseline flush on stop failed: ${e.message}`);
    });
    if (this.unregMsg !== null) { this.unregMsg(); this.unregMsg = null; }
    if (this.unregState !== null) { this.unregState(); this.unregState = null; }
    wsManager.unregisterAuthListener();
    wsManager.disconnect();
    this.stopFlushLoop();
    this.started = false;
  }

  /** 拉服务器 folder 列表 + 确保本地映射目录存在（须已授权云梯根目录） */
  async refreshFolders(): Promise<void> {
    try {
      const resp = await SyncApi.listFolders();
      this.folders = resp.data !== null ? resp.data : [];
      if (!cloudLadderStorage.isPublicWritable()) {
        // 后台/未授权：目录等回前台 flush 时按需建出，不在这里报错中断
        return;
      }
      // 为每个启用的 folder 创建本地映射目录（授权根/同步/<name>）
      for (let i = 0; i < this.folders.length; i++) {
        const f: SyncFolderInfo = this.folders[i];
        if (!f.enabled) continue;
        await cloudLadderStorage.ensureSyncFolder(f.name);
      }
    } catch (e) {
      console.warn(`[SyncEngine] listFolders failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  // ====== relative_path 处理（对标 Android sanitizeRel / remoteDirFor / joinRemote） ======

  /**
   * 消毒并拆分 relative_path：统一 '/'、去空段与 '.'、拒绝 '..' 穿越。
   * relative_path **含文件名**，故最后一段是叶子名，前面才是目录。
   * relative_path 为空（folder 根下的文件）时用 fallbackName 兜底。
   * 非法返回 null。
   */
  private splitRel(relativePath: string, fallbackName: string): RelParts | null {
    const raw: string[] = relativePath.replace(/\\/g, '/').split('/');
    const parts: string[] = [];
    for (let i = 0; i < raw.length; i++) {
      const p: string = raw[i];
      if (p.length === 0 || p === '.') continue;
      if (p === '..') return null;
      parts.push(p);
    }
    if (parts.length === 0) {
      if (fallbackName.length === 0) return null;
      const only: RelParts = { dir: '', name: fallbackName };
      return only;
    }
    const name: string = parts[parts.length - 1];
    const dir: string = parts.slice(0, parts.length - 1).join('/');
    const rp: RelParts = { dir, name };
    return rp;
  }

  /**
   * 远端目录 = folder.remote_path + relative_path 的目录部分。
   * 与 Android joinRemote 一致固定用 '/' 拼接——服务端 filepath.Join 会自行归一化，
   * Android 线上就是这么发的。
   */
  private joinRemote(remoteRoot: string, relDir: string): string {
    const root: string = remoteRoot.replace(/[\\/]+$/, '');
    return relDir.length === 0 ? root : `${root}/${relDir}`;
  }

  /** folder by id */
  private folderById(id: number): SyncFolderInfo | null {
    for (let i = 0; i < this.folders.length; i++) {
      if (this.folders[i].id === id) return this.folders[i];
    }
    return null;
  }

  // ====== WS 连接后追赶 ======

  private async onConnected(force: boolean = false): Promise<void> {
    // 串行化：WS 重连风暴时避免并发追赶
    if (this.catchUpRunning) return;
    this.catchUpRunning = true;
    try {
      const deviceId: string = deviceInfoSvc.getDeviceId();
      // 重连后刷新 folder 列表（服务端可能新增）
      await this.refreshFolders();

      // 前台的话，先把上一轮后台攒下的暂存搬进公共目录，再上报清单，
      // 否则 scan 会把「已下好但还在沙盒」的文件算成本地缺失。
      await this.flushStaging();

      // 台账镜像：沙盒被清（重装/清数据/换调试包）后从公共目录那份重建，
      // 否则清单为空 → 服务端全量重派。必须在任何 scan 之前完成。
      await this.ensureBaselineMirror();

      // 1) 拉本设备积压 pending 任务并入队（离线期间已派发未完成的）
      try {
        const resp = await SyncApi.pendingTasks(deviceId);
        const tasks: SyncTaskInfo[] = resp.data !== null ? resp.data : [];
        console.info(`[云梯Boot] pending tasks=${tasks.length}`);
        for (let i = 0; i < tasks.length; i++) {
          this.enqueueTask(this.toEvent(tasks[i]));
        }
      } catch (e) {
        console.warn(`[SyncEngine] pendingTasks fetch failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      }

      // 2) 扫描追赶（对标 Android catchUpFolder 的 Phase2）：逐 folder 把本地全量清单
      //    交服务端比对 trunk，补派离线期间遗漏的 download（trunk 有本地无）。
      //    download_only 客户端无本地探测，故不做 Phase1 的本地变更上传。
      for (let i = 0; i < this.folders.length; i++) {
        const f: SyncFolderInfo = this.folders[i];
        if (!f.enabled) continue;
        if (!syncMappingStore.isEnabled(f.id)) continue;
        try {
          await this.catchUpFolder(deviceId, f, force);
        } catch (e) {
          // 单个 folder 失败不影响其余 folder 的追赶。
          // ⚠ 走到这里意味着本轮**没有上报 scan**——清单残缺比不报更糟（服务端会重派整个 trunk），
          //   所以这是有意为之。但必须打成 error：它同时也是「服务端为什么一直重派」的答案。
          console.error(`[云梯Boot] scan 未上报 folder=${f.name}，本地清单构建失败: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
        }
      }
    } finally {
      this.catchUpRunning = false;
    }
  }

  /** 引擎是否已启动（MainShell 挂载后为 true）。 */
  isStarted(): boolean {
    return this.started;
  }

  /**
   * 手动触发一轮追赶（同步列表页「重新对齐」可用）。
   *
   * @param force 允许上报空清单 = 「我本地啥也没有，请把整个 folder 重发一遍」。
   *   自动追赶**永远不会**这么干（见 catchUpFolder 的空清单闸门），只有用户显式点重新对齐才行。
   */
  triggerCatchUp(force: boolean = false): void {
    this.onConnected(force).catch((e: Error) => {
      console.warn(`[SyncEngine] triggerCatchUp failed: ${e.message}`);
    });
  }

  /**
   * 把台账镜像挂到公共同步目录：`<Download/包名>/同步/.yt_baseline.json`。
   * 沙盒台账在 → 顺手补写镜像；沙盒空而镜像在 → 用镜像重建内存台账。
   */
  private async ensureBaselineMirror(): Promise<void> {
    if (!cloudLadderStorage.isReady()) return;
    try {
      await cloudLadderStorage.ensureDir(cloudLadderStorage.syncDir());
      await syncBaseStore.attachMirror(`${cloudLadderStorage.syncDir()}/${BASE_MIRROR_NAME}`);
    } catch (e) {
      console.warn(`[SyncEngine] attach baseline mirror failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  /**
   * 沙盒里只要还有货就周期性重试归位，清空即自动停表。
   *
   * 沙盒**只是后台写不进共享目录时的临时中转**，不是存储位置——任何时候留在里面的文件
   * 对用户都是不可见的，等于「下载了但没拿到」。所以不能只在几个离散时机（启动/回前台/
   * WS 连上/队列跑干）碰运气，中间的空档、以及部分条目 flush 失败的情况都得有人兜。
   */
  private ensureFlushLoop(): void {
    if (this.flushTimerId !== -1) return;
    if (syncStagingStore.pendingCount() === 0) return;
    this.flushTimerId = setInterval(() => {
      if (syncStagingStore.pendingCount() === 0) {
        this.stopFlushLoop();
        return;
      }
      this.flushStaging().catch((e: Error) => {
        console.warn(`[SyncEngine] loop flush failed: ${e.message}`);
      });
    }, FLUSH_RETRY_INTERVAL_MS);
  }

  private stopFlushLoop(): void {
    if (this.flushTimerId === -1) return;
    clearInterval(this.flushTimerId);
    this.flushTimerId = -1;
  }

  /**
   * 立刻尝试归位一次，并保证「只要还有剩就继续重试」。
   * 任何往沙盒里放东西的地方都该调它（见 executeDownload / executeMkdir）。
   */
  kickFlush(): void {
    this.flushStaging()
      .catch((e: Error) => { console.warn(`[SyncEngine] kick flush failed: ${e.message}`); })
      .finally(() => { this.ensureFlushLoop(); });
  }

  /**
   * 把沙盒暂存搬进公共共享目录。
   * @returns 归位的条目数
   */
  async flushStaging(): Promise<number> {
    if (!syncStagingStore.isReady()) return 0;
    if (syncStagingStore.pendingCount() === 0) {
      this.stopFlushLoop();
      return 0;
    }
    // 走 ensureWritable：授权只是失效而非真的拿不到时，前台能就地重取后立刻归位
    if (!await cloudLadderStorage.ensureWritable()) {
      this.ensureFlushLoop();   // 这次不行，挂上重试表
      return 0;
    }
    try {
      const moved: number = await syncStagingStore.flush();
      // 没搬干净（部分条目失败）就继续挂着重试，搬空了自动停表
      if (syncStagingStore.pendingCount() > 0) {
        this.ensureFlushLoop();
      } else {
        this.stopFlushLoop();
      }
      return moved;
    } catch (e) {
      console.warn(`[SyncEngine] flushStaging failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      this.ensureFlushLoop();
      return 0;
    }
  }

  /**
   * 构建本地全量清单并 POST /sync/scan，服务端比对 trunk 补派下载。
   *
   * **清单以基线台账（SyncBaseStore）为准，不以磁盘遍历为准。**
   * 原因见 SyncBaseStore 类注释：picker 临时授权的公共目录上 `fs.listFile` 不可靠，
   * 枚举失败就会报出空清单，服务端把整个 trunk 重派一遍
   * （现象：每次切前台全量重下，靠 hash 相同才被跳过，白空转一轮）。
   * 磁盘遍历降级成补充手段：成功就并进来（捡外部塞进来的文件），失败只记一笔，不影响上报。
   *
   * **空清单永远不自动上报**（除非 force）：见文末的闸门。空清单 = 告诉服务端「我啥也没有」
   * = 换来整个 trunk 的重派，这正是「鸿蒙一上线就全量拉一次」的唯一入口。
   */
  private async catchUpFolder(deviceId: string, folder: SyncFolderInfo, force: boolean = false): Promise<void> {
    if (!cloudLadderStorage.isReady()) {
      console.warn(`[SyncEngine] scan skipped for ${folder.name}: cloud dir not authorized`);
      return;
    }
    const localRoot: string = await cloudLadderStorage.ensureSyncFolder(folder.name);
    // 哨兵：既是「这个 folder 同步过」的标记，也是下面枚举自检的靶子
    const sentinelOk: boolean = await this.ensureSentinel(localRoot);
    const items: ScanItemDto[] = [];
    const seen: Set<string> = new Set<string>();

    // 1) 基线台账 —— 权威来源。
    //
    // ⚠ 存在性校验绝不能只信一个判据。`fs.listFile` 已经被证明在 picker 授权目录上不可靠，
    //   没有任何理由相信 `fs.access` 就一定可靠。而这里一旦误判，代价是**把台账整本擦掉**
    //   → 清单变空 → 服务端全量重派 → 又回到最初那个死循环，且台账再也回不来。
    //   所以：access 说没有时用 stat 再问一遍；两个都说没有才算数；
    //   并且如果"消失"的比例高得离谱（超过一半），判定为**校验手段坏了而不是文件没了**，
    //   本轮一条都不删、按台账原样上报。宁可多报也绝不少报。
    const base: BaseEntry[] = syncBaseStore.snapshot(folder.id);
    const alive: BaseEntry[] = [];
    const missing: BaseEntry[] = [];
    for (let i = 0; i < base.length; i++) {
      const b: BaseEntry = base[i];
      const full: string = `${localRoot}/${b.rel}`;
      let exists: boolean = false;
      try {
        exists = await fs.access(full);
      } catch (e) {
        exists = false;
      }
      if (!exists) {
        // 第二判据：stat 走的是另一条系统调用路径
        try {
          await fs.stat(full);
          exists = true;
        } catch (e2) {
          exists = false;
        }
      }
      if (exists) { alive.push(b); } else { missing.push(b); }
    }

    const suspicious: boolean = base.length > 0 && missing.length * 2 > base.length;
    if (suspicious) {
      console.error(`[云梯Boot] ${missing.length}/${base.length} 条台账判定为「文件不存在」，`
        + '比例异常，判定为存在性校验失效而非文件丢失：本轮不删任何台账，按原样上报');
    } else {
      for (let i = 0; i < missing.length; i++) {
        syncBaseStore.remove(folder.id, missing[i].rel);
      }
    }
    const reportBase: BaseEntry[] = suspicious ? base : alive;
    for (let i = 0; i < reportBase.length; i++) {
      const b: BaseEntry = reportBase[i];
      const idx: number = b.rel.lastIndexOf('/');
      const leaf: string = idx >= 0 ? b.rel.substring(idx + 1) : b.rel;
      const item: ScanItemDto = {
        relative_path: b.rel, file_name: leaf, file_size: b.size,
        file_hash: b.hash, is_dir: false, mtime: b.mtime,
      };
      items.push(item);
      seen.add(b.rel);
    }
    const vanished: number = suspicious ? 0 : missing.length;

    // 2) 磁盘遍历 —— 补充。能走通就并进来，走不通只记一笔，绝不因此放弃上报。
    let walkErr: string = '';
    const walked: ScanItemDto[] = [];
    try {
      await this.walk(localRoot, '', walked);
      for (let i = 0; i < walked.length; i++) {
        const it: ScanItemDto = walked[i];
        if (seen.has(it.relative_path)) continue;
        items.push(it);
        seen.add(it.relative_path);
        // 遍历到的文件顺手补进台账，下轮就不必再依赖遍历
        if (!it.is_dir && it.file_hash.length > 0) {
          syncBaseStore.set(folder.id, it.relative_path, it.file_hash, it.file_size, it.mtime);
        }
      }
    } catch (e) {
      walkErr = e instanceof Error ? e.message : JSON.stringify(e);
      console.warn(`[云梯Boot] 磁盘遍历失败（改用基线台账上报）: ${walkErr}`);
    }
    // 诊断：台账里明明有货，遍历却一项都没列出来——十有八九又是 listFile 在授权目录上静默返回空。
    // 自检一次把结论留在日志里：哪天真机数据证明 listFile 其实可靠，清单就能像安卓那样直接以磁盘为准，
    // 台账退回纯加速角色，这一整类问题（台账丢 = 清单丢）就不存在了。
    if (walkErr.length === 0 && walked.length === 0 && base.length > 0) {
      const trust: boolean = await this.canTrustEmptyListing(localRoot);
      console.warn(`[云梯Boot] 遍历 0 项而台账 ${base.length} 条，枚举自检=${trust ? '通过' : '不通过(listFile 不可信)'}`);
    }

    // 3) 沙盒暂存：已下好、只是还没搬进公共目录的，也算"本地有"
    this.mergeStaged(folder.name, items);

    // 4) 从文件 rel 反推父目录补进清单。
    //    服务端 trunk 把目录也当独立记录，清单里缺目录同样会 `!ok` → 每轮派一堆 mkdir。
    //    台账只记文件（目录没有 hash、没必要单独存），这里推导出来即可。
    const dirSet: Set<string> = new Set<string>();
    for (let i = 0; i < items.length; i++) {
      if (items[i].is_dir) { dirSet.add(items[i].relative_path); continue; }
      let p: string = items[i].relative_path;
      let idx: number = p.lastIndexOf('/');
      while (idx > 0) {
        p = p.substring(0, idx);
        if (dirSet.has(p)) break;
        dirSet.add(p);
        idx = p.lastIndexOf('/');
      }
    }
    dirSet.forEach((d: string) => {
      if (seen.has(d)) return;
      const i2: number = d.lastIndexOf('/');
      const leaf: string = i2 >= 0 ? d.substring(i2 + 1) : d;
      const dirItem: ScanItemDto = {
        relative_path: d, file_name: leaf, file_size: 0,
        file_hash: '', is_dir: true, mtime: 0,
      };
      items.push(dirItem);
      seen.add(d);
    });

    let fileCount: number = 0;
    for (let i = 0; i < items.length; i++) {
      if (!items[i].is_dir) fileCount++;
    }
    const summary: string = `文件=${fileCount} 目录=${items.length - fileCount} 台账=${base.length}`
      + ` 遍历=${walked.length} 镜像=${syncBaseStore.hasMirror() ? '在' : '无'}`
      + `${vanished > 0 ? ` 已失效=${vanished}` : ''}`
      + `${walkErr.length > 0 ? ' 遍历失败(用台账)' : ''}`;
    // 这一行是排查「服务端为什么一直重派」的唯一入口，过滤关键字：SCAN汇总
    console.info(`[云梯Boot] SCAN汇总 folder=${folder.name} ${summary}`);
    if (fileCount === 0) {
      console.error(`[云梯Boot] SCAN汇总 清单里一个文件都没有！台账=${base.length} 遍历错误=${walkErr}`);
    }
    flushLog.add(FlushEventKind.SCAN, folder.name, `清单上报 ${summary}`, walkErr);

    // ⭐ 空清单闸门 —— 「上线后无条件全量拉一次」的唯一入口就在这里。
    //
    // 空清单的语义是「我本地一个文件都没有」，服务端据此把整个 trunk 重派一遍。
    // 但空清单有两种可能的来源，代价天差地别：
    //   (a) 本地**真的**是空的（第一次同步 / 用户清空了目录）→ 全量补派是对的；
    //   (b) 台账丢了（沙盒被清）+ 枚举又不可信（listFile 在 picker 授权目录上会静默返回空）
    //       → 文件明明在磁盘上，却告诉服务端「我啥也没有」→ 白拉一整轮。
    // 光看 items.length 分不出这两种。所以用哨兵做**枚举自检**：哨兵一定在磁盘上，
    // listFile 连它都列不出来 → 这个目录上的「空」不可信 → 判定为 (b)，本轮不报。
    if (items.length === 0 && !force) {
      const trusted: boolean = sentinelOk && await this.canTrustEmptyListing(localRoot);
      if (!trusted) {
        console.error(`[云梯Boot] SCAN汇总 清单为空且无法自证本地确实为空`
          + `（哨兵=${sentinelOk ? '在' : '写不进'} 台账=${base.length} 遍历错误=${walkErr}），`
          + '跳过本轮 scan，避免服务端全量重派');
        flushLog.add(FlushEventKind.SCAN, folder.name, '清单为空且不可信，本轮不上报', walkErr);
        return;
      }
      console.warn(`[云梯Boot] ${folder.name} 本地确认为空（枚举自检通过），上报空清单，服务端将全量补派`);
    }
    // 上报前把台账刷到磁盘：这份清单就是按它建的，进程随时可能被冻死，别让两边对不上
    await syncBaseStore.flushNow();
    const params: SyncScanParams = { device_id: deviceId, folder_id: folder.id, items };
    await SyncApi.scan(params);
  }

  /**
   * 确保 folder 根下有哨兵文件，返回它是否确实躺在磁盘上。
   * 内容无意义（只写个时间戳），存在性才是全部价值。
   */
  private async ensureSentinel(localRoot: string): Promise<boolean> {
    const path: string = `${localRoot}/${SENTINEL_NAME}`;
    try {
      if (await fs.access(path)) return true;
    } catch (e) { /* 当作不存在，往下建 */ }
    try {
      const f: fs.File = await fs.open(path, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      try {
        await fs.write(f.fd, `${Date.now()}`);
      } finally {
        await fs.close(f.fd);
      }
      return true;
    } catch (e) {
      console.warn(`[SyncEngine] 哨兵写入失败 ${path}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      return false;
    }
  }

  /**
   * 枚举自检：`fs.listFile` 能列出哨兵，才认这个目录上的「列出来是空的」这个结论。
   * （picker 临时授权的公共目录上，listFile 会失败或静默返回空——这是本类多处绕着它走的原因。）
   */
  private async canTrustEmptyListing(localRoot: string): Promise<boolean> {
    try {
      const names: string[] = await fs.listFile(localRoot);
      for (let i = 0; i < names.length; i++) {
        if (names[i] === SENTINEL_NAME) return true;
      }
      console.error(`[云梯Boot] 枚举自检失败：listFile 列不出哨兵（返回 ${names.length} 项），`
        + '该目录上的枚举结果一律不可信');
      return false;
    } catch (e) {
      console.error(`[云梯Boot] 枚举自检失败：listFile 抛错 ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      return false;
    }
  }

  /** 把该 folder 的沙盒暂存条目并进 scan 清单（公共目录已有同路径则跳过）。 */
  private mergeStaged(folderName: string, items: ScanItemDto[]): void {
    const staged: StagedEntry[] = syncStagingStore.entriesFor(folderName);
    if (staged.length === 0) return;
    const seen: Set<string> = new Set<string>();
    for (let i = 0; i < items.length; i++) {
      seen.add(items[i].relative_path);
    }
    for (let i = 0; i < staged.length; i++) {
      const s: StagedEntry = staged[i];
      const rel: string = s.relativePath.length > 0 ? `${s.relativePath}/${s.fileName}` : s.fileName;
      if (seen.has(rel)) continue;
      const item: ScanItemDto = {
        relative_path: rel,
        file_name: s.fileName,
        file_size: s.size,
        file_hash: s.hash,
        is_dir: s.isDir,
        mtime: Math.floor(s.stagedAt / 1000),
      };
      items.push(item);
      seen.add(rel);
    }
  }

  /**
   * 递归遍历本地目录，收集目录项 + 文件项（文件带 blake3 hash）。
   *
   * ⚠ 任何一处失败都**必须抛出**，绝不能静默跳过。
   * scan 是全量比对：少报一个文件 = 服务端认为本地缺这个文件 = 重派 download。
   * 之前这里 listFile 失败直接 return、hash 算不出来直接 continue，
   * 结果是清单悄悄少一片，服务端每轮都重派同一批文件——流量哗哗跑、永不收敛，
   * 而且日志上一点痕迹都没有。宁可整轮 scan 不发（下一轮再来），也不能发残缺清单。
   */
  private async walk(dir: string, relPrefix: string, items: ScanItemDto[]): Promise<void> {
    let names: string[];
    try {
      names = await fs.listFile(dir);
    } catch (e) {
      const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
      throw new Error(`listFile 失败 dir=${dir}: ${msg}`);
    }
    for (let i = 0; i < names.length; i++) {
      const name: string = names[i];
      if (this.shouldIgnore(name)) continue;
      const full: string = `${dir}/${name}`;
      const rel: string = relPrefix.length === 0 ? name : `${relPrefix}/${name}`;
      let stat: fs.Stat;
      try {
        stat = await fs.stat(full);
      } catch (e) {
        const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
        throw new Error(`stat 失败 ${full}: ${msg}`);
      }
      if (stat.isDirectory()) {
        const dirItem: ScanItemDto = {
          relative_path: rel, file_name: name, file_size: 0,
          file_hash: '', is_dir: true, mtime: Math.floor(stat.mtime),
        };
        items.push(dirItem);
        await this.walk(full, rel, items);
      } else {
        const hash: string = await this.cachedHash(full, stat.size, Math.floor(stat.mtime));
        if (hash.length === 0) {
          // 算不出 hash 的文件如果就这么跳过，服务端会当它不存在而重派下载
          throw new Error(`hash 计算失败 ${full}（size=${stat.size}）`);
        }
        const fileItem: ScanItemDto = {
          relative_path: rel, file_name: name, file_size: stat.size,
          file_hash: hash, is_dir: false, mtime: Math.floor(stat.mtime),
        };
        items.push(fileItem);
      }
    }
  }

  /**
   * 忽略引擎自身的工作文件：临时目录（.synctmp 原子发布 / .syncpending 冲突隔离）
   * 与自用记账文件（哨兵 .ytsync、台账镜像 .yt_baseline.json）。
   * 这些进了清单会被服务端当成「本地多出来的文件」而派 delete。
   */
  private shouldIgnore(name: string): boolean {
    return name === '.synctmp' || name === '.syncpending' || name.startsWith('.yt');
  }

  /** 带进程内缓存的文件 blake3（size/mtime 未变则复用）。 */
  private async cachedHash(path: string, size: number, mtime: number): Promise<string> {
    const key: string = `${path}|${size}|${mtime}`;
    const cached: string | undefined = this.hashCache.get(key);
    if (cached !== undefined) return cached;
    try {
      const h: string = await fileHashHex(path);
      this.hashCache.set(key, h);
      return h;
    } catch (e) {
      console.error(`[云梯Boot] blake3 计算失败 ${path} (size=${size}): ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      return '';
    }
  }

  // ====== 消息分发 ======

  private onMessage(msg: WsMessage): void {
    const env: WsMessageEnvelope | null = msg.parsed;
    if (env === null) return;
    if (env.type !== 'file_sync') return;
    const content: Object | null | undefined = env.content;
    if (content === null || content === undefined) return;
    const event: string = (content as Record<string, Object>)['event'] as string;
    if (event === 'task_created') {
      this.enqueueTask(content as Object as WsTaskCreatedEvent);
    } else if (event === 'conflict') {
      this.onConflict(content as Object as WsConflictEvent).catch((e: Error) => {
        console.error(`[SyncEngine] conflict handle failed: ${e.message}`);
      });
    }
    // conflict_resolved 在 download_only 客户端没有 keep_local 回放，无操作
  }

  private async onConflict(ev: WsConflictEvent): Promise<void> {
    // download_only 客户端：默认 accept_server，本地已收敛为 trunk 版本（下次 download 会同步）
    try {
      await SyncApi.resolveConflict(ev.conflict_id, 'accept_server');
    } catch (e) {
      console.warn(`[SyncEngine] resolve conflict ${ev.conflict_id} failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  // ====== 任务队列（有界并发） ======

  /**
   * REST 形态的 task → 统一成 task_created 事件形状。
   *
   * ⚠ pendingTasks 的返回体里**没有 remote_dir**，必须自己算：
   *   folder.remote_path + relative_path 的目录部分。
   *   曾经直接填 folder.remote_path（folder 根），子目录里的文件就会去根目录找，
   *   服务端 os.Stat 落空 → 每个积压任务稳定 404（对标 Android remoteDirFor）。
   */
  private toEvent(t: SyncTaskInfo): WsTaskCreatedEvent {
    const folder: SyncFolderInfo | null = this.folderById(t.folder_id);
    let remoteDir: string = '';
    if (folder !== null) {
      const rp: RelParts | null = this.splitRel(t.relative_path, t.file_name);
      remoteDir = this.joinRemote(folder.remote_path, rp !== null ? rp.dir : '');
    }
    const ev: WsTaskCreatedEvent = {
      event: 'task_created',
      task_id: t.id,
      task_type: t.task_type,
      direction: t.direction,
      folder_id: t.folder_id,
      relative_path: t.relative_path,
      file_name: t.file_name,
      file_size: t.file_size,
      file_hash: t.file_hash,
      remote_path: '',
      remote_dir: remoteDir,
    };
    return ev;
  }

  /** 入队并按 MAX_CONCURRENT_TASKS 拉起（同 task_id 去重）。 */
  private enqueueTask(ev: WsTaskCreatedEvent): void {
    if (this.inFlightTaskIds.has(ev.task_id)) return;
    if (this.queuedTaskIds.has(ev.task_id)) return;
    this.queuedTaskIds.add(ev.task_id);
    this.taskQueue.push(ev);
    this.pump();
  }

  private pump(): void {
    while (this.activeTaskCount < MAX_CONCURRENT_TASKS && this.taskQueue.length > 0) {
      const ev: WsTaskCreatedEvent | undefined = this.taskQueue.shift();
      if (ev === undefined) break;
      this.queuedTaskIds.delete(ev.task_id);
      this.activeTaskCount++;
      this.runTask(ev)
        .catch((e: Error) => { console.error(`[SyncEngine] task ${ev.task_id} failed: ${e.message}`); })
        .finally(() => {
          this.activeTaskCount--;
          this.pump();
          // 一轮任务全部跑完：立刻把这轮落到沙盒的文件归位到共享目录，
          // 不必干等下一次前后台切换（用户可能全程停在前台）。
          if (this.activeTaskCount === 0 && this.taskQueue.length === 0) {
            this.kickFlush();
          }
        });
    }
  }

  private async runTask(ev: WsTaskCreatedEvent): Promise<void> {
    if (this.inFlightTaskIds.has(ev.task_id)) return;
    this.inFlightTaskIds.add(ev.task_id);
    try {
      let folder: SyncFolderInfo | null = this.folderById(ev.folder_id);
      if (folder === null) {
        // 可能是刚在服务端新建的 folder，本地列表还没刷到，刷一次再判
        await this.refreshFolders();
        folder = this.folderById(ev.folder_id);
      }
      if (folder === null) {
        await SyncApi.failTask(ev.task_id, `folder ${ev.folder_id} not mapped`);
        return;
      }
      // relative_path 含文件名，统一在这里拆成 目录 + 叶子名，下游一律用拆好的
      const rp: RelParts | null = this.splitRel(ev.relative_path, ev.file_name);
      if (rp === null) {
        await SyncApi.failTask(ev.task_id, `invalid relative_path: ${ev.relative_path}`);
        return;
      }
      // WS 事件的 remote_dir 由服务端算好（filepath.Dir），可信；缺失时按同样规则补
      if (ev.remote_dir === null || ev.remote_dir === undefined || ev.remote_dir.length === 0) {
        ev.remote_dir = this.joinRemote(folder.remote_path, rp.dir);
      }
      if (ev.task_type === 'download') {
        await this.executeDownload(ev.task_id, folder, ev, rp);
      } else if (ev.task_type === 'delete') {
        await this.executeDelete(ev.task_id, folder, rp);
      } else if (ev.task_type === 'mkdir') {
        await this.executeMkdir(ev.task_id, folder, rp);
      } else {
        await SyncApi.failTask(ev.task_id, `unsupported task_type: ${ev.task_type}`);
      }
    } finally {
      this.inFlightTaskIds.delete(ev.task_id);
    }
  }

  // ====== 任务执行 ======

  /**
   * 决定这次下载写到哪：前台且已授权 → 公共目录；否则 → 沙盒暂存。
   * 只拼路径 + 建父目录，不创建文件本身。
   */
  private async resolveTarget(folderName: string, relativePath: string, fileName: string): Promise<WriteTarget> {
    // ensureWritable 而非 isPublicWritable：授权失效时前台会**就地重取**，
    // 否则一次写失败就会把整个会话钉死在沙盒里（见 CloudLadderStorage.ensureWritable 注释）。
    if (await cloudLadderStorage.ensureWritable()) {
      const folderDir: string = await cloudLadderStorage.ensureSyncFolder(folderName);
      const dir: string = relativePath.length > 0 ? `${folderDir}/${relativePath}` : folderDir;
      await cloudLadderStorage.ensureDir(dir);
      const target: WriteTarget = { path: `${dir}/${fileName}`, staged: false };
      return target;
    }
    console.warn(`[SyncEngine] 公共目录不可写，${fileName} 落沙盒暂存待归位`);
    const staged: string = await syncStagingStore.ensureStageFile(folderName, relativePath, fileName);
    const target: WriteTarget = { path: staged, staged: true };
    return target;
  }

  /**
   * 本地是否已经有一份和期望 hash 一致的文件。
   *
   * 用 cachedHash（按 path|size|mtime 缓存）所以重复问不贵。
   * 期望 hash 为空时一律返回 false——没有依据就别跳过。
   */
  private async alreadyHave(folder: SyncFolderInfo, rp: RelParts, expectHash: string): Promise<boolean> {
    if (expectHash.length === 0) return false;
    if (!cloudLadderStorage.isReady()) return false;
    const path: string = this.localPathFor(cloudLadderStorage.syncFolderPath(folder.name), rp);
    try {
      const st: fs.Stat = await fs.stat(path);
      if (st.isDirectory()) return false;
      const h: string = await this.cachedHash(path, st.size, Math.floor(st.mtime));
      return h.length > 0 && h.toLowerCase() === expectHash.toLowerCase();
    } catch (e) {
      return false;   // 不存在
    }
  }

  /** 把一个已存在于本地的文件补进基线台账（老版本升级上来时台账是空的）。 */
  private async backfillBase(folder: SyncFolderInfo, rp: RelParts, rel: string, hash: string): Promise<void> {
    if (!cloudLadderStorage.isReady()) return;
    const path: string = this.localPathFor(cloudLadderStorage.syncFolderPath(folder.name), rp);
    try {
      const st: fs.Stat = await fs.stat(path);
      syncBaseStore.set(folder.id, rel, hash, st.size, Math.floor(st.mtime));
    } catch (e) { /* 拿不到就算了，下次下载时会记 */ }
  }

  /**
   * 下载一个文件。
   *
   * **先看本地是不是已经有了**：服务端的 scan 比对一旦有偏差就会反复重派同一批文件，
   * 傻下的话流量白跑、文件被反复重写（时间戳永远是"刚刚"），而且永远不收敛。
   * 本地已有同 hash 文件时直接 completeTask 并打一条 warn——
   * 那条 warn 就是「服务端在重复派发」的实锤，见到就该去查 scan 侧为什么对不上。
   *
   * 带重试：网络在传输中途被掐（进程冻结解冻、切网、服务端重启）是常态，
   * 一次失败就 failTask 会让这个文件在本轮同步里彻底消失——即「同步断了就不再继续」。
   * 这里退避重试 MAX_DOWNLOAD_ATTEMPTS 次，全败才上报 failTask（服务端下轮 pending 仍会再派）。
   */
  private async executeDownload(taskId: number, folder: SyncFolderInfo, ev: WsTaskCreatedEvent, rp: RelParts): Promise<void> {
    const expectHash: string = (ev.file_hash !== null && ev.file_hash !== undefined) ? ev.file_hash : '';
    const relForLog: string = rp.dir.length > 0 ? `${rp.dir}/${rp.name}` : rp.name;
    if (await this.alreadyHave(folder, rp, expectHash)) {
      console.warn(`[云梯Boot] 重复派发：本地已有同 hash 文件，跳过下载 rel=${relForLog} hash=${expectHash.substring(0, 12)}`);
      // 台账里没有才会被重派——顺手回填，下一轮 scan 就能正确上报，重派自然停止
      await this.backfillBase(folder, rp, relForLog, expectHash);
      await SyncApi.completeTask(taskId, expectHash);
      return;
    }
    console.info(`[云梯Boot] 下载 rel=${relForLog} size=${ev.file_size} wantHash=${expectHash.substring(0, 12)}`);
    let lastError: string = 'unknown';
    for (let attempt = 1; attempt <= MAX_DOWNLOAD_ATTEMPTS; attempt++) {
      const r: DownloadAttempt = await this.attemptDownload(folder, ev, rp);
      if (r.ok) {
        // 记账：落沙盒的登记待归位；落公共目录的清掉可能存在的旧暂存版本
        if (r.staged) {
          let size: number = ev.file_size;
          try { size = (await fs.stat(r.path)).size; } catch (e) { /* 用服务端给的 size */ }
          const entry: StagedEntry = {
            folderName: folder.name,
            relativePath: rp.dir,
            fileName: rp.name,
            stagedPath: r.path,
            size,
            hash: r.hash,
            isDir: false,
            stagedAt: Date.now(),
          };
          await syncStagingStore.record(entry);
          this.kickFlush();   // 刚落沙盒，立刻试着搬走，别等队列跑干
        } else {
          await syncStagingStore.drop(folder.name, rp.dir, rp.name);
        }
        // 记基线台账：scan 的清单以它为准（磁盘遍历在授权目录上不可靠）
        let mtimeSec: number = Math.floor(Date.now() / 1000);
        let sizeOnDisk: number = ev.file_size;
        try {
          const st: fs.Stat = await fs.stat(r.path);
          mtimeSec = Math.floor(st.mtime);
          sizeOnDisk = st.size;
        } catch (e) { /* 用服务端给的值兜底 */ }
        syncBaseStore.set(folder.id, relForLog, r.hash, sizeOnDisk, mtimeSec);
        await SyncApi.completeTask(taskId, r.hash);
        return;
      }
      lastError = r.error;
      if (r.fatal) break;
      if (attempt < MAX_DOWNLOAD_ATTEMPTS) {
        console.warn(`[SyncEngine] task ${taskId} attempt ${attempt} failed (${lastError}), retrying...`);
        await this.sleep(1000 * attempt);
      }
    }
    await SyncApi.failTask(taskId, `download failed after ${MAX_DOWNLOAD_ATTEMPTS} attempts: ${lastError}`);
  }

  /** 单次下载尝试：定目标 → 开文件 → 流式下载 → blake3 校验。 */
  private async attemptDownload(folder: SyncFolderInfo, ev: WsTaskCreatedEvent, rp: RelParts): Promise<DownloadAttempt> {
    let target: WriteTarget = { path: '', staged: false };
    let fd: number = -1;
    // 1) 先把目标文件打开——写不进公共目录（后台授权失效）要在**发起网络请求之前**就发现，
    //    否则白下一整个文件再失败。打不开就降级到沙盒暂存。
    let triedPublic: boolean = false;
    try {
      target = await this.resolveTarget(folder.name, rp.dir, rp.name);
      triedPublic = !target.staged;
      // 历史 bug 残留：目标位置可能是个同名目录，不清掉就永远写不进去、永远重派
      if (target.path.length > 0) {
        await cloudLadderStorage.clearBlockingPath(target.path, false);
      }
      if (target.path.length === 0) {
        return { ok: false, fatal: true, error: 'no writable target (cloud dir + staging both unavailable)', path: '', staged: false, hash: '' };
      }
      fd = (await this.openForWrite(target.path)).fd;
    } catch (e) {
      const first: string = e instanceof Error ? e.message : JSON.stringify(e);
      if (!triedPublic) {
        return { ok: false, fatal: true, error: `open staging target failed: ${first}`, path: '', staged: true, hash: '' };
      }
      // 公共目录打不开 = picker 授权已失效（多半是刚退到后台）→ 作废授权、改落沙盒
      console.warn(`[SyncEngine] open public target failed, fallback to staging: ${first}`);
      cloudLadderStorage.invalidate();
      const staged: string = await syncStagingStore.ensureStageFile(folder.name, rp.dir, rp.name);
      if (staged.length === 0) {
        return { ok: false, fatal: true, error: `open target failed: ${first}`, path: '', staged: true, hash: '' };
      }
      target = { path: staged, staged: true };
      try {
        fd = (await this.openForWrite(target.path)).fd;
      } catch (e2) {
        const m: string = e2 instanceof Error ? e2.message : JSON.stringify(e2);
        return { ok: false, fatal: true, error: `open staging failed: ${m}`, path: '', staged: true, hash: '' };
      }
    }

    // 2) 流式 Range 下载边下边写（不整块进内存）
    try {
      try {
        await this.downloadToFd(ev, rp, fd);
      } finally {
        try { await fs.close(fd); } catch (e) { /* ignore */ }
      }
    } catch (e) {
      const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
      try { await fs.unlink(target.path); } catch (e2) { /* ignore */ }
      // 4xx 是确定性拒绝（404 文件不存在 / 403 越权 / 400 参数错），重试只是白烧流量；
      // 其余（超时、连接被掐、5xx）才是可重试的瞬时故障。
      const fatal: boolean = this.isDeterministicHttpError(e);
      return { ok: false, fatal, error: `transfer failed: ${msg}`, path: target.path, staged: target.staged, hash: '' };
    }

    // 3) 校验 blake3：fileHashHex 走 native（子线程 mmap+rayon）优先，回退纯 ArkTS 异步分片，
    //    均不占主线程——绝不能对整块 buffer 在主线程同步算（大文件会 THREAD_BLOCK 卡死闪退）。
    const hasHash: boolean = ev.file_hash !== null && ev.file_hash !== undefined && ev.file_hash.length > 0;
    let localHash: string = '';
    try {
      localHash = await fileHashHex(target.path);
    } catch (e) {
      const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
      try { await fs.unlink(target.path); } catch (e2) { /* ignore */ }
      return { ok: false, fatal: false, error: `hash failed: ${msg}`, path: target.path, staged: target.staged, hash: '' };
    }
    if (hasHash && localHash.toLowerCase() !== (ev.file_hash as string).toLowerCase()) {
      try { await fs.unlink(target.path); } catch (e) { /* ignore */ }
      // 多半是传输被掐导致的截断文件，重试一次往往就好，故不算 fatal
      return {
        ok: false, fatal: false,
        error: `hash mismatch: got ${localHash}, want ${ev.file_hash}`,
        path: target.path, staged: target.staged, hash: '',
      };
    }

    return {
      ok: true, fatal: false, error: '',
      path: target.path, staged: target.staged,
      hash: hasHash ? (ev.file_hash as string) : localHash,
    };
  }

  /**
   * 是否为「重试也不会变」的 HTTP 错误（4xx，除 408 超时 / 429 限流）。
   * 这类错误重试纯属浪费：404 说明服务端根本没这个文件，得靠下一轮 scan 让 trunk 自愈。
   */
  private isDeterministicHttpError(e: Object): boolean {
    if (!(e instanceof ResponseException)) return false;
    const code: number = (e as ResponseException).code;
    if (code === 408 || code === 429) return false;
    return code >= 400 && code < 500;
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve: () => void) => { setTimeout(resolve, ms); });
  }

  private async openForWrite(path: string): Promise<fs.File> {
    return fs.open(path, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
  }

  /**
   * 流式下载到已打开的 fd：Range 分片顺序追加，峰值内存 = 一片大小。
   * 服务端不支持 Range（或探测不到长度）时才回退整块下载。
   */
  private async downloadToFd(ev: WsTaskCreatedEvent, rp: RelParts, fd: number): Promise<void> {
    // 服务端 /file/download 做的是 filepath.Join(path, name)，
    // 所以 path 必须是**文件所在目录**、name 必须是叶子文件名。
    const query: Record<string, string> = {
      'path': ev.remote_dir,
      'name': rp.name,
      'device_id': '',
    };

    // 小文件（≤ 一片）直接整块下，省掉一次 probe 往返——同步绝大多数是小文件
    let total: number = 0;
    let acceptRanges: boolean = false;
    if (ev.file_size <= 0 || ev.file_size > DOWNLOAD_CHUNK_BYTES) {
      try {
        const probe = await Request.probeDownload('/file/download', query);
        total = probe.totalBytes;
        acceptRanges = probe.acceptRanges;
      } catch (e) {
        console.warn(`[SyncEngine] probe failed, fallback to full download: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      }
    }

    if (!acceptRanges || total <= 0) {
      const buf: ArrayBuffer = await Request.downloadBuffer('/file/download', query);
      if (buf.byteLength > 0) {
        await fs.write(fd, buf);
      }
      return;
    }

    let received: number = 0;
    while (received < total) {
      const end: number = Math.min(received + DOWNLOAD_CHUNK_BYTES - 1, total - 1);
      const buf: ArrayBuffer = await Request.downloadRange('/file/download', query, received, end);
      if (buf.byteLength === 0) {
        throw new Error(`empty range response at offset ${received}/${total}`);
      }
      await fs.write(fd, buf);
      received += buf.byteLength;
    }
  }

  /** 本地落盘路径：<同步/folder>/<rel 目录>/<叶子名>（对标 Android File(localRoot, rel)）。 */
  private localPathFor(folderDir: string, rp: RelParts): string {
    return rp.dir.length > 0 ? `${folderDir}/${rp.dir}/${rp.name}` : `${folderDir}/${rp.name}`;
  }

  private async executeDelete(taskId: number, folder: SyncFolderInfo, rp: RelParts): Promise<void> {
    try {
      // 公共目录里的副本（授权有效时）
      if (cloudLadderStorage.isReady()) {
        const path: string = this.localPathFor(cloudLadderStorage.syncFolderPath(folder.name), rp);
        try {
          await fs.unlink(path);
        } catch (e) {
          // 不存在视为已删
        }
      }
      // 还压在沙盒里没归位的副本，一并删掉，免得前台 flush 把已删文件又搬回共享目录
      await syncStagingStore.drop(folder.name, rp.dir, rp.name);
      syncBaseStore.remove(folder.id, rp.dir.length > 0 ? `${rp.dir}/${rp.name}` : rp.name);
      await SyncApi.completeTask(taskId, '');
    } catch (e) {
      await SyncApi.failTask(taskId, `delete failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  private async executeMkdir(taskId: number, folder: SyncFolderInfo, rp: RelParts): Promise<void> {
    try {
      // 与 download 用同一判据，否则会出现「文件进了共享目录、目录却建在沙盒」的错位
      if (await cloudLadderStorage.ensureWritable()) {
        const folderDir: string = await cloudLadderStorage.ensureSyncFolder(folder.name);
        const dirPath: string = this.localPathFor(folderDir, rp);
        await cloudLadderStorage.clearBlockingPath(dirPath, true);
        await cloudLadderStorage.ensureDir(dirPath);
      } else {
        // 后台：沙盒里占位，前台 flush 时补建到公共目录（保留空目录结构）
        await syncStagingStore.recordDir(folder.name, rp.dir, rp.name);
        this.kickFlush();
      }
      await SyncApi.completeTask(taskId, '');
    } catch (e) {
      await SyncApi.failTask(taskId, `mkdir failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }
}

export const syncEngine = new SyncEngineClass();
