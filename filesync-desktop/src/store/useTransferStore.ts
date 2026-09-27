// store/useTransferStore.ts
// 传输状态聚合：上传 / 同步下载 / 同步引擎活动 三类。
//
// 两种模式，对外接口一致：
//
//   Tauri 模式：状态的唯一来源是 Rust（src-tauri/src/transfers.rs），这里只是镜像。
//     主窗口关闭会销毁 WebView，前端内存里的东西全没了；进度由 Rust 持有，窗口重建后
//     先取一次快照（get_transfers），再靠 `transfer-changed` 事件增量更新。
//     普通上传由 Rust 的 upload_file 自己登记；在页面里执行的上传（粘贴快传的 XHR）
//     通过 transfer_upload_* 命令把进度报给 Rust。
//
//   Web 模式：没有 Rust，状态和测速都在本地维护。
//
// 条目一律用 id 引用而不是对象引用：Tauri 下条目由事件整条推送后合并进来，
// 调用方拿着 id 随时 uploadById 取到的才是最新的响应式对象。
import { defineStore } from 'pinia'
import { ref, computed, reactive, type Ref } from 'vue'
import { invoke, isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { uploadFile as apiUploadFile } from '../api/file/fileApi'
import { listPendingTasks, listConflicts } from '../api/sync/syncApi'
import type { SyncTask, SyncConflict } from '../api/sync/syncTypes'
import { SpeedMeter } from '../utils/speedMeter'

// ── 类型（与 Rust transfers.rs 的序列化结果一一对应） ─────────────────────────
export type UploadStatus = 'uploading' | 'done' | 'error'
/** manual = 文件管理/发布里的分片上传；quick-share = 粘贴快传（单次整包上传） */
export type UploadKind = 'manual' | 'quick-share'
export interface UploadEntry {
  id: string
  name: string
  kind: UploadKind
  total: number
  sent: number
  status: UploadStatus
  error?: string
  startedAt: number
  finishedAt?: number
  /** 当前速度（字节/秒），上传中每秒刷新；完成后为整段平均速度 */
  speed: number
  /** 是否已进入真实传输阶段。之前处于「准备中」（本地算哈希、等 init 响应） */
  started: boolean
}

export type DownloadStatus = 'downloading' | 'done' | 'blocked' | 'error'
export interface DownloadEntry {
  id: string
  taskId?: number
  name: string
  path: string
  status: DownloadStatus
  error?: string
  startedAt: number
  finishedAt?: number
}

export interface SyncEventEntry {
  id: string
  path: string
  kind: string // create|modify|delete|deleted_by_server|upload
  status: string
  error?: string
  time: number
}

type TransferChange =
  | { list: 'upload'; entry: UploadEntry }
  | { list: 'download'; entry: DownloadEntry }
  | { list: 'sync'; entry: SyncEventEntry }

interface TransferSnapshot {
  uploads: UploadEntry[]
  downloads: DownloadEntry[]
  syncEvents: SyncEventEntry[]
}

type ListName = 'uploads' | 'downloads' | 'syncEvents'

const MAX_LIST = 100

/** Tauri 下页面内上传（XHR 每 ~50ms 一次进度）报给 Rust 的最小间隔 */
const PROGRESS_REPORT_INTERVAL_MS = 200

// ── store ──────────────────────────────────────────────────────────────────
export const useTransferStore = defineStore('transfer', () => {
  const tauri = isTauri()

  const uploads = ref<UploadEntry[]>([])
  const downloads = ref<DownloadEntry[]>([])
  const syncEvents = ref<SyncEventEntry[]>([])
  const wsConnected = ref(false)
  const syncEngineRunning = ref(false)
  // 最近一次同步活动时间，用于推断「同步进行中」
  const lastSyncActiveAt = ref(0)

  const pendingTasks = ref<SyncTask[]>([])
  const conflicts = ref<SyncConflict[]>([])

  let unlisteners: UnlistenFn[] = []
  let wsUnlisten: UnlistenFn | null = null
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let clockTimer: ReturnType<typeof setInterval> | null = null
  // 响应式时钟：computed 里裸用 Date.now() 没有响应性，时间流逝不会触发重算，
  // 曾导致「同步完成后指示器永远转圈」。
  const now = ref(Date.now())

  // ── WS 状态提前注册（App.vue 调用，防事件丢失）─────────────────────────────
  async function initWs() {
    if (wsUnlisten || !tauri) return
    try {
      wsUnlisten = await listen<{ connected: boolean; message: string }>('ws-status', (e) => {
        wsConnected.value = e.payload.connected
      })
      // ws-status 是边沿事件：WS 在 app 启动时就连上了，那一下 emit 早于本监听注册 → 丢失，
      // 状态会永远卡在「未连接」。注册后主动查一次电平补齐（见 Rust is_ws_connected）。
      wsConnected.value = await invoke<boolean>('is_ws_connected')
    } catch { /* 非 Tauri */ }
  }

  // ── getters ──────────────────────────────────────────────────────────────
  const activeUploads = computed(() => uploads.value.filter((u) => u.status === 'uploading'))
  const activeDownloads = computed(() => downloads.value.filter((d) => d.status === 'downloading'))
  const activeSyncUploads = computed(() =>
    syncEvents.value.filter((e) => e.status === 'uploading'),
  )

  /** 同步正在工作：有活跃同步上传，或最近 8 秒内有同步活动且引擎在跑 */
  const syncing = computed(
    () =>
      activeSyncUploads.value.length > 0 ||
      (syncEngineRunning.value &&
        wsConnected.value &&
        now.value - lastSyncActiveAt.value < 8000),
  )

  /** 图标状态：优先级 上传 > 下载 > 同步 > 空闲 */
  const indicator = computed(() => {
    if (activeUploads.value.length > 0) return { type: 'upload' as const, count: activeUploads.value.length }
    if (activeDownloads.value.length > 0)
      return { type: 'download' as const, count: activeDownloads.value.length }
    if (syncing.value) return { type: 'sync' as const, count: activeSyncUploads.value.length }
    return { type: 'idle' as const, count: 0 }
  })

  function uploadById(id: string | null | undefined): UploadEntry | undefined {
    return id ? uploads.value.find((u) => u.id === id) : undefined
  }

  // ── 通用：按 id upsert ────────────────────────────────────────────────────
  //
  // 合并进已有对象（Object.assign）而不是整体替换：调用方持有的响应式对象保持同一个，
  // 界面上的绑定不会闪断。新条目放最前（与 Rust 侧顺序、界面展示一致）。
  function upsert<T extends { id: string }>(list: Ref<T[]>, entry: T) {
    const existing = list.value.find((x) => x.id === entry.id)
    if (existing) {
      Object.assign(existing, entry)
    } else {
      list.value.unshift(entry)
      if (list.value.length > MAX_LIST) list.value.length = MAX_LIST
    }
  }

  // ══ Tauri：镜像 Rust ═════════════════════════════════════════════════════

  let mirrorReady: Promise<void> | null = null

  /**
   * 挂上 transfer 事件监听并取一次快照。幂等，任何需要传输状态的入口都先调它：
   * 主窗口走 init()，粘贴快传悬浮窗不经过 home.vue，发起上传时也会触发。
   *
   * 这组监听跟随整个页面生命周期，不在 dispose 里卸载 —— 页面在（比如退回登录页
   * 又登录回来），状态就一直有效；窗口销毁时它们随 WebView 一起消失。
   */
  function ensureMirror(): Promise<void> {
    if (!tauri) return Promise.resolve()
    if (!mirrorReady) {
      mirrorReady = (async () => {
        // 先挂监听再取快照：反过来的话两步之间的变化两边都拿不到
        await listen<TransferChange>('transfer-changed', (e) => applyChange(e.payload))
        await listen<ListName>('transfer-cleared', (e) => applyCleared(e.payload))
        mergeSnapshot(await invoke<TransferSnapshot>('get_transfers'))
      })().catch((err) => {
        mirrorReady = null // 允许下次重试
        throw err
      })
    }
    return mirrorReady
  }

  function applyChange(change: TransferChange) {
    if (change.list === 'upload') upsert(uploads, change.entry)
    else if (change.list === 'download') upsert(downloads, change.entry)
    else {
      upsert(syncEvents, change.entry)
      lastSyncActiveAt.value = Date.now()
    }
  }

  function applyCleared(list: ListName) {
    if (list === 'uploads') uploads.value = uploads.value.filter((u) => u.status === 'uploading')
    else if (list === 'downloads') downloads.value = downloads.value.filter((d) => d.status === 'downloading')
    else if (list === 'syncEvents') syncEvents.value = []
  }

  /**
   * 合并快照：只补充本地还没有的条目。监听先于快照挂上，所以本地已有的条目
   * 来自事件，一定不比快照旧，不能被快照覆盖回去。
   */
  function mergeSnapshot(snap: TransferSnapshot) {
    const mergeInto = <T extends { id: string }>(list: Ref<T[]>, incoming: T[], ts: (x: T) => number) => {
      const have = new Set(list.value.map((x) => x.id))
      const missing = incoming.filter((x) => !have.has(x.id))
      if (missing.length === 0) return
      list.value = [...list.value, ...missing].sort((a, b) => ts(b) - ts(a)).slice(0, MAX_LIST)
    }
    mergeInto(uploads, snap.uploads, (u) => u.startedAt)
    mergeInto(downloads, snap.downloads, (d) => d.startedAt)
    mergeInto(syncEvents, snap.syncEvents, (s) => s.time)
    const latestSync = snap.syncEvents.reduce((m, s) => Math.max(m, s.time), 0)
    if (latestSync > lastSyncActiveAt.value) lastSyncActiveAt.value = latestSync
  }

  // 页面内上传的进度节流：XHR 大约每 50ms 回调一次，逐次 IPC 没必要
  const throttle = new Map<string, { lastSent: number; pending?: [number, number]; timer?: ReturnType<typeof setTimeout> }>()

  function sendProgress(id: string, sent: number, total: number) {
    const t = throttle.get(id) ?? { lastSent: 0 }
    throttle.set(id, t)
    // 发出去了就作废待补发的定时器。必须把句柄也清掉：留着旧句柄的话，
    // 下一次节流期内的上报会以为"已经排过补发"而不再安排，最后一次进度就被吞了
    if (t.timer) clearTimeout(t.timer)
    t.timer = undefined
    t.lastSent = Date.now()
    t.pending = undefined
    invoke('transfer_upload_progress', { id, sent, total }).catch(() => { /* 进度丢一次无所谓 */ })
  }

  function flushProgress(id: string) {
    const t = throttle.get(id)
    if (!t) return
    if (t.pending) sendProgress(id, t.pending[0], t.pending[1])
    if (t.timer) clearTimeout(t.timer)
    throttle.delete(id)
  }

  // ══ Web：本地状态 + 测速 ══════════════════════════════════════════════════

  // 每个上传一个测速器。不放进响应式数据里：采样数组每次进度都在变，
  // 做成响应式只会徒增依赖追踪开销，界面只需要算好的 speed。
  const meters = new Map<string, SpeedMeter>()
  let speedTimer: ReturnType<typeof setInterval> | null = null

  function uid(): string {
    return Math.random().toString(36).slice(2) + Date.now().toString(36)
  }

  // 链路卡住时不会有新进度，速度必须靠定时器自己衰减下来（见 SpeedMeter 注释）。
  // 只在有进行中的上传时才跑，空闲不占资源。
  function ensureSpeedTicker() {
    if (speedTimer) return
    speedTimer = setInterval(() => {
      const t = Date.now()
      let active = 0
      for (const u of uploads.value) {
        if (u.status !== 'uploading') continue
        active++
        const meter = meters.get(u.id)
        if (meter) u.speed = meter.rate(t)
      }
      if (active === 0) stopSpeedTicker()
    }, 1000)
  }

  function stopSpeedTicker() {
    if (speedTimer) clearInterval(speedTimer)
    speedTimer = null
  }

  // ══ 对外：上传登记（两种模式统一接口） ════════════════════════════════════

  /** 登记一个在本页面里执行的上传，返回 id。之后用 reportUploadProgress / finishUpload 更新。 */
  async function beginUpload(opts: { name: string; kind: UploadKind; total?: number }): Promise<string> {
    const total = opts.total ?? 0
    if (tauri) {
      await ensureMirror()
      const id = await invoke<string>('transfer_upload_begin', { name: opts.name, kind: opts.kind, total })
      // 事件可能比这次 invoke 的返回晚到；先放一条占位，调用方立刻就能 uploadById 拿到
      if (!uploadById(id)) {
        upsert(uploads, {
          id, name: opts.name, kind: opts.kind, total, sent: 0, status: 'uploading',
          startedAt: Date.now(), speed: 0, started: false,
        })
      }
      return id
    }

    // 必须存 reactive 代理：ref 数组里放的是 raw 对象时，直接改 raw 对象的字段不会触发任何更新
    const entry = reactive<UploadEntry>({
      id: uid(), name: opts.name, kind: opts.kind, total, sent: 0, status: 'uploading',
      startedAt: Date.now(), speed: 0, started: false,
    })
    // 分片上传开头有两次簿记上报，不计入速度；快传 XHR 进度每次都是真实字节
    meters.set(entry.id, new SpeedMeter(opts.kind === 'manual' ? 2 : 0))
    uploads.value.unshift(entry)
    if (uploads.value.length > MAX_LIST) {
      for (const dropped of uploads.value.splice(MAX_LIST)) meters.delete(dropped.id)
    }
    ensureSpeedTicker()
    return entry.id
  }

  function reportUploadProgress(id: string, sent: number, total: number) {
    if (tauri) {
      const t = throttle.get(id)
      const due = !t || Date.now() - t.lastSent >= PROGRESS_REPORT_INTERVAL_MS || (total > 0 && sent >= total)
      if (due) {
        sendProgress(id, sent, total)
      } else if (t) {
        // 节流期内只记最新值，到点补发，保证最后一次进度不会被吞掉
        t.pending = [sent, total]
        if (!t.timer) {
          t.timer = setTimeout(() => {
            if (t.pending) sendProgress(id, t.pending[0], t.pending[1])
          }, PROGRESS_REPORT_INTERVAL_MS - (Date.now() - t.lastSent))
        }
      }
      return
    }

    const entry = uploadById(id)
    if (!entry || entry.status !== 'uploading') return
    entry.sent = sent
    if (total > 0) entry.total = total
    const meter = meters.get(id)
    if (meter) {
      meter.push(sent)
      entry.speed = meter.rate()
      entry.started = meter.started
    } else {
      entry.started = true
    }
  }

  /** 结束一个上传。error 为 undefined 表示成功。 */
  async function finishUpload(id: string, error?: unknown) {
    const message = error === undefined ? undefined : error instanceof Error ? error.message : String(error)
    if (tauri) {
      flushProgress(id)
      await invoke('transfer_upload_finish', { id, error: message ?? null }).catch(() => {})
      return
    }

    const entry = uploadById(id)
    if (!entry) return
    entry.finishedAt = Date.now()
    if (message === undefined) {
      entry.status = 'done'
      entry.sent = entry.total || entry.sent
    } else {
      entry.status = 'error'
      entry.error = message
    }
    // 完成后显示整段平均速度，比"最后一瞬间的速度"有意义
    entry.speed = meters.get(id)?.average(entry.finishedAt) ?? 0
    meters.delete(id)
  }

  // ── 手动上传（并行）──────────────────────────────────────────────────────
  /**
   * 启动一次手动上传。多个调用并行即为并行上传（调用方用 Promise.all / Promise.allSettled）。
   *
   * Tauri 下 entry 是本地路径，上传整个在 Rust 里执行并由 Rust 自己登记进度 ——
   * 关掉窗口上传也照常跑完。Web 下 entry 是 File，在页面里分片上传。
   */
  async function startManualUpload(entry: string | File, remoteDir: string): Promise<void> {
    if (tauri) {
      await ensureMirror()
      await apiUploadFile(entry, remoteDir)
      return
    }

    const file = entry as File
    const id = await beginUpload({ name: file.name, kind: 'manual', total: file.size })
    try {
      await apiUploadFile(file, remoteDir, (sent, t) => reportUploadProgress(id, sent, t))
      await finishUpload(id)
    } catch (e) {
      await finishUpload(id, e)
      throw e
    }
  }

  /** 清除已完成/失败的记录 */
  function clearFinished(list: ListName) {
    if (tauri) {
      // Rust 清完会推 transfer-cleared，所有窗口统一同步
      invoke('transfer_clear_finished', { list }).catch(() => {})
      return
    }
    applyCleared(list)
  }

  // ── 主窗口初始化（home.vue 调用）──────────────────────────────────────────
  async function init() {
    if (!tauri) return
    if (unlisteners.length > 0) return // 防重复
    await initWs() // 提前注册，init() 被延迟调用时补位
    try {
      await ensureMirror()
    } catch {
      /* 取不到快照不影响后续实时事件以外的功能 */
    }
    unlisteners = [
      await listen<{ connected: boolean; message: string }>('ws-status', (e) => {
        wsConnected.value = e.payload.connected
      }),
    ]

    // 轮询同步引擎运行状态 + 刷新同步 tab 数据
    try {
      const refreshEngine = async () => {
        syncEngineRunning.value = await invoke<boolean>('is_sync_running')
      }
      await refreshEngine()

      // 登录后补位：setup 阶段无 token 未能启动，现在尝试
      if (!syncEngineRunning.value) {
        try { await invoke('start_sync'); await refreshEngine() } catch { /* 静默 */ }
      }

      pollTimer = setInterval(async () => {
        await refreshEngine()
        await refreshSyncData()
      }, 5000)
    } catch {
      /* 非 Tauri 忽略 */
    }
    clockTimer = setInterval(() => {
      now.value = Date.now()
    }, 2000)
  }

  async function refreshSyncData() {
    try {
      pendingTasks.value = await listPendingTasks()
      conflicts.value = await listConflicts()
    } catch {
      /* 静默 */
    }
  }

  function dispose() {
    unlisteners.forEach((fn) => fn())
    unlisteners = []
    wsUnlisten?.()
    wsUnlisten = null
    if (pollTimer) clearInterval(pollTimer)
    pollTimer = null
    if (clockTimer) clearInterval(clockTimer)
    clockTimer = null
    stopSpeedTicker()
  }

  return {
    uploads,
    downloads,
    syncEvents,
    pendingTasks,
    conflicts,
    wsConnected,
    syncEngineRunning,
    activeUploads,
    activeDownloads,
    activeSyncUploads,
    syncing,
    indicator,
    init,
    initWs,
    dispose,
    refreshSyncData,
    uploadById,
    startManualUpload,
    beginUpload,
    reportUploadProgress,
    finishUpload,
    clearFinished,
  }
})
