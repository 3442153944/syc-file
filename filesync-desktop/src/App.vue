<script setup lang="ts">
import { NMessageProvider, NDialogProvider, NNotificationProvider } from 'naive-ui'
import { isTauri } from '@tauri-apps/api/core'
import { getCurrentWindow } from '@tauri-apps/api/window'
import LogViewer from './views/logs/LogViewer.vue'

// 日志窗口（label === 'logs'）只渲染 LogViewer，跳过主应用路由。
let isLogWindow = false
if (isTauri()) {
  try {
    isLogWindow = getCurrentWindow().label === 'logs'
  } catch {
    isLogWindow = false
  }
}
</script>

<template>
  <LogViewer v-if="isLogWindow" />

  <n-message-provider v-else>
    <n-notification-provider>
      <n-dialog-provider>
        <div class="main">
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
