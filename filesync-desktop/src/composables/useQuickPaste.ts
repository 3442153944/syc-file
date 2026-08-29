import {ref} from 'vue'
import {isTauri} from '@tauri-apps/api/core'
import {quickSharePaste} from '@/api/share/quickShareApi'
import {copyText} from '@/utils/clipboard'
import {getServerUrl} from '@/api/platform'
import type {CreateShareLinkData} from '@/api/file/fileTypes'

export interface QuickPasteResult {
  data: CreateShareLinkData
  url: string
}

/**
 * 粘贴快传的共享逻辑：给一个 ClipboardEvent，剪贴板里没有文件就什么都不做——
 * 不 preventDefault，不影响正常粘贴文本到输入框；有文件就拦下来（只取第一个，
 * v1 不做多文件批量）、上传、自动复制分享链接。
 *
 * 被两处复用：App.vue 挂到 document 上做"应用窗口内随便哪个页面粘贴文件都直接上传"
 * 的全局静默监听，QuickPaste.vue 挂在自己的粘贴目标区域上做可见的上传流程。
 */
export function useQuickPaste(onResult?: (result: QuickPasteResult | null, error?: unknown) => void) {
  const uploading = ref(false)

  const buildShareUrl = (data: CreateShareLinkData) => {
    const base = isTauri() ? getServerUrl().replace(/\/+$/, '') : window.location.origin
    return base + data.url_path
  }

  const handlePasteEvent = async (event: ClipboardEvent) => {
    const files = event.clipboardData?.files
    if (!files || files.length === 0) return
    event.preventDefault()
    const file = files[0]

    uploading.value = true
    try {
      const data = await quickSharePaste(file)
      const url = buildShareUrl(data)
      try {
        await copyText(url)
      } catch {
        // 复制失败不影响主流程（链接已经生成），交给调用方在结果里自行提示
      }
      onResult?.({data, url})
    } catch (e) {
      onResult?.(null, e)
    } finally {
      uploading.value = false
    }
  }

  return {uploading, handlePasteEvent, buildShareUrl}
}
