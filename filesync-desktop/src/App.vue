<script setup lang="ts">
import { NMessageProvider, NDialogProvider, NNotificationProvider } from 'naive-ui'
import { isTauri } from '@tauri-apps/api/core'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { useTransferStore } from './store/useTransferStore'
import LogViewer from './views/logs/LogViewer.vue'
import QuickPaste from './views/share/QuickPaste.vue'
import GlobalPasteListener from './components/GlobalPasteListener.vue'

// 日志窗口（label === 'logs'）只渲染 LogViewer，粘贴快传悬浮窗（label === 'quick-paste'）
// 只渲染 QuickPaste，两者都跳过主应用路由。
let isLogWindow = false
let isQuickPasteWindow = false
if (isTauri()) {
  try {
    const label = getCurrentWindow().label
    isLogWindow = label === 'logs'
    isQuickPasteWindow = label === 'quick-paste'
  } catch {
    isLogWindow = false
    isQuickPasteWindow = false
  }
}

// 尽早注册 WS 状态监听，防止 Rust setup 自动启动早于 home.vue 挂载
useTransferStore().initWs()
</script>

<template>
  <LogViewer v-if="isLogWindow" />

  <n-message-provider v-else-if="isQuickPasteWindow">
    <QuickPaste />
  </n-message-provider>

  <n-message-provider v-else>
    <n-notification-provider>
      <n-dialog-provider>
        <div class="main">
          <GlobalPasteListener />
          <router-view />
        </div>
      </n-dialog-provider>
    </n-notification-provider>
  </n-message-provider>
</template>

<style scoped>
.main {
  /* 你的外层容器样式，比如 100vh 满屏等 */
  height: 100vh;
  width: 100vw;
}
</style>

<style>
/* 全局样式 */
</style>
