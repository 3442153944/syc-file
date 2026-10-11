import {ref, computed} from 'vue'
import {isTauri} from '@tauri-apps/api/core'
import {quickSharePaste, quickSharePastePath} from '@/api/share/quickShareApi'
import {copyText} from '@/utils/clipboard'
import {getServerUrl} from '@/api/platform'
import {useTransferStore} from '@/store/useTransferStore'
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
 * 只存 id，条目从 transfer store 里现取：桌面端条目由 Rust 事件整条推送后合并进来，
 * 按 id 取到的才是最新的响应式对象（进度/速度由 store 统一维护）。
 */
const currentTaskId = ref<string | null>(null)
const currentTask = computed(() => useTransferStore().uploadById(currentTaskId.value) ?? null)
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

    // 剪贴板内容定性优先：Windows 对资源管理器复制的文件只存路径引用（CF_HDROP），
    // WebView2 给的 File 是它物化的临时副本——大文件读字节等于先整拷一遍再 IPC
    // 序列化（GB 级直接崩）。先让 Rust 读剪贴板原始路径（剪贴板本身稳定，不抢时间），
    // 有路径就走原文件零拷贝上传；没有（剪贴板图片等无路径内容）才回退字节路径。
    // 回退时的 NotReadableError 风险（WebView2 临时文件回收）只对无路径内容存在，
    // 那种情况紧跟发起读取，失败也有明确提示。
    let srcPath: string | null = null
    if (isTauri()) {
      try {
        const {invoke} = await import('@tauri-apps/api/core')
        const paths = await invoke<string[]>('clipboard_file_paths')
        if (paths.length > 0) srcPath = paths[0]
      } catch {
        // 探测失败不致命，回退字节路径
      }
    }

    const store = useTransferStore()
    currentUrl.value = ''
    uploading.value = true

    // 登记失败（比如 IPC 出错）不该拦住上传本身：没有进度显示也比传不了强
    let taskId: string | null = null
    try {
      taskId = await store.beginUpload({
        name: file.name || 'pasted-file',
        kind: 'quick-share',
        total: file.size,
      })
      currentTaskId.value = taskId
    } catch {
      currentTaskId.value = null
    }

    try {
      const report = (sent: number, total: number) => {
        if (taskId) store.reportUploadProgress(taskId, sent, total)
      }
      let data: CreateShareLinkData
      if (srcPath) {
        data = await quickSharePastePath(srcPath, file.name, report)
      } else {
        // 字节兜底：超大文件若无原始路径，经 WebView 读字节必然不现实，早败早提示
        if (isTauri() && file.size > 256 * 1024 * 1024) {
          throw new Error('文件过大且剪贴板未提供原始路径，请通过上传管理页上传')
        }
        let bytes: Uint8Array
        try {
          bytes = new Uint8Array(await file.arrayBuffer())
        } catch {
          // 剪贴板临时文件已被回收等场景：给出可操作的提示而不是原生英文报错
          throw new Error('读取剪贴板文件失败，请重新复制该文件后再粘贴（或改用上传管理页）')
        }
        data = await quickSharePaste(file, report, bytes)
      }
      const url = buildShareUrl(data)
      if (currentTaskId.value === taskId) currentUrl.value = url
      // 先复制链接再报完成：桌面端主窗口若在上传期间被关闭（隐藏着等上传结束），
      // finish 之后 Rust 会销毁这个 WebView —— 顺序反过来的话链接可能来不及进剪贴板
      try {
        await copyText(url)
      } catch {
        // 复制失败不影响主流程（链接已经生成），交给调用方在结果里自行提示
      }
      if (taskId) await store.finishUpload(taskId)
      onResult?.({data, url})
    } catch (e) {
      if (taskId) await store.finishUpload(taskId, e)
      onResult?.(null, e)
    } finally {
      uploading.value = false
    }
  }

  return {uploading, handlePasteEvent, buildShareUrl, currentTask, currentUrl}
}
