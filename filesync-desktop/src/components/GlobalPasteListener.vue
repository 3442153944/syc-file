<script setup lang="ts">
// 挂在主窗口根组件里的无渲染监听器：应用窗口只要处于焦点，随便在哪个页面粘贴文件都
// 直接上传（不是文件就完全不干预，不影响正常粘贴文本到输入框）。悬浮窗/日志窗口不挂这个，
// 各自有自己的处理（悬浮窗用 QuickPaste.vue 自己的可见粘贴区域）。
//
// 在快传页面上粘贴时，页面容器会先接住事件，这里据 defaultPrevented 跳过，
// 不会重复上传（见 useQuickPaste）。
import {onMounted, onUnmounted, watch} from 'vue'
import {useMessage, type MessageReactive} from 'naive-ui'
import {useRoute} from 'vue-router'
import {useQuickPaste} from '@/composables/useQuickPaste'
import {formatBytes, formatSpeed, formatEta} from '@/utils/speedMeter'

const message = useMessage()
const route = useRoute()

// 上传过程中的常驻提示，实时显示进度和速度；结束时换成成功/失败提示。
// 快传页面自己有进度展示，那里就不重复弹。
let progressMsg: MessageReactive | null = null

const closeProgress = () => {
  progressMsg?.destroy()
  progressMsg = null
}

const {handlePasteEvent, currentTask} = useQuickPaste((result, err) => {
  closeProgress()
  if (result) {
    message.success('已上传，链接已复制到剪贴板')
  } else {
    message.error(err instanceof Error ? err.message : String(err))
  }
})

const onQuickSharePage = () => route.name === 'QuickShare'

watch(
    () => currentTask.value && [currentTask.value.status, currentTask.value.sent, currentTask.value.speed],
    () => {
      const t = currentTask.value
      if (!t || t.status !== 'uploading' || onQuickSharePage()) {
        closeProgress()
        return
      }
      const content = describeProgress(t)
      if (progressMsg) {
        progressMsg.content = content
      } else {
        progressMsg = message.loading(content, {duration: 0})
      }
    },
)

function describeProgress(t: { name: string; sent: number; total: number; speed: number; started: boolean }) {
  if (!t.started) return `快传 ${t.name}：准备中…`
  if (t.total > 0 && t.sent >= t.total) return `快传 ${t.name}：已发送，服务器处理中…`
  const pct = t.total > 0 ? Math.floor((t.sent / t.total) * 100) : 0
  const eta = formatEta(t.total - t.sent, t.speed)
  return `快传 ${t.name}：${pct}% · ${formatBytes(t.sent)} / ${formatBytes(t.total)} · ${formatSpeed(t.speed)}${eta ? ` · 剩余 ${eta}` : ''}`
}

onMounted(() => document.addEventListener('paste', handlePasteEvent))
onUnmounted(() => {
  document.removeEventListener('paste', handlePasteEvent)
  closeProgress()
})
</script>

<template></template>
