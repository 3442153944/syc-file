import {computed} from 'vue'
import {invoke, isTauri} from '@tauri-apps/api/core'
import {controlQuickShare, quickSharePaste, startQuickShareBytes, startQuickSharePath} from '@/api/share/quickShareApi'
import {copyText} from '@/utils/clipboard'
import {useTransferStore} from '@/store/useTransferStore'
import type {CreateShareLinkData} from '@/api/file/fileTypes'

export interface QuickPasteResult { data: CreateShareLinkData; url: string }
const webControllers = new Map<string, AbortController>()
export async function controlQuickPaste(id: string, action: 'pause' | 'resume' | 'cancel') {
  if (isTauri()) return controlQuickShare(id, action)
  if (action !== 'cancel') throw new Error('网页快传暂不支持暂停，请使用桌面端')
  webControllers.get(id)?.abort()
  useTransferStore().setWebUploadStatus(id, 'cancelled')
}
export function useQuickPaste(onResult?: (result: QuickPasteResult | null, error?: unknown) => void) {
  const store = useTransferStore()
  const tasks = computed(() => store.uploads.filter(t => t.kind === 'quick-share'))
  const currentTask = computed(() => tasks.value[0] ?? null)
  const currentUrl = computed(() => currentTask.value?.shareUrl ?? '')
  const uploading = computed(() => tasks.value.some(t => t.status === 'uploading'))
  const handlePasteEvent = async (event: ClipboardEvent) => {
    if (event.defaultPrevented) return
    const files = Array.from(event.clipboardData?.files ?? [])
    if (!files.length) return
    event.preventDefault()
    try {
      const paths = isTauri() ? await invoke<string[]>('clipboard_file_paths').catch(() => []) : []
      const payloads = isTauri() && !paths.length ? files.map(file => {
        if (file.size > 256 * 1024 * 1024) return null
        const promise = file.arrayBuffer()
        void promise.catch(() => {})
        return promise
      }) : []
      await store.ensureMirror()
      if (isTauri()) {
        if (paths.length) {
          for (const path of paths) {
            try { await startQuickSharePath(path) }
            catch (e) { onResult?.(null, e) }
          }
        } else {
          for (let i = 0; i < files.length; i++) {
            try {
              const buffer = payloads[i]
              if (!buffer) throw new Error('文件过大且剪贴板未提供原始路径，请通过上传管理页上传')
              await startQuickShareBytes(files[i].name || 'pasted-file', new Uint8Array(await buffer))
            } catch (e) { onResult?.(null, e) }
          }
        }
        return
      }
      for (const file of files) {
        const id = await store.beginUpload({name: file.name || 'pasted-file', kind: 'quick-share', total: file.size})
        const controller = new AbortController()
        webControllers.set(id, controller)
        void (async () => {
          try {
            const data = await quickSharePaste(file, (sent, total) => store.reportUploadProgress(id, sent, total), controller.signal)
            if (controller.signal.aborted) return
            const url = window.location.origin + data.url_path
            const task = store.uploadById(id)
            if (task) task.shareUrl = url
            try { await copyText(url) } catch { /* 仍可手动复制 */ }
            await store.finishUpload(id)
            onResult?.({data, url})
          } catch (e) {
            if (!controller.signal.aborted) {
              await store.finishUpload(id, e)
              onResult?.(null, e)
            }
          } finally { webControllers.delete(id) }
        })()
      }
    } catch (e) { onResult?.(null, e) }
  }
  return {tasks, uploading, handlePasteEvent, currentTask, currentUrl}
}
