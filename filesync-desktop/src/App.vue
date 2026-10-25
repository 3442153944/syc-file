<script setup lang="ts">
import { onMounted } from 'vue'
import { NMessageProvider, NDialogProvider, NNotificationProvider } from 'naive-ui'
import { ensureVpnDevice, startEmbeddedTunnel, resumeEmbeddedTunnel, meshMaintenanceError } from './api/vpn'
import { isTauri } from '@tauri-apps/api/core'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { useTransferStore } from './store/useTransferStore'
import LogViewer from './views/logs/LogViewer.vue'
import QuickPaste from './views/share/QuickPaste.vue'
import GlobalPasteListener from './components/GlobalPasteListener.vue'
import ServerNotificationListener from './components/ServerNotificationListener.vue'
import DesktopUpdateListener from './components/DesktopUpdateListener.vue'

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

// 开机自启后自动组网：登录完成（有 token）且隧道未启动时，自动用会话换身份并拉起 wireguard。
// 外部单独启动 WireGuard 服务时，隧道配置已安装，服务会自行连接；这里只兜底“还没建隧道”的情况。
onMounted(() => {
  if (!isTauri()) return
  const auto = () => localStorage.getItem('syc_vpn_auto_tunnel') === '1'
  let attempting = false
  const attempt = async () => {
    if (attempting || !localStorage.getItem('token')) return
    attempting = true
    try {
      const {invoke} = await import('@tauri-apps/api/core')
      if (await invoke<boolean>('vpn_tunnel_status')) {
        const device = await ensureVpnDevice()
        await resumeEmbeddedTunnel(device)
        return
      }
      if (auto()) {
        const device = await ensureVpnDevice()
        await startEmbeddedTunnel(device)
      }
    } catch (e) {
      meshMaintenanceError.value = String(e)
    } finally { attempting = false }
  }
  setTimeout(attempt, 3000)
  setInterval(attempt, 60_000)
})
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
          <ServerNotificationListener />
          <DesktopUpdateListener />
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
