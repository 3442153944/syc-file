import { common } from '@kit.AbilityKit';
import { readTextFile, writeTextFileAtomic } from './JsonFileStore';

/**
 * FlushLog
 * 「文件归位」流水账：沙盒暂存 → 共享目录 的搬运记录。
 *
 * 为什么要有它：沙盒是中转不是存储，留在里面的文件对用户完全不可见。
 * 光有一个「待归位 N 个」的数字，出问题时说不清是卡在哪一步、失败原因是什么。
 * 这里把每条 落沙盒/归位成功/归位失败 都记下来，首页直接展示：
 * 搬运中就是实时消息流，空闲时就是可翻的历史。
 *
 * 只留最近 MAX_EVENTS 条；持久化落沙盒文件（200 条带文件名轻松几十 KB，
 * 不适合塞 preferences，见 storage/JsonFileStore）并做了防抖。
 */

export enum FlushEventKind {
  /** 写不进共享目录，先落沙盒暂存 */
  STAGED = 'staged',
  /** 已从沙盒搬进共享目录 */
  MOVED = 'moved',
  /** 搬运失败，会自动重试 */
  FAILED = 'failed',
  /** 清单上报（scan）结果——服务端凭它判断本地缺什么，出问题时最该看的一条 */
  SCAN = 'scan',
}

export interface FlushEvent {
  id: number;
  kind: string;
  folderName: string;
  fileName: string;
  /** 失败原因等补充信息，可为空串 */
  detail: string;
  /** epoch ms */
  at: number;
}

/** AppStorage key：搬运流水（新的在前），供首页 @StorageLink 订阅。 */
export const FLUSH_LOG_KEY = 'sync_flush_log';
/** AppStorage key：当前是否正在搬运——true 时首页按「消息流」展示。 */
export const FLUSH_ACTIVE_KEY = 'sync_flush_active';

const MAX_EVENTS = 200;
const LOG_FILE = 'sync_flush_log.json';
const SAVE_DEBOUNCE_MS = 2000;

class FlushLogClass {
  private events: FlushEvent[] = [];
  private nextId: number = 1;
  private filePath: string = '';
  private saveTimer: number = -1;

  async init(ctx: common.Context): Promise<void> {
    this.filePath = `${ctx.filesDir}/${LOG_FILE}`;
    const raw: string = await readTextFile(this.filePath);
    if (raw.length > 0) {
      try {
        const arr = JSON.parse(raw) as FlushEvent[];
        this.events = arr;
        for (let i = 0; i < arr.length; i++) {
          if (arr[i].id >= this.nextId) this.nextId = arr[i].id + 1;
        }
      } catch (e) {
        console.error(`[FlushLog] 流水解析失败，按空处理: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
      }
    }
    this.publish();
    AppStorage.setOrCreate<boolean>(FLUSH_ACTIVE_KEY, false);
  }

  /** 追加一条（新的在前）。 */
  add(kind: FlushEventKind, folderName: string, fileName: string, detail: string): void {
    const ev: FlushEvent = {
      id: this.nextId++,
      kind: kind as string,
      folderName,
      fileName,
      detail,
      at: Date.now(),
    };
    this.events.unshift(ev);
    if (this.events.length > MAX_EVENTS) {
      this.events = this.events.slice(0, MAX_EVENTS);
    }
    this.publish();
    this.scheduleSave();
  }

  /** 搬运中标记：首页据此在「消息流」和「历史」之间切换展示。 */
  setActive(v: boolean): void {
    AppStorage.setOrCreate<boolean>(FLUSH_ACTIVE_KEY, v);
  }

  getEvents(): FlushEvent[] {
    return this.events;
  }

  async clear(): Promise<void> {
    this.events = [];
    this.publish();
    if (this.filePath.length > 0) {
      await writeTextFileAtomic(this.filePath, '[]');
    }
  }

  /** 换引用才能触发 @StorageLink 刷新。 */
  private publish(): void {
    AppStorage.setOrCreate<FlushEvent[]>(FLUSH_LOG_KEY, this.events.slice());
  }

  /** 防抖持久化：一次搬几百个文件时不该每条都落盘。 */
  private scheduleSave(): void {
    if (this.saveTimer !== -1) {
      clearTimeout(this.saveTimer);
    }
    this.saveTimer = setTimeout(() => {
      this.saveTimer = -1;
      this.persist();
    }, SAVE_DEBOUNCE_MS);
  }

  private persist(): void {
    if (this.filePath.length === 0) return;
    writeTextFileAtomic(this.filePath, JSON.stringify(this.events))
      .catch((e: Error) => { console.warn(`[FlushLog] persist failed: ${e.message}`); });
  }
}

export const flushLog = new FlushLogClass();

/** 时间戳 → HH:mm:ss。 */
export function formatClock(ms: number): string {
  const d: Date = new Date(ms);
  const hh: string = d.getHours().toString().padStart(2, '0');
  const mm: string = d.getMinutes().toString().padStart(2, '0');
  const ss: string = d.getSeconds().toString().padStart(2, '0');
  return `${hh}:${mm}:${ss}`;
}
