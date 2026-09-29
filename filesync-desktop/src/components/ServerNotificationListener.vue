<script setup lang="ts">
// 无渲染监听器：接住服务端主动推送的通用通知（见 new_server 的 ws.NotifyAll，
// 目前用于资源告警），弹一条 naive-ui 通知。必须挂在 NNotificationProvider 里面
// 才能拿到 useNotification()，所以单独拆一个组件而不是直接写进 App.vue——
// App.vue 自己的 script setup 跟它渲染出来的 provider 不在同一层 inject 边界里。
import { onMounted, onUnmounted } from 'vue'
import { isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { useNotification } from 'naive-ui'

interface ServerNotificationPayload {
  title: string
  message: string
  level: string
  time: number
}

const notification = useNotification()
let unlisten: UnlistenFn | null = null

function show(payload: ServerNotificationPayload) {
  const opts = { title: payload.title, content: payload.message, duration: 6000, keepAliveOnHover: true }
  switch (payload.level) {
    case 'warning':
      notification.warning(opts)
      break
    case 'error':
      notification.error(opts)
      break
    case 'success':
      notification.success(opts)
      break
    default:
      notification.info(opts)
  }
}

onMounted(async () => {
  if (!isTauri()) return // Web 模式没有常驻 WS，这条通道暂不覆盖
  unlisten = await listen<ServerNotificationPayload>('server-notification', (e) => show(e.payload))
})

onUnmounted(() => {
  if (unlisten) unlisten()
})
</script>

<template></template>
