<script setup lang="ts">
import {ref, onMounted, onUnmounted, nextTick} from 'vue'
import {isTauri} from '@tauri-apps/api/core'
import {getCurrentWindow} from '@tauri-apps/api/window'
import {NSpin, useMessage} from 'naive-ui'
import {useQuickPaste} from '@/composables/useQuickPaste'

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

const resultUrl = ref('')
const errorMsg = ref('')
const containerRef = ref<HTMLElement | null>(null)

const {uploading, handlePasteEvent} = useQuickPaste((result, err) => {
  if (result) {
    resultUrl.value = result.url
    errorMsg.value = ''
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
    errorMsg.value = err instanceof Error ? err.message : String(err)
    message.error(errorMsg.value)
  }
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
    <n-spin :show="uploading">
      <div class="paste-target">
        <div class="icon">📋</div>
        <template v-if="resultUrl">
          <div class="hint">已上传，链接已复制到剪贴板</div>
          <div class="url">{{ resultUrl }}</div>
        </template>
        <template v-else-if="errorMsg">
          <div class="hint error">{{ errorMsg }}</div>
        </template>
        <template v-else>
          <div class="hint">按 Ctrl+V 粘贴文件</div>
          <div class="sub">立即上传并生成分享链接{{ isFloating ? '，Esc 取消' : '' }}</div>
        </template>
      </div>
    </n-spin>
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
}
.icon { font-size: 32px; margin-bottom: 8px; }
.hint { font-size: 15px; color: #303133; }
.hint.error { color: #d03050; word-break: break-all; }
.sub { font-size: 13px; color: #909399; margin-top: 4px; }
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
