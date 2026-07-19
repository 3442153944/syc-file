import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';
import { appConfig } from '../app/AppConfig';
import { deviceInfoSvc } from '../util/DeviceInfo';
import { wsManager, WsState, WsMessage } from '../net/WebSocketManager';
import { SyncApi } from '../api/sync/SyncApi';
import type { SyncFolderInfo, WsTaskCreatedEvent, WsConflictEvent, WsMessageEnvelope, SyncTaskInfo } from '../api/sync/SyncTypes';
import { FileApi } from '../api/file/FileApi';
import { Request } from '../net/Request';
import { fileHashHex } from '../core/FileCore';

/**
 * SyncEngine
 * 鸿蒙端同步引擎（download_only），对标 Android sync/SyncEngine.kt + 桌面端 sync_engine.rs。
 *
 * 与 Android/桌面端的关键差异（鸿蒙无 root、无文件监听能力）：
 *  - **只执行不探测**：处理 S→C 的 task_created（download/delete/mkdir），不监听本地变更、不上报 file_changed。
 *  - **冲突策略**：默认 accept_server（download_only 客户端没有 watcher 能把本地分叉回放重传）。
 *  - **离线追赶**：WS Connected 时拉本设备 pending 任务执行（服务端按 device_id 过滤）。
 *  - **原子发布**：download 写 .synctmp → 校验 blake3 → rename 到主目录；被占用回 task_blocked。
 *
 * 本地同步根目录：${ctx.filesDir}/sync/<folder_name>/。
 */

const SYNC_ROOT: string = 'sync'; // 沙箱内相对 ctx.filesDir

class SyncEngineClass {
  private ctx: common.Context | null = null;
  private started: boolean = false;
  private folders: SyncFolderInfo[] = [];
  private unregMsg: (() => void) | null = null;
  private unregState: (() => void) | null = null;
  private inFlightTaskIds: Set<number> = new Set<number>();

  /** 在 EntryAbility onCreate / MainShell aboutToAppear 调用 */
  start(context: common.Context): void {
    if (this.started) return;
    this.ctx = context;
    this.started = true;
    wsManager.registerAuthListener();

    this.unregMsg = wsManager.onMessage((msg: WsMessage) => this.onMessage(msg));
    this.unregState = wsManager.onState((s: WsState, _: string) => {
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

  /** 拉服务器 folder 列表 + 确保本地映射目录存在 */
  async refreshFolders(): Promise<void> {
    try {
      const resp = await SyncApi.listFolders();
      this.folders = resp.data !== null ? resp.data : [];
      // 为每个 folder 创建本地映射目录
      for (let i = 0; i < this.folders.length; i++) {
        const f: SyncFolderInfo = this.folders[i];
        if (!f.enabled) continue;
        const localDir: string = this.localDirFor(f);
        try {
          if (!fs.accessSync(localDir)) {
            fs.mkdirSync(localDir, true);
          }
        } catch (e) { /* ignore */ }
      }
    } catch (e) {
      console.warn(`[SyncEngine] listFolders failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  /** folder → 本地映射目录 */
  private localDirFor(f: SyncFolderInfo): string {
    const base: string = this.ctx !== null ? `${this.ctx.filesDir}/${SYNC_ROOT}` : SYNC_ROOT;
    return `${base}/${f.name}`;
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
    const deviceId: string = deviceInfoSvc.getDeviceId();
    // 重连后刷新 folder 列表（服务端可能新增）
    await this.refreshFolders();
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
    const localDir: string = `${this.localDirFor(folder)}/${ev.relative_path}`;
    const tmpDir: string = `${this.localDirFor(folder)}/.synctmp`;
    const tmpPath: string = `${tmpDir}/${ev.file_name}`;
    const finalPath: string = `${localDir}/${ev.file_name}`;
    try {
      // 准备目录
      try { fs.mkdirSync(localDir, true); } catch (e) { /* ignore */ }
      try { fs.mkdirSync(tmpDir, true); } catch (e) { /* ignore */ }

      // 流式下载整个文件到 tmpPath
      const buf: ArrayBuffer = await Request.downloadBuffer('/file/download', {
        'path': ev.remote_dir,
        'name': ev.file_name,
        'device_id': '',
      });
      const file: fs.File = fs.openSync(tmpPath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
      fs.writeSync(file.fd, buf);
      fs.closeSync(file);

      // 校验 blake3
      if (ev.file_hash !== null && ev.file_hash !== undefined && ev.file_hash.length > 0) {
        const localHash: string = await fileHashHex(tmpPath);
        if (localHash.toLowerCase() !== ev.file_hash.toLowerCase()) {
          // 删 tmp + 报失败
          try { fs.unlinkSync(tmpPath); } catch (e) { /* ignore */ }
          await SyncApi.failTask(taskId, `hash mismatch: got ${localHash}, want ${ev.file_hash}`);
          return;
        }
      }

      // 原子 rename 到主目录
      try {
        fs.renameSync(tmpPath, finalPath);
      } catch (e) {
        // 目标被占用 → blocked（不计入重试次数）
        try { fs.unlinkSync(tmpPath); } catch (e2) { /* ignore */ }
        await SyncApi.blockTask(taskId, 'target locked');
        return;
      }

      // 回传落盘 hash 供服务端校验
      const hashHex: string = ev.file_hash !== null && ev.file_hash !== undefined && ev.file_hash.length > 0
        ? ev.file_hash
        : await fileHashHex(finalPath);
      await SyncApi.completeTask(taskId, hashHex);
    } catch (e) {
      const msg: string = e instanceof Error ? e.message : JSON.stringify(e);
      await SyncApi.failTask(taskId, `download failed: ${msg}`);
    }
  }

  private async executeDelete(taskId: number, folder: SyncFolderInfo, ev: WsTaskCreatedEvent): Promise<void> {
    const path: string = `${this.localDirFor(folder)}/${ev.relative_path}/${ev.file_name}`;
    try {
      try {
        if (fs.accessSync(path)) {
          fs.unlinkSync(path);
        }
      } catch (e) {
        // 不存在视为已删
      }
      await SyncApi.completeTask(taskId, '');
    } catch (e) {
      await SyncApi.failTask(taskId, `delete failed: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    }
  }

  private async executeMkdir(taskId: number, folder: SyncFolderInfo, ev: WsTaskCreatedEvent): Promise<void> {
    const path: string = `${this.localDirFor(folder)}/${ev.relative_path}/${ev.file_name}`;
    try {
      try { fs.mkdirSync(path, true); } catch (e) { /* ignore if exists */ }
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
