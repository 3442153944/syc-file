<script setup lang="ts">
import {ref, computed, onMounted, onUnmounted, nextTick} from 'vue'
import {isTauri} from '@tauri-apps/api/core'
import {getCurrentWindow} from '@tauri-apps/api/window'
import {NButton, NProgress, NSpace, NTag, useMessage} from 'naive-ui'
import {controlQuickPaste, useQuickPaste} from '@/composables/useQuickPaste'
import {useTransferStore, type UploadEntry} from '@/store/useTransferStore'
import {copyText} from '@/utils/clipboard'
import {formatBytes, formatSpeed, formatEta} from '@/utils/speedMeter'

const message = useMessage()
const desktop = isTauri()
const isFloating = desktop && getCurrentWindow().label === 'quick-paste'
const containerRef = ref<HTMLElement | null>(null)
const store = useTransferStore()
const {handlePasteEvent, tasks} = useQuickPaste((result, err) => {
  if (result) message.success('已上传，链接已复制到剪贴板')
  else message.error(err instanceof Error ? err.message : String(err))
})
const active = computed(() => tasks.value.filter(t => t.status === 'uploading' || t.status === 'paused').length)
const busy = ref(new Set<string>())
const percent = (t: UploadEntry) => t.total > 0 ? Math.min(100, Math.floor(t.sent / t.total * 100)) : 0
const labels = {uploading: '上传中', paused: '已暂停', cancelled: '已取消', done: '完成', error: '失败'}
function phase(t: UploadEntry) {
  if (t.status === 'paused') return '已暂停，可以继续或取消'
  if (t.status !== 'uploading') return t.status === 'done' && t.speed > 0 ? `平均 ${formatSpeed(t.speed)}` : ''
  if (!t.started) return '准备中…'
  if (t.total > 0 && t.sent >= t.total) return '服务器处理中…'
  const eta = formatEta(t.total - t.sent, t.speed)
  return `${formatSpeed(t.speed)}${eta ? ' · 剩余 ' + eta : ''}`
}
async function control(id: string, action: 'pause' | 'resume' | 'cancel') {
  busy.value.add(id)
  try { await controlQuickPaste(id, action) }
  catch (e) { message.error(String(e)) }
  finally { busy.value.delete(id) }
}
async function copy(url: string) {
  try { await copyText(url); message.success('链接已复制') }
  catch (e) { message.error(String(e)) }
}
const onKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Escape' && isFloating) void getCurrentWindow().close()
}
onMounted(async () => {
  try { await store.ensureMirror() } catch (e) { message.error(String(e)) }
  await nextTick()
  containerRef.value?.focus()
  if (isFloating) window.addEventListener('keydown', onKeydown)
})
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div ref="containerRef" class="quick-paste-container" tabindex="0" @paste="handlePasteEvent">
    <div class="paste-target">
      <strong>按 Ctrl+V 粘贴文件</strong>
      <div class="sub">支持多选与连续粘贴，每个文件单独生成链接</div>
      <div class="sub" v-if="desktop">关闭窗口后继续上传，重新打开可查看和控制任务</div>
    </div>
    <n-space justify="space-between" align="center">
      <span>快传任务（{{ active }} 个进行中）</span>
      <n-button size="tiny" quaternary @click="store.clearFinished('uploads')">清除已结束</n-button>
    </n-space>
    <div v-if="tasks.length === 0" class="empty">暂无快传任务</div>
    <div class="tasks">
      <div v-for="task in tasks" :key="task.id" class="task">
        <n-space justify="space-between" align="center">
          <span class="name" :title="task.name">{{ task.name }}</span>
          <n-tag size="small" :type="task.status === 'error' ? 'error' : task.status === 'done' ? 'success' : 'default'">{{ labels[task.status] }}</n-tag>
        </n-space>
        <n-progress v-if="task.status === 'uploading' || task.status === 'paused'" type="line" :percentage="percent(task)" :processing="task.status === 'uploading'" :height="8" />
        <div class="sub">{{ formatBytes(task.sent) }} / {{ formatBytes(task.total) }} · {{ phase(task) }}</div>
        <div v-if="task.error" class="error">{{ task.error }}</div>
        <div v-if="task.shareUrl" class="url">{{ task.shareUrl }}</div>
        <n-space class="actions">
          <n-button v-if="desktop && task.status === 'uploading'" size="small" :disabled="busy.has(task.id)" @click="control(task.id, 'pause')">暂停</n-button>
          <n-button v-if="task.status === 'paused'" size="small" :disabled="busy.has(task.id)" @click="control(task.id, 'resume')">继续</n-button>
          <n-button v-if="task.status === 'uploading' || task.status === 'paused'" size="small" :disabled="busy.has(task.id)" @click="control(task.id, 'cancel')">取消</n-button>
          <n-button v-if="task.shareUrl" size="small" @click="copy(task.shareUrl)">复制链接</n-button>
        </n-space>
      </div>
    </div>
  </div>
</template>

<style scoped>
.quick-paste-container {height: 100%; box-sizing: border-box; overflow-y: auto; padding: 16px; background: var(--n-color, #fff); outline: none;}
.paste-target {text-align: center; border: 2px dashed #d0d5db; border-radius: 8px; padding: 16px; margin-bottom: 14px;}
.tasks {display: flex; flex-direction: column; gap: 10px; margin-top: 10px;}
.task {border: 1px solid #e0e0e6; border-radius: 6px; padding: 12px;}
.name {max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;}
.sub, .empty {font-size: 12px; color: #808080; margin-top: 6px;}
.error {color: #d03050; word-break: break-word;}
.url {font-size: 12px; word-break: break-all; margin-top: 6px;}
.actions {margin-top: 8px;}
</style>
