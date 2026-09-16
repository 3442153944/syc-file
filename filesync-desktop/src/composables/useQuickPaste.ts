import {ref} from 'vue'
import {isTauri} from '@tauri-apps/api/core'
import {quickSharePaste} from '@/api/share/quickShareApi'
import {copyText} from '@/utils/clipboard'
import {getServerUrl} from '@/api/platform'
import {useTransferStore, type UploadEntry} from '@/store/useTransferStore'
import type {CreateShareLinkData} from '@/api/file/fileTypes'

export interface QuickPasteResult {
  data: CreateShareLinkData
  url: string
}

/**
 * 最近一次快传任务（模块级单例，所有调用方共享）。
 *
 * 为什么是单例：粘贴快传有两个入口（GlobalPasteListener 挂在 document 上、QuickPaste
 * 页面挂在自己的容器上），无论哪个入口接住了粘贴，快传页面都应该显示这次上传的进度 ——
 * 比如用户在快传页面上点了别处导致焦点不在容器里，粘贴会由全局监听接住，页面若只看
 * 自己发起的任务就会毫无反应。
 *
 * 值就是 transfer store 里那条响应式 UploadEntry，进度/速度由 store 统一维护。
 */
const currentTask = ref<UploadEntry | null>(null)
/** 最近一次快传成功后的分享链接（和 currentTask 对应） */
const currentUrl = ref('')

/**
 * 粘贴快传的共享逻辑：给一个 ClipboardEvent，剪贴板里没有文件就什么都不做——
 * 不 preventDefault，不影响正常粘贴文本到输入框；有文件就拦下来（只取第一个，
 * v1 不做多文件批量）、上传、自动复制分享链接。
 *
 * 被两处复用：GlobalPasteListener 挂到 document 上做"应用窗口内随便哪个页面粘贴文件
 * 都直接上传"的全局监听，QuickPaste.vue 挂在自己的粘贴目标区域上做可见的上传流程。
 */
export function useQuickPaste(onResult?: (result: QuickPasteResult | null, error?: unknown) => void) {
  const uploading = ref(false)

  const buildShareUrl = (data: CreateShareLinkData) => {
    const base = isTauri() ? getServerUrl().replace(/\/+$/, '') : window.location.origin
    return base + data.url_path
  }

  const handlePasteEvent = async (event: ClipboardEvent) => {
    // 同一次粘贴只处理一次。两个入口是嵌套关系：快传页面的容器在 document 里面，
    // 粘贴事件先在容器上触发、再冒泡到 document —— 两边各传一遍，就是"粘贴一次
    // 上传两次"的原因。先处理的那一方已经同步调过 preventDefault，后面的据此跳过。
    //
    // 不用 stopPropagation：那会把事件对其它所有 document 监听器都藏起来，
    // defaultPrevented 是专门表达"这个事件已经有人处理了"的标准信号。
    if (event.defaultPrevented) return

    const files = event.clipboardData?.files
    if (!files || files.length === 0) return
    // 必须在第一个 await 之前调用：冒泡是同步进行的，await 之后再标记就来不及了
    event.preventDefault()
    const file = files[0]

    const store = useTransferStore()
    const task = store.beginUpload({
      name: file.name || 'pasted-file',
      kind: 'quick-share',
      total: file.size,
    })
    currentTask.value = task
    currentUrl.value = ''

    uploading.value = true
    try {
      const data = await quickSharePaste(file, (sent, total) => store.reportUploadProgress(task, sent, total))
      store.finishUpload(task)
      const url = buildShareUrl(data)
      if (currentTask.value === task) currentUrl.value = url
      try {
        await copyText(url)
      } catch {
        // 复制失败不影响主流程（链接已经生成），交给调用方在结果里自行提示
      }
      onResult?.({data, url})
    } catch (e) {
      store.finishUpload(task, e)
      onResult?.(null, e)
    } finally {
      uploading.value = false
    }
  }

  return {uploading, handlePasteEvent, buildShareUrl, currentTask, currentUrl}
}
