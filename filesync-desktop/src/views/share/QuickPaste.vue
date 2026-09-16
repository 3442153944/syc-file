<script setup lang="ts">
import {ref, computed, onMounted, onUnmounted, nextTick} from 'vue'
import {isTauri} from '@tauri-apps/api/core'
import {getCurrentWindow} from '@tauri-apps/api/window'
import {NProgress, useMessage} from 'naive-ui'
import {useQuickPaste} from '@/composables/useQuickPaste'
import {formatBytes, formatSpeed, formatEta} from '@/utils/speedMeter'

const message = useMessage()

// 既作为路由页面用（主窗口/Web），也作为悬浮窗内容用——通过窗口 label 区分，
// 悬浮窗下用紧凑样式，且成功后自动关闭窗口（App.vue 对 "quick-paste" label 的
// 硬分支渲染就是这个组件本身，不经过 vue-router）。
const isFloating = ref(false)
if (isTauri()) {
  try {
    isFloating.value = getCurrentWindow().label === 'quick-paste'
  } catch {
    isFloating.value = false
  }
}

const containerRef = ref<HTMLElement | null>(null)

// 进度、结果、错误都取自共享的 currentTask：粘贴无论被这个页面还是全局监听接住，
// 这里都能显示（见 useQuickPaste 顶部注释）。回调只负责本页发起时的提示和悬浮窗自动关闭。
const {handlePasteEvent, currentTask, currentUrl} = useQuickPaste((result, err) => {
  if (result) {
    message.success('已上传，链接已复制到剪贴板')
    if (isFloating.value) {
      setTimeout(() => {
        try {
          getCurrentWindow().close()
        } catch {
          // 忽略：非 Tauri 环境或窗口已经被关闭
        }
      }, 2500)
    }
  } else {
    message.error(err instanceof Error ? err.message : String(err))
  }
})

const task = computed(() => currentTask.value)
const uploading = computed(() => task.value?.status === 'uploading')

const percent = computed(() => {
  const t = task.value
  if (!t || !t.total) return 0
  return Math.min(100, Math.floor((t.sent / t.total) * 100))
})

/** 进度条下方的状态文字：准备中 / 传输中（速度+剩余）/ 服务器处理中 */
const phaseText = computed(() => {
  const t = task.value
  if (!t) return ''
  if (!t.started) return '准备中…'
  // 字节发完 ≠ 完成：服务端还要落盘、生成分享链接
  if (t.total > 0 && t.sent >= t.total) return '已发送，服务器处理中…'
  const eta = formatEta(t.total - t.sent, t.speed)
  return `${formatSpeed(t.speed)}${eta ? ` · 剩余 ${eta}` : ''}`
})

// 悬浮窗用的是带原生标题栏的普通窗口，关闭/最小化/拖动都是操作系统自己处理的原生行为，
// 不用自己糊控件。这里只加个 Esc 快捷键方便不粘贴时快速关掉。
const onKeydown = (e: KeyboardEvent) => {
  if (e.key !== 'Escape' || !isFloating.value) return
  try {
    getCurrentWindow().close()
  } catch {
    // 忽略：非 Tauri 环境或窗口已经被关闭
  }
}

onMounted(async () => {
  await nextTick()
  containerRef.value?.focus()
  if (isFloating.value) {
    window.addEventListener('keydown', onKeydown)
  }
})

onUnmounted(() => {
  if (isFloating.value) {
    window.removeEventListener('keydown', onKeydown)
  }
})
</script>

<template>
  <div
      ref="containerRef"
      :class="['quick-paste-container', { floating: isFloating }]"
      tabindex="0"
      @paste="handlePasteEvent"
  >
    <div class="paste-target">
      <div class="icon">📋</div>

      <!-- 上传中：进度 + 速度 -->
      <template v-if="task && uploading">
        <div class="hint name" :title="task.name">{{ task.name }}</div>
        <n-progress
            class="bar"
            type="line"
            :percentage="percent"
            :height="10"
            :border-radius="5"
            :processing="true"
            indicator-placement="inside"
        />
        <div class="stats">
          <span>{{ formatBytes(task.sent) }} / {{ formatBytes(task.total) }}</span>
          <span>{{ phaseText }}</span>
        </div>
      </template>

      <template v-else-if="task && task.status === 'done'">
        <div class="hint">已上传，链接已复制到剪贴板</div>
        <div v-if="currentUrl" class="url">{{ currentUrl }}</div>
        <div class="sub">
          {{ formatBytes(task.total) }}
          <template v-if="task.speed > 0"> · 平均 {{ formatSpeed(task.speed) }}</template>
        </div>
        <div class="sub">继续按 Ctrl+V 粘贴下一个</div>
      </template>

      <template v-else-if="task && task.status === 'error'">
        <div class="hint error">{{ task.error }}</div>
        <div class="sub">再次按 Ctrl+V 重试</div>
      </template>

      <template v-else>
        <div class="hint">按 Ctrl+V 粘贴文件</div>
        <div class="sub">立即上传并生成分享链接{{ isFloating ? '，Esc 取消' : '' }}</div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.quick-paste-container {
  position: relative;
  height: 100%;
  min-height: 320px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  outline: none;
}
.quick-paste-container.floating {
  min-height: 100vh;
  border-radius: 0;
  box-shadow: none;
  padding: 16px;
}
.paste-target {
  text-align: center;
  border: 2px dashed #d0d5db;
  border-radius: 8px;
  padding: 32px 24px;
  min-width: 280px;
  width: min(440px, 100%);
}
.icon { font-size: 32px; margin-bottom: 8px; }
.hint { font-size: 15px; color: #303133; }
.hint.error { color: #d03050; word-break: break-all; }
.sub { font-size: 13px; color: #909399; margin-top: 4px; }
.name {
  max-width: 360px;
  margin: 0 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bar { margin-top: 14px; }
.stats {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin-top: 8px;
  font-size: 12px;
  color: #909399;
  font-variant-numeric: tabular-nums;
}
.url {
  margin-top: 10px;
  font-size: 12px;
  color: #666;
  word-break: break-all;
  background-color: #f5f7fa;
  border-radius: 4px;
  padding: 6px 8px;
}
</style>
