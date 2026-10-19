<script setup lang="ts">
import {onMounted, onUnmounted, ref} from 'vue'
import {invoke} from '@tauri-apps/api/core'
import {listen, type UnlistenFn} from '@tauri-apps/api/event'
import {NAlert, NButton, NCard, NInput, NInputNumber, NSpace, NSwitch, NTag, NText, useMessage} from 'naive-ui'

interface ProxyStatus {
  running: boolean
  listen_address: string
  authenticated: boolean
  active: number
  accepted: number
  rejected: number
  uploaded: number
  downloaded: number
}

const message = useMessage()
const status = ref<ProxyStatus | null>(null)
const busy = ref(false)
const port = ref(7897)
const allowLan = ref(false)
const authEnabled = ref(false)
const username = ref('')
const password = ref('')
let unlisten: UnlistenFn | undefined
let disposed = false

function applyStatus(value: ProxyStatus) {
  status.value = value
  if (value.running) {
    authEnabled.value = value.authenticated
    allowLan.value = value.listen_address.startsWith('0.0.0.0:')
    port.value = Number(value.listen_address.split(':').pop())
  }
}

function bytes(n = 0) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(1)} MiB`
}

async function toggle() {
  busy.value = true
  try {
    if (status.value?.running) {
      status.value = await invoke<ProxyStatus>('mesh_proxy_stop')
      message.success('代理已停止，现有连接已关闭')
    } else {
      status.value = await invoke<ProxyStatus>('mesh_proxy_start', {
        options: {
          port: port.value, allow_lan: allowLan.value, auth_enabled: authEnabled.value,
          username: authEnabled.value ? username.value : '', password: authEnabled.value ? password.value : '',
        },
      })
      password.value = ''
      message.success('组网代理已启动')
    }
  } catch (e) {
    message.error(String(e))
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  try {
    unlisten = await listen<ProxyStatus>('mesh-proxy-status', e => applyStatus(e.payload))
    if (disposed) { unlisten(); return }
    applyStatus(await invoke<ProxyStatus>('mesh_proxy_status'))
  } catch (e) { message.error(String(e)) }
})
onUnmounted(() => { disposed = true; unlisten?.() })
</script>

<template>
  <NCard title="本机 / 局域网代理" size="small" style="margin-bottom: 12px">
    <NSpace vertical :size="12">
      <NText depth="3">先启动组网隧道，再让其他软件使用此 HTTP / SOCKS5 混合端口访问 172.1.0.0/16 内的服务。</NText>
      <NSpace align="center">
        <NText>端口</NText>
        <NInputNumber v-model:value="port" :min="1" :max="65535" :precision="0" :show-button="false"
                      :disabled="status?.running || busy" style="width: 110px"/>
        <NText>允许局域网连接</NText>
        <NSwitch v-model:value="allowLan" :disabled="status?.running || busy"/>
        <NText>需要密码</NText>
        <NSwitch v-model:value="authEnabled" :disabled="status?.running || busy"/>
        <NInput v-if="authEnabled" v-model:value="username" placeholder="代理用户名" :disabled="status?.running || busy" style="width: 210px"/>
        <NInput v-if="authEnabled" v-model:value="password" type="password" show-password-on="click" autocomplete="off"
                placeholder="代理密码" :disabled="status?.running || busy" style="width: 230px"/>
        <NButton :type="status?.running ? 'warning' : 'primary'" :loading="busy" @click="toggle">
          {{ status?.running ? '停止代理' : '启动代理' }}
        </NButton>
        <NTag :type="status?.running ? 'success' : 'default'" :bordered="false">
          {{ status?.running ? `监听 ${status.listen_address}` : '未启动' }}
        </NTag>
      </NSpace>
      <NAlert v-if="allowLan || (status?.running && status.listen_address.startsWith('0.0.0.0:'))" type="info" :bordered="false">
        局域网软件填入这台电脑的局域网 IP 和代理端口；开启「需要密码」时，再填写代理用户名和密码。
      </NAlert>
      <NText v-if="status?.running" code>
        HTTP：http://127.0.0.1:{{ status.listen_address.split(':').pop() }}　
        SOCKS5：socks5h://127.0.0.1:{{ status.listen_address.split(':').pop() }}
      </NText>
      <NSpace v-if="status?.running" :size="20">
        <NText>当前连接 {{ status.active }} / 128</NText>
        <NText>累计接入 {{ status.accepted }} · 失败/拒绝 {{ status.rejected }}</NText>
        <NText>转发流量 ↑ {{ bytes(status.uploaded) }}　↓ {{ bytes(status.downloaded) }}</NText>
        <NText>{{ status.authenticated ? '已启用代理认证' : '无需密码' }}</NText>
      </NSpace>
      <NText depth="3" style="font-size: 12px">
        支持 HTTP、HTTPS CONNECT 与 SOCKS5 TCP；域名由代理端解析且须指向组网地址。UDP、ICMP 和公网目标不经此端口转发。停止代理会关闭已有连接，退出应用后不保留监听。
      </NText>
    </NSpace>
  </NCard>
</template>
