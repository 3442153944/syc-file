// store/useTransferStore.ts
// 传输状态聚合：手动上传 / 同步下载 / 同步引擎活动 三类。
// 统一监听 Tauri 后端推送的事件，并对外暴露 startManualUpload 供 ViewCatalog 并行多文件上传。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { uploadFile as apiUploadFile } from '../api/file/fileApi'
import { listPendingTasks, listConflicts } from '../api/sync/syncApi'
import type { SyncTask, SyncConflict } from '../api/sync/syncTypes'

// ── 类型 ────────────────────────────────────────────────────────────────────
export type UploadStatus = 'uploading' | 'done' | 'error'
export interface UploadEntry {
  id: string
  name: string
  matchKey?: string // Tauri 模式为本地路径，用于匹配 upload-progress-byte 事件
  total: number
  sent: number
  status: UploadStatus
  error?: string
  startedAt: number
  finishedAt?: number
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

  let unlisteners: UnlistenFn[] = []
  let pollTimer: ReturnType<typeof setInterval> | null = null

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
        Date.now() - lastSyncActiveAt.value < 8000),
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
    unlisteners = [
      await listen<{ path: string; sent: number; total: number }>(
        'upload-progress-byte',
        (e) => {
          const p = e.payload
          const entry = uploads.value.find((u) => u.matchKey === p.path && u.status === 'uploading')
          if (entry) {
            entry.sent = p.sent
            if (p.total > 0) entry.total = p.total
          }
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
          // 同步引擎触发的上传（upload_worker），进同步 tab
          syncEvents.value.unshift({
            id: uid(),
            path: p.path,
            kind: 'upload',
            status: p.status,
            error: p.error,
            time: Date.now(),
          })
          if (syncEvents.value.length > MAX_LIST) syncEvents.value.length = MAX_LIST
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
      pollTimer = setInterval(async () => {
        await refreshEngine()
        await refreshSyncData()
      }, 5000)
    } catch {
      /* 非 Tauri 忽略 */
    }
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
    if (pollTimer) clearInterval(pollTimer)
    pollTimer = null
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
    const name = isPath ? (entry as string).split(/[\\/]/).pop() || (entry as string) : (entry as File).name
    const total = isPath ? 0 : (entry as File).size
    const matchKey = isPath ? (entry as string) : undefined
    const item: UploadEntry = {
      id: uid(),
      name,
      matchKey,
      total,
      sent: 0,
      status: 'uploading',
      startedAt: Date.now(),
    }
    uploads.value.unshift(item)
    if (uploads.value.length > MAX_LIST) uploads.value.length = MAX_LIST

    try {
      await apiUploadFile(entry, remoteDir, (sent, t) => {
        item.sent = sent
        if (t > 0) item.total = t
      })
      item.status = 'done'
      item.sent = item.total || item.sent
      item.finishedAt = Date.now()
    } catch (e) {
      item.status = 'error'
      item.error = String(e)
      item.finishedAt = Date.now()
      throw e
    }
  }

  /** 清除已完成/失败的记录 */
  function clearFinished(list: 'uploads' | 'downloads' | 'syncEvents') {
    if (list === 'uploads') {
      uploads.value = uploads.value.filter((u) => u.status === 'uploading')
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
    dispose,
    refreshSyncData,
    startManualUpload,
    clearFinished,
  }
})
