<script setup lang="ts">
// 组网总览：设备 / 用户 / 分配地址 / 在线状态，外加任意地址 ping。
// 管理员看自己机构，超管看全部；普通用户看不到这个页面（路由 MinLevel=2）。
import {computed, h, onBeforeUnmount, onMounted, ref} from 'vue'
import {
  NAlert, NButton, NCard, NDataTable, NInput, NInputNumber, NModal, NSpace, NSwitch, NTag, NText, useDialog, useMessage,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {isTauri} from '@tauri-apps/api/core'
import MeshProxyPanel from '@/components/MeshProxyPanel.vue'
import {meshNodeName} from '@/api/net'
import {
  ensureVpnDevice, fetchMeshOverview, startEmbeddedTunnel, stopEmbeddedTunnel,
  vpnBan, vpnKick, vpnReleaseDevice, vpnSuspend,
} from '@/api/vpn'

interface MeshRow {
  role?: string
  id: number
  device_id: string
  user_id: number
  username: string
  group: string
  org: string
  address: string
  status: string
  online: boolean
  endpoint: string
  last_seen: string | null
}

const message = useMessage()
const dialog = useDialog()
const rows = ref<MeshRow[]>([])
const loading = ref(false)
const serverAddress = ref('172.1.0.254')
const target = ref('172.1.0.254')
const pingOut = ref('')
const pinging = ref(false)
const tunnelUp = ref<boolean | null>(null)
/** 本次隧道实际使用的组网协调节点（按延迟自动选，和当前文件节点无关） */
const meshNode = ref('')
const showSuspend = ref(false)
const suspendTarget = ref<MeshRow | null>(null)
const suspendMinutes = ref(30)
const tunnelBusy = ref(false)
const autoStart = ref(false)
const autoTunnel = ref(localStorage.getItem('syc_vpn_auto_tunnel') === '1')
// 组网在线由后端按服务器 WireGuard 接口的实时流量判断，不再看 last_seen
const onlineCount = computed(() => rows.value.filter(r => r.online).length)

async function fetchOverview() {
  loading.value = true
  try {
    const data = await fetchMeshOverview()
    rows.value = (data.devices || []) as MeshRow[]
    if (data.server_address) {
      serverAddress.value = data.server_address
      target.value = data.server_address
    }
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

async function ping() {
  const t = target.value.trim()
  if (!t) return
  if (!isTauri()) {
    pingOut.value = 'Web 模式无法 ping 覆盖网地址，请用桌面端或 syc ping'
    return
  }
  pinging.value = true
  pingOut.value = ''
  const {invoke} = await import('@tauri-apps/api/core')
  const {listen} = await import('@tauri-apps/api/event')
  const unLine = await listen<string>('vpn-ping-line', e => {
    pingOut.value += e.payload + '\n'
  })
  const unDone = await listen<boolean>('vpn-ping-done', () => {
    pinging.value = false
    unLine()
    unDone()
  })
  try {
    await invoke('vpn_ping_stream', {target: t, count: 3})
  } catch (e) {
    pingOut.value += String(e)
    pinging.value = false
    unLine()
    unDone()
  }
}

async function startTunnel() {
  tunnelBusy.value = true
  try {
    const device = await ensureVpnDevice()
    const msg = await startEmbeddedTunnel(device)
    meshNode.value = meshNodeName()
    message.success(`${msg}（组网节点：${meshNode.value}）`)
    // wireguard 服务启动到接口就绪有延迟，轮询几次状态再显示
    for (let i = 0; i < 10; i++) {
      await new Promise(r => setTimeout(r, 500))
      await refreshTunnel()
      if (tunnelUp.value) break
    }
  } catch (e) {
    message.error(String(e))
  } finally {
    tunnelBusy.value = false
  }
}

async function stopTunnel() {
  tunnelBusy.value = true
  try {
    await stopEmbeddedTunnel()
    message.success('隧道已停止')
    await refreshTunnel()
  } catch (e) {
    message.error(String(e))
  } finally {
    tunnelBusy.value = false
  }
}

async function refreshTunnel() {
  if (!isTauri()) {
    tunnelUp.value = null
    return
  }
  try {
    const {invoke} = await import('@tauri-apps/api/core')
    tunnelUp.value = await invoke<boolean>('vpn_tunnel_status')
    if (tunnelUp.value) meshNode.value = meshNodeName()
  } catch {
    tunnelUp.value = null
  }
}

function openSuspend(row: MeshRow) {
  suspendTarget.value = row
  suspendMinutes.value = 30
  showSuspend.value = true
}

async function doSuspend() {
  if (!suspendTarget.value) return
  await deviceAction(suspendTarget.value, 'suspend', suspendMinutes.value)
  showSuspend.value = false
}

async function deviceAction(row: MeshRow, kind: 'kick' | 'suspend' | 'ban' | 'release', minutes = 30) {
  try {
    if (kind === 'kick') await vpnKick(row.id)
    else if (kind === 'suspend') await vpnSuspend(row.id, minutes)
    else if (kind === 'ban') await vpnBan(row.id, row.status !== 'banned')
    else await vpnReleaseDevice(row.id)
    message.success('已执行')
    await fetchOverview()
  } catch (e) {
    message.error(String(e))
  }
}

function confirmRelease(row: MeshRow) {
  dialog.warning({
    title: '永久释放',
    content: `确定释放 ${row.device_id}（${row.address}）？设备与地址都会被删除。`,
    positiveText: '释放',
    negativeText: '取消',
    onPositiveClick: () => deviceAction(row, 'release'),
  })
}

const columns: DataTableColumns<MeshRow> = [
  {title: '用户', key: 'username', width: 140},
  {title: '设备', key: 'device_id', width: 200, ellipsis: {tooltip: true}},
  {title: '地址', key: 'address', width: 130},
  {title: '组 / 机构', key: 'group', width: 180, render: r => `${r.group || '-'} / ${r.org || '-'}`},
  {
    title: '状态', key: 'online', width: 100,
    render: r => h(NTag, {size: 'small', type: r.online ? 'success' : 'default', bordered: false},
      {default: () => (r.online ? '在线' : r.status === 'revoked' ? '已吊销' : '离线')}),
  },
  {
    title: '最后在线', key: 'last_seen', width: 170,
    render: r => (r.last_seen ? new Date(r.last_seen).toLocaleString() : '-'),
  },
  {title: '端点', key: 'endpoint', ellipsis: {tooltip: true},
    render: r => r.endpoint || '(未知)'},
  {
    title: '操作', key: 'actions', width: 300,
    render: r => {
      if (r.role === 'server') return h(NText, {depth: 3}, {default: () => '—'})
      return h(NSpace, {size: 4}, {
        default: () => [
          h(NButton, {size: 'tiny', onClick: () => deviceAction(r, 'kick')}, {default: () => '踢下线'}),
          h(NButton, {size: 'tiny', onClick: () => openSuspend(r)}, {default: () => '停用'}),
          h(NButton, {size: 'tiny', type: r.status === 'banned' ? 'primary' : 'warning',
            onClick: () => deviceAction(r, 'ban')},
            {default: () => (r.status === 'banned' ? '解封' : '封禁')}),
          h(NButton, {size: 'tiny', type: 'error', onClick: () => confirmRelease(r)}, {default: () => '释放'}),
        ],
      })
    },
  },
]

async function loadAutoStart() {
  if (!isTauri()) return
  try {
    const {invoke} = await import('@tauri-apps/api/core')
    autoStart.value = await invoke<boolean>('autostart_get')
  } catch { /* ignore */ }
}

async function toggleAutoStart(v: boolean) {
  try {
    const {invoke} = await import('@tauri-apps/api/core')
    await invoke('autostart_set', {enabled: v})
    autoStart.value = v
    message.success(v ? '已开启开机自启' : '已关闭开机自启')
  } catch (e) {
    message.error(String(e))
  }
}

function toggleAutoTunnel(v: boolean) {
  autoTunnel.value = v
  localStorage.setItem('syc_vpn_auto_tunnel', v ? '1' : '0')
  message.success(v ? '启动后将自动组网' : '已关闭自动组网')
}

let overviewTimer: number | undefined
let tunnelTimer: number | undefined
onMounted(() => {
  fetchOverview()
  refreshTunnel()
  loadAutoStart()
  // 在线状态靠 2s 心跳刷新，页面停留时 5s 拉一次
  overviewTimer = window.setInterval(fetchOverview, 5_000)
  // 隧道状态要调 netsh，没必要 5s 一次；30s 足够
  tunnelTimer = window.setInterval(refreshTunnel, 30_000)
})

onBeforeUnmount(() => {
  if (overviewTimer) window.clearInterval(overviewTimer)
  if (tunnelTimer) window.clearInterval(tunnelTimer)
})
</script>

<template>
  <div class="mesh-page">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <h2 style="margin: 0">组网</h2>
      <NSpace align="center">
        <NSpace v-if="isTauri()" align="center" :size="6">
          <NText depth="3" style="font-size: 12px">开机自启</NText>
          <NSwitch :value="autoStart" size="small" @update:value="toggleAutoStart"/>
          <NText depth="3" style="font-size: 12px">启动自动组网</NText>
          <NSwitch :value="autoTunnel" size="small" @update:value="toggleAutoTunnel"/>
        </NSpace>
        <NButton v-if="isTauri()" size="small" :loading="tunnelBusy" @click="startTunnel">启动隧道</NButton>
        <NButton v-if="isTauri()" size="small" :loading="tunnelBusy" @click="stopTunnel">停止隧道</NButton>
        <NTag size="small" :type="tunnelUp === true ? 'success' : tunnelUp === false ? 'warning' : 'default'" :bordered="false">
          隧道 {{ tunnelUp === true ? '已启动' : tunnelUp === false ? '未启动' : '未知' }}
        </NTag>
        <NTag v-if="tunnelUp === true" size="small" :bordered="false">组网节点 {{ meshNode || '—' }}</NTag>
        <NTag size="small" round type="success" :bordered="false">在线 {{ onlineCount }} / {{ rows.length }}</NTag>
        <NButton size="small" :loading="loading" @click="fetchOverview">刷新</NButton>
      </NSpace>
    </NSpace>

    <MeshProxyPanel v-if="isTauri()"/>

    <NCard size="small" style="margin-bottom: 12px">
      <NSpace align="center">
        <NText strong>Ping 任意组内地址</NText>
        <NInput v-model:value="target" placeholder="例如 172.1.0.254" style="max-width: 220px"/>
        <NButton size="small" type="primary" :loading="pinging" @click="ping">Ping</NButton>
        <NText depth="3" style="font-size: 12px">
          默认目标为服务器 {{ serverAddress }}；ping 走隧道，需先在客户端启动组网隧道
          （Linux：<code>syc up</code>；Windows：点击「启动隧道」）
        </NText>
      </NSpace>
      <NAlert v-if="pingOut" type="info" :bordered="false" style="margin-top: 10px; white-space: pre-wrap">
        {{ pingOut }}
      </NAlert>
    </NCard>

    <NCard size="small">
      <NDataTable
        :columns="columns"
        :data="rows"
        :loading="loading"
        :row-key="(r: MeshRow) => r.id"
        size="small"
        :pagination="{pageSize: 20}"
      />
    </NCard>

    <NModal v-model:show="showSuspend" preset="card" title="临时停用" style="max-width: 380px">
      <NSpace vertical>
        <NText>设备 {{ suspendTarget?.device_id }}（{{ suspendTarget?.address }}）</NText>
        <NSpace align="center">
          <NInputNumber v-model:value="suspendMinutes" :min="1" :max="10080" style="width: 160px">
            <template #suffix>分钟</template>
          </NInputNumber>
          <NButton size="small" @click="suspendMinutes = 30">30m</NButton>
          <NButton size="small" @click="suspendMinutes = 60">1h</NButton>
          <NButton size="small" @click="suspendMinutes = 1440">1d</NButton>
        </NSpace>
        <NButton type="warning" @click="doSuspend">确定停用</NButton>
      </NSpace>
    </NModal>
  </div>
</template>
