import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';
import { deviceInfoSvc } from '../util/DeviceInfo';
import { wsManager, WsState, WsMessage } from '../net/WebSocketManager';
import { SyncApi } from '../api/sync/SyncApi';
import type { SyncFolderInfo, WsTaskCreatedEvent, WsConflictEvent, WsMessageEnvelope, SyncTaskInfo, ScanItemDto, SyncScanParams } from '../api/sync/SyncTypes';
import { Request } from '../net/Request';
import { fileHashHex, hashBufferHex } from '../core/FileCore';
import { syncMappingStore } from './SyncMappingStore';
import { cloudLadderStorage } from '../storage/CloudLadderStorage';

/**
 * SyncEngine
 * 鸿蒙端同步引擎（download_only），对标 Android sync/SyncEngine.kt + 桌面端 sync_engine.rs。
 *
 * 与 Android/桌面端的关键差异（鸿蒙无 root、无文件监听能力）：
 *  - **只执行不探测**：处理 S→C 的 task_created（download/delete/mkdir），不监听本地变更、不上报 file_changed。
 *  - **冲突策略**：默认 accept_server（download_only 客户端没有 watcher 能把本地分叉回放重传）。
 *  - **离线追赶**：WS Connected 时拉本设备 pending 任务 + scan 全量清单比对补派（对标 Android Phase2）。
 *  - **落盘校验**：download 整块下载到内存 → 对 buffer 直接算 blake3 校验 → 写授权目录（无原子 rename）。
 *
 * 本地同步根目录：用户授权的「云梯根」/同步/<folder_name>/（docs:// URI，用户可在文件管理器查看/操控）。
 * 鸿蒙手机不支持公共固定目录，须用户先在设置里选一个文件夹授权，见 storage/CloudLadderStorage。
 * native 内核 mmap 不认 docs URI，故授权目录内文件的 hash 走纯 ArkTS（fileHashHex 自动回退）/
 * 下载校验直接对内存 buffer 算（hashBufferHex）。
 */

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
    if (this.unregMsg !== null) { this.unregMsg(); this.unregMsg = null; }
    if (this.unregState !== null) { this.unregState(); this.unregState = null; }
    wsManager.unregisterAuthListener();
    wsManager.disconnect();
    this.started = false;
  }

  /** 拉服务器 folder 列表 + 确保本地映射目录存在（须已授权云梯根目录） */
  async refreshFolders(): Promise<void> {
    try {
      const resp = await SyncApi.listFolders();
      this.folders = resp.data !== null ? resp.data : [];
      if (!cloudLadderStorage.hasRoot()) {
        console.warn('[SyncEngine] cloud root not set, skip creating sync dirs');
        return;
      }
      await cloudLadderStorage.ensureActivated();
      // 为每个启用的 folder 创建本地映射目录（授权根/同步/<name>）
      for (let i = 0; i < this.folders.length; i++) {
        const f: SyncFolderInfo = this.folders[i];
        if (!f.enabled) continue;
        cloudLadderStorage.ensureSyncFolderDir(f.name);
      }
    } catch (e) {
      console.warn(`[SyncEngine] listFolders failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  /** folder → 本地映射目录 URI（授权根/同步/<name>；未授权根目录时返回空串）。 */
  private localDirFor(f: SyncFolderInfo): string {
    if (!cloudLadderStorage.hasRoot()) return '';
    return cloudLadderStorage.syncFolderDir(f.name);
  }

  /** folder by id */
  private folderById(id: number): SyncFolderInfo | null {
    for (let i = 0; i < this.folders.length; i++) {
      if (this.folders[i].id === id) return this.folders[i];
    }
    return null;
  }

  // ====== WS 连接后追赶 ======

  private async onConnected(): Promise<void> {
    // 串行化：WS 重连风暴时避免并发追赶
    if (this.catchUpRunning) return;
    this.catchUpRunning = true;
    try {
      const deviceId: string = deviceInfoSvc.getDeviceId();
      // 重连后刷新 folder 列表（服务端可能新增）
      await this.refreshFolders();

      // 未授权云梯根目录：无处落盘，整轮追赶跳过（用户在设置里选目录后重连会重新追赶）
      if (!cloudLadderStorage.hasRoot()) {
        console.warn('[云梯Boot] catchUp skipped: cloud root not set');
        return;
      }
      await cloudLadderStorage.ensureActivated();

      // 1) 拉本设备积压 pending 任务并执行（离线期间已派发未完成的）
      try {
        const resp = await SyncApi.pendingTasks(deviceId);
        const tasks: SyncTaskInfo[] = resp.data !== null ? resp.data : [];
        for (let i = 0; i < tasks.length; i++) {
          const t: SyncTaskInfo = tasks[i];
          this.executeTask(t).catch((e: Error) => {
            console.error(`[SyncEngine] pending task ${t.id} exec failed: ${e.message}`);
          });
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
          await this.catchUpFolder(deviceId, f);
        } catch (e) {
          console.warn(`[SyncEngine] catchUp folder ${f.name} failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
        }
      }
    } finally {
      this.catchUpRunning = false;
    }
  }

  /** 手动触发一轮追赶（同步列表页「重新对齐」可用）。 */
  triggerCatchUp(): void {
    this.onConnected().catch((e: Error) => {
      console.warn(`[SyncEngine] triggerCatchUp failed: ${e.message}`);
    });
  }

  /** 扫描本地同步目录，构建全量清单 POST /sync/scan，服务端比对 trunk 补派下载。 */
  private async catchUpFolder(deviceId: string, folder: SyncFolderInfo): Promise<void> {
    const localRoot: string = cloudLadderStorage.ensureSyncFolderDir(folder.name);
    const items: ScanItemDto[] = [];
    await this.walk(localRoot, '', items);
    console.info(`[云梯Boot] scan catchUp folder=${folder.name} localRoot=${localRoot} items=${items.length}`);
    const params: SyncScanParams = { device_id: deviceId, folder_id: folder.id, items };
    await SyncApi.scan(params);
  }

  /** 递归遍历本地目录，收集目录项 + 文件项（文件带 blake3 hash）。 */
  private async walk(dir: string, relPrefix: string, items: ScanItemDto[]): Promise<void> {
    let names: string[];
    try {
      names = fs.listFileSync(dir);
    } catch (e) {
      return;
    }
    for (let i = 0; i < names.length; i++) {
      const name: string = names[i];
      if (this.shouldIgnore(name)) continue;
      const full: string = `${dir}/${name}`;
      const rel: string = relPrefix.length === 0 ? name : `${relPrefix}/${name}`;
      let stat: fs.Stat;
      try {
        stat = fs.statSync(full);
      } catch (e) {
        continue;
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
        if (hash.length === 0) continue;
        const fileItem: ScanItemDto = {
          relative_path: rel, file_name: name, file_size: stat.size,
          file_hash: hash, is_dir: false, mtime: Math.floor(stat.mtime),
        };
        items.push(fileItem);
      }
    }
  }

  /** 忽略引擎自身的临时工作目录（.synctmp 原子发布 / .syncpending 冲突隔离）。 */
  private shouldIgnore(name: string): boolean {
    return name === '.synctmp' || name === '.syncpending';
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
      console.warn(`[SyncEngine] hash failed for ${path}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
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
      this.onTaskCreated(content as Object as WsTaskCreatedEvent).catch((e: Error) => {
        console.error(`[SyncEngine] task_created exec failed: ${e.message}`);
      });
    } else if (event === 'conflict') {
      this.onConflict(content as Object as WsConflictEvent).catch((e: Error) => {
        console.error(`[SyncEngine] conflict handle failed: ${e.message}`);
      });
    }
    // conflict_resolved 在 download_only 客户端没有 keep_local 回放，无操作
  }

  private async onTaskCreated(ev: WsTaskCreatedEvent): Promise<void> {
    const taskId: number = ev.task_id;
    if (this.inFlightTaskIds.has(taskId)) return;
    this.inFlightTaskIds.add(taskId);
    try {
      const folder: SyncFolderInfo | null = this.folderById(ev.folder_id);
      if (folder === null) {
        await SyncApi.failTask(taskId, `folder ${ev.folder_id} not mapped`);
        return;
      }
      if (ev.task_type === 'download') {
        await this.executeDownload(taskId, folder, ev);
      } else if (ev.task_type === 'delete') {
        await this.executeDelete(taskId, folder, ev);
      } else if (ev.task_type === 'mkdir') {
        await this.executeMkdir(taskId, folder, ev);
      } else {
        await SyncApi.failTask(taskId, `unsupported task_type: ${ev.task_type}`);
      }
    } finally {
      this.inFlightTaskIds.delete(taskId);
    }
  }

  private async onConflict(ev: WsConflictEvent): Promise<void> {
    // download_only 客户端：默认 accept_server，本地已收敛为 trunk 版本（下次 download 会同步）
    try {
      await SyncApi.resolveConflict(ev.conflict_id, 'accept_server');
    } catch (e) {
      console.warn(`[SyncEngine] resolve conflict ${ev.conflict_id} failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  // ====== 任务执行 ======

  private async executeDownload(taskId: number, folder: SyncFolderInfo, ev: WsTaskCreatedEvent): Promise<void> {
    if (!cloudLadderStorage.hasRoot()) {
      await SyncApi.failTask(taskId, 'cloud root not set');
      return;
    }
    await cloudLadderStorage.ensureActivated();
    // 目标目录 = 授权根/同步/<folder>/<relative_path>，逐层建出
    const folderDir: string = cloudLadderStorage.ensureSyncFolderDir(folder.name);
    const dirUri: string = ev.relative_path.length > 0
      ? cloudLadderStorage.mkdirpUnder(folderDir, ev.relative_path)
      : folderDir;
    const finalPath: string = `${dirUri}/${ev.file_name}`;
    try {
      // 整块下载到内存
      const buf: ArrayBuffer = await Request.downloadBuffer('/file/download', {
        'path': ev.remote_dir,
        'name': ev.file_name,
        'device_id': '',
      });

      // 校验 blake3：对内存 buffer 直接算（授权目录是 docs URI，native mmap 不认；且省一次读回）
      const hasHash: boolean = ev.file_hash !== null && ev.file_hash !== undefined && ev.file_hash.length > 0;
      const localHash: string = hashBufferHex(new Uint8Array(buf));
      if (hasHash && localHash.toLowerCase() !== (ev.file_hash as string).toLowerCase()) {
        await SyncApi.failTask(taskId, `hash mismatch: got ${localHash}, want ${ev.file_hash}`);
        return;
      }

      // 写入授权目录（直接写目标：内容一次性 write，非流式，半写风险低；失败下轮 scan 会重下）
      const file: fs.File = fs.openSync(finalPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      try {
        fs.writeSync(file.fd, buf);
      } finally {
        fs.closeSync(file);
      }

      await SyncApi.completeTask(taskId, hasHash ? (ev.file_hash as string) : localHash);
    } catch (e) {
      const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
      await SyncApi.failTask(taskId, `download failed: ${msg}`);
    }
  }

  private async executeDelete(taskId: number, folder: SyncFolderInfo, ev: WsTaskCreatedEvent): Promise<void> {
    if (!cloudLadderStorage.hasRoot()) {
      await SyncApi.failTask(taskId, 'cloud root not set');
      return;
    }
    await cloudLadderStorage.ensureActivated();
    const folderDir: string = cloudLadderStorage.syncFolderDir(folder.name);
    const path: string = ev.relative_path.length > 0
      ? `${folderDir}/${ev.relative_path}/${ev.file_name}`
      : `${folderDir}/${ev.file_name}`;
    try {
      try {
        fs.unlinkSync(path);
      } catch (e) {
        // 不存在视为已删
      }
      await SyncApi.completeTask(taskId, '');
    } catch (e) {
      await SyncApi.failTask(taskId, `delete failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  private async executeMkdir(taskId: number, folder: SyncFolderInfo, ev: WsTaskCreatedEvent): Promise<void> {
    if (!cloudLadderStorage.hasRoot()) {
      await SyncApi.failTask(taskId, 'cloud root not set');
      return;
    }
    await cloudLadderStorage.ensureActivated();
    const folderDir: string = cloudLadderStorage.ensureSyncFolderDir(folder.name);
    const rel: string = ev.relative_path.length > 0 ? `${ev.relative_path}/${ev.file_name}` : ev.file_name;
    try {
      cloudLadderStorage.mkdirpUnder(folderDir, rel);
      await SyncApi.completeTask(taskId, '');
    } catch (e) {
      await SyncApi.failTask(taskId, `mkdir failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  // ====== 直接执行 REST 形态的 task（离线追赶用） ======

  private async executeTask(t: SyncTaskInfo): Promise<void> {
    if (this.inFlightTaskIds.has(t.id)) return;
    this.inFlightTaskIds.add(t.id);
    try {
      const folder: SyncFolderInfo | null = this.folderById(t.folder_id);
      if (folder === null) {
        await SyncApi.failTask(t.id, `folder ${t.folder_id} not mapped`);
        return;
      }
      // 构造类似 task_created 的事件
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
        remote_dir: folder.remote_path,
      };
      if (t.task_type === 'download') {
        await this.executeDownload(t.id, folder, ev);
      } else if (t.task_type === 'delete') {
        await this.executeDelete(t.id, folder, ev);
      } else if (t.task_type === 'mkdir') {
        await this.executeMkdir(t.id, folder, ev);
      } else {
        await SyncApi.failTask(t.id, `unsupported task_type: ${t.task_type}`);
      }
    } finally {
      this.inFlightTaskIds.delete(t.id);
    }
  }
}

export const syncEngine = new SyncEngineClass();
