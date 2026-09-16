// store/useTransferStore.ts
// 传输状态聚合：手动上传 / 同步下载 / 同步引擎活动 三类。
// 统一监听 Tauri 后端推送的事件，并对外暴露 startManualUpload 供 ViewCatalog 并行多文件上传。
import { defineStore } from 'pinia'
import { ref, computed, reactive } from 'vue'
import { isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { uploadFile as apiUploadFile } from '../api/file/fileApi'
import { listPendingTasks, listConflicts } from '../api/sync/syncApi'
import type { SyncTask, SyncConflict } from '../api/sync/syncTypes'
import { SpeedMeter } from '../utils/speedMeter'

// ── 类型 ────────────────────────────────────────────────────────────────────
export type UploadStatus = 'uploading' | 'done' | 'error'
/** manual = 文件管理里的普通分片上传；quick-share = 粘贴快传（单次整包上传） */
export type UploadKind = 'manual' | 'quick-share'
export interface UploadEntry {
  id: string
  name: string
  kind: UploadKind
  matchKey?: string // Tauri 模式为本地路径，用于匹配 upload-progress-byte 事件
  total: number
  sent: number
  status: UploadStatus
  error?: string
  startedAt: number
  finishedAt?: number
  /** 当前速度（字节/秒），上传中每秒刷新；完成后为整段平均速度 */
  speed: number
  /** 是否已收到过进度。没收到前处于「准备中」（本地算哈希、等 init 响应） */
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
  kind: string // create|modify|delete|deleted_by_server|uploading|done|error
  status: string
  error?: string
  time: number
}

const MAX_LIST = 100

// ── store ──────────────────────────────────────────────────────────────────
export const useTransferStore = defineStore('transfer', () => {
  const uploads = ref<UploadEntry[]>([])
  const downloads = ref<DownloadEntry[]>([])
  const syncEvents = ref<SyncEventEntry[]>([])
  const wsConnected = ref(false)
  const syncEngineRunning = ref(false)
  // 最近一次同步活动时间，用于推断「同步进行中」
  const lastSyncActiveAt = ref(0)

  const pendingTasks = ref<SyncTask[]>([])
  const conflicts = ref<SyncConflict[]>([])

  // 每个上传一个测速器。不放进响应式数据里：采样数组每次进度都在变，
  // 做成响应式只会徒增依赖追踪开销，界面只需要算好的 speed。
  const meters = new Map<string, SpeedMeter>()
  let speedTimer: ReturnType<typeof setInterval> | null = null

  let unlisteners: UnlistenFn[] = []
  let wsUnlisten: UnlistenFn | null = null
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let clockTimer: ReturnType<typeof setInterval> | null = null
  // 响应式时钟：computed 里裸用 Date.now() 没有响应性，时间流逝不会触发重算，
  // 曾导致「同步完成后指示器永远转圈」。
  const now = ref(Date.now())

  // ── WS 状态提前注册（App.vue 调用，防事件丢失）─────────────────────────────
  async function initWs() {
    if (wsUnlisten || !isTauri()) return
    try {
      const { listen } = await import('@tauri-apps/api/event')
      wsUnlisten = await listen<{ connected: boolean; message: string }>('ws-status', (e) => {
        wsConnected.value = e.payload.connected
      })
      // ws-status 是边沿事件：WS 在 app 启动时就连上了，那一下 emit 早于本监听注册 → 丢失，
      // 状态会永远卡在「未连接」。注册后主动查一次电平补齐（见 Rust is_ws_connected）。
      const { invoke } = await import('@tauri-apps/api/core')
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

  // ── 内部辅助 ──────────────────────────────────────────────────────────────
  function uid(): string {
    return Math.random().toString(36).slice(2) + Date.now().toString(36)
  }

  function bumpSyncActive() {
    lastSyncActiveAt.value = Date.now()
  }

  // ── 事件监听（Tauri 模式）────────────────────────────────────────────────
  async function init() {
    if (!isTauri()) return
    if (unlisteners.length > 0) return // 防重复
    await initWs() // 提前注册，init() 被延迟调用时补位
    unlisteners = [
      await listen<{ path: string; sent: number; total: number }>(
        'upload-progress-byte',
        (e) => {
          const p = e.payload
          const entry = uploads.value.find((u) => u.matchKey === p.path && u.status === 'uploading')
          if (entry) reportUploadProgress(entry, p.sent, p.total)
        },
      ),
      await listen<{ path: string; status: string; error?: string; taskId?: number }>(
        'download-progress',
        (e) => {
          const p = e.payload
          const name = p.path.split(/[\\/]/).pop() || p.path
          // upsert by taskId 或 path
          let entry = downloads.value.find(
            (d) => (p.taskId != null && d.taskId === p.taskId) || d.path === p.path,
          )
          if (!entry && p.status === 'downloading') {
            entry = {
              id: uid(),
              taskId: p.taskId,
              name,
              path: p.path,
              status: 'downloading',
              startedAt: Date.now(),
            }
            downloads.value.unshift(entry)
            if (downloads.value.length > MAX_LIST) downloads.value.length = MAX_LIST
          }
          if (entry) {
            entry.status = p.status as DownloadStatus
            if (p.error) entry.error = p.error
            if (p.status !== 'downloading') entry.finishedAt = Date.now()
          }
        },
      ),
      await listen<{ path: string; kind: string }>('sync-event', (e) => {
        const p = e.payload
        syncEvents.value.unshift({
          id: uid(),
          path: p.path,
          kind: p.kind,
          status: p.kind,
          time: Date.now(),
        })
        if (syncEvents.value.length > MAX_LIST) syncEvents.value.length = MAX_LIST
        bumpSyncActive()
      }),
      await listen<{ path: string; status: string; error?: string }>(
        'upload-progress',
        (e) => {
          const p = e.payload
          // 同步引擎触发的上传（upload_worker），进同步 tab。
          // 按 path upsert：done/error 事件更新原 uploading 行，而不是另起一行——
          // 否则 uploading 行永远停留在"进行中"，activeSyncUploads 恒 >0，指示器转圈不止。
          const existing = syncEvents.value.find(
            (s) => s.kind === 'upload' && s.path === p.path && s.status === 'uploading',
          )
          if (existing) {
            existing.status = p.status
            existing.error = p.error
            existing.time = Date.now()
          } else {
            syncEvents.value.unshift({
              id: uid(),
              path: p.path,
              kind: 'upload',
              status: p.status,
              error: p.error,
              time: Date.now(),
            })
            if (syncEvents.value.length > MAX_LIST) syncEvents.value.length = MAX_LIST
          }
          bumpSyncActive()
        },
      ),
      await listen<{ connected: boolean; message: string }>('ws-status', (e) => {
        wsConnected.value = e.payload.connected
      }),
    ]

    // 轮询同步引擎运行状态 + 刷新同步 tab 数据
    try {
      const { invoke } = await import('@tauri-apps/api/core')
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

  // ── 上传登记（所有上传的统一入口）─────────────────────────────────────────
  //
  // 普通上传和粘贴快传都从这里登记，于是传输列表、顶栏指示器、测速逻辑只有一份。
  //
  // 注意必须返回 reactive 代理而不是原始对象：ref 数组里存的是 raw 对象，
  // 直接改 raw 对象的字段**不会触发任何更新**——之前 startManualUpload 就是这么写的，
  // 结果 status 改成 done 后 activeUploads 不重算，顶栏指示器一直显示"上传中"，
  // Web 模式的进度回调也全部白改。

  /** 登记一个新上传，返回响应式条目，之后用 reportUploadProgress / finishUpload 更新。 */
  function beginUpload(opts: { name: string; kind: UploadKind; total?: number; matchKey?: string }): UploadEntry {
    const entry = reactive<UploadEntry>({
      id: uid(),
      name: opts.name,
      kind: opts.kind,
      matchKey: opts.matchKey,
      total: opts.total ?? 0,
      sent: 0,
      status: 'uploading',
      startedAt: Date.now(),
      speed: 0,
      started: false,
    })
    // 分片上传开头有两次簿记上报（算完哈希报 0、init 后报已有字节），不计入速度；
    // 快传的 XHR 进度每次都是真实字节。见 SpeedMeter 构造函数注释。
    meters.set(entry.id, new SpeedMeter(opts.kind === 'manual' ? 2 : 0))
    uploads.value.unshift(entry)
    if (uploads.value.length > MAX_LIST) {
      for (const dropped of uploads.value.splice(MAX_LIST)) meters.delete(dropped.id)
    }
    ensureSpeedTicker()
    return entry
  }

  function reportUploadProgress(entry: UploadEntry, sent: number, total: number) {
    if (entry.status !== 'uploading') return
    entry.sent = sent
    if (total > 0) entry.total = total
    const meter = meters.get(entry.id)
    if (meter) {
      meter.push(sent)
      entry.speed = meter.rate()
      entry.started = meter.started
    } else {
      entry.started = true
    }
  }

  /** 结束一个上传。error 为空表示成功。 */
  function finishUpload(entry: UploadEntry, error?: unknown) {
    entry.finishedAt = Date.now()
    if (error === undefined) {
      entry.status = 'done'
      entry.sent = entry.total || entry.sent
    } else {
      entry.status = 'error'
      entry.error = error instanceof Error ? error.message : String(error)
    }
    // 完成后显示整段平均速度，比"最后一瞬间的速度"有意义
    entry.speed = meters.get(entry.id)?.average(entry.finishedAt) ?? 0
    meters.delete(entry.id)
  }

  // 链路卡住时不会有新进度，速度必须靠定时器自己衰减下来（见 SpeedMeter 注释）。
  // 只在有进行中的上传时才跑，空闲不占资源。
  function ensureSpeedTicker() {
    if (speedTimer) return
    speedTimer = setInterval(() => {
      const now = Date.now()
      let active = 0
      for (const u of uploads.value) {
        if (u.status !== 'uploading') continue
        active++
        const meter = meters.get(u.id)
        if (meter) u.speed = meter.rate(now)
      }
      if (active === 0) stopSpeedTicker()
    }, 1000)
  }

  function stopSpeedTicker() {
    if (speedTimer) clearInterval(speedTimer)
    speedTimer = null
  }

  // ── 手动上传（并行）──────────────────────────────────────────────────────
  /**
   * 启动一次手动上传，注册到上传列表，独立追踪进度。
   * 多个调用并行即为并行上传（调用方用 Promise.all / Promise.allSettled）。
   */
  async function startManualUpload(
    entry: string | File,
    remoteDir: string,
  ): Promise<void> {
    const isPath = typeof entry === 'string'
    const item = beginUpload({
      name: isPath ? entry.split(/[\\/]/).pop() || entry : entry.name,
      kind: 'manual',
      total: isPath ? 0 : entry.size,
      // Tauri 模式进度走 upload-progress-byte 事件，按本地路径匹配回这一条
      matchKey: isPath ? entry : undefined,
    })

    try {
      // Web 模式走回调；Tauri 模式这个回调不会被调用（见 fileApi.uploadFile）
      await apiUploadFile(entry, remoteDir, (sent, t) => reportUploadProgress(item, sent, t))
      finishUpload(item)
    } catch (e) {
      finishUpload(item, e)
      throw e
    }
  }

  /** 清除已完成/失败的记录 */
  function clearFinished(list: 'uploads' | 'downloads' | 'syncEvents') {
    if (list === 'uploads') {
      uploads.value = uploads.value.filter((u) => u.status === 'uploading')
      // 进行中的测速器保留，其余的已在 finishUpload 里删过
    } else if (list === 'downloads') {
      downloads.value = downloads.value.filter((d) => d.status === 'downloading')
    } else {
      syncEvents.value = []
    }
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
    startManualUpload,
    beginUpload,
    reportUploadProgress,
    finishUpload,
    clearFinished,
  }
})
