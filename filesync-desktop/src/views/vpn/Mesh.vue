<script setup lang="ts">
// 组网总览：设备 / 用户 / 分配地址 / 在线状态，外加任意地址 ping。
// 管理员看自己机构，超管看全部；普通用户看不到这个页面（路由 MinLevel=2）。
import {computed, h, onMounted, ref} from 'vue'
import {
  NAlert, NButton, NCard, NDataTable, NInput, NSpace, NTag, NText, useDialog, useMessage,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {isTauri} from '@tauri-apps/api/core'
import {netState} from '@/api/net'

interface MeshRow {
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
const onlineCount = computed(() => rows.value.filter(r => r.online).length)

async function fetchOverview() {
  loading.value = true
  try {
    const res = await fetch(`${netState.serverUrl}/v1/vpn/admin/overview`, {
      headers: {Token: localStorage.getItem('token') || ''},
    })
    const body = await res.json()
    if (body?.code !== 200) throw new Error(body?.message || '读取失败')
    rows.value = body.data.devices || []
    if (body.data.server_address) {
      serverAddress.value = body.data.server_address
      target.value = body.data.server_address
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
  try {
    const {invoke} = await import('@tauri-apps/api/core')
    pingOut.value = await invoke<string>('vpn_ping', {target: t, count: 3})
  } catch (e) {
    pingOut.value = String(e)
  } finally {
    pinging.value = false
  }
}

async function deviceAction(row: MeshRow, path: string, method: 'POST' | 'DELETE' = 'POST', body?: unknown) {
  try {
    const res = await fetch(`${netState.serverUrl}/v1/vpn/admin/devices/${row.id}${path}`, {
      method,
      headers: {'Content-Type': 'application/json', Token: localStorage.getItem('token') || ''},
      body: method === 'DELETE' ? undefined : JSON.stringify(body || {}),
    })
    const data = await res.json()
    if (data?.code !== 200) throw new Error(data?.message || '操作失败')
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
    onPositiveClick: () => deviceAction(row, '', 'DELETE'),
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
    render: r => h(NSpace, {size: 4}, {
      default: () => [
        h(NButton, {size: 'tiny', onClick: () => deviceAction(r, '/kick')}, {default: () => '踢下线'}),
        h(NButton, {size: 'tiny', onClick: () => deviceAction(r, '/suspend', 'POST', {minutes: 30})}, {default: () => '停用30m'}),
        h(NButton, {size: 'tiny', type: r.status === 'banned' ? 'primary' : 'warning',
          onClick: () => deviceAction(r, '/ban', 'POST', {banned: r.status !== 'banned'})},
          {default: () => (r.status === 'banned' ? '解封' : '封禁')}),
        h(NButton, {size: 'tiny', type: 'error', onClick: () => confirmRelease(r)}, {default: () => '释放'}),
      ],
    }),
  },
]

onMounted(fetchOverview)
</script>

<template>
  <div class="mesh-page">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <h2 style="margin: 0">组网</h2>
      <NSpace align="center">
        <NTag size="small" round type="success" :bordered="false">在线 {{ onlineCount }} / {{ rows.length }}</NTag>
        <NButton size="small" :loading="loading" @click="fetchOverview">刷新</NButton>
      </NSpace>
    </NSpace>

    <NCard size="small" style="margin-bottom: 12px">
      <NSpace align="center">
        <NText strong>Ping 任意组内地址</NText>
        <NInput v-model:value="target" placeholder="例如 172.1.0.254" style="max-width: 220px"/>
        <NButton size="small" type="primary" :loading="pinging" @click="ping">Ping</NButton>
        <NText depth="3" style="font-size: 12px">默认目标为服务器 {{ serverAddress }}</NText>
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
  </div>
</template>
