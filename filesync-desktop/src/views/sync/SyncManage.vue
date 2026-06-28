<script setup lang="ts">
// 同步管理：创建同步文件夹、启动/停止同步引擎、查看状态与冲突待办。
// 直接粘贴文件到目录不会自动同步——必须先在这里创建同步文件夹并启动同步。
import { ref, onMounted, onUnmounted, computed, h } from 'vue'
import { invoke, isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { open } from '@tauri-apps/plugin-dialog'
import { useMessage, useDialog } from 'naive-ui'
import {
  NCard, NSpace, NButton, NInput, NSelect, NTag, NDataTable, NEmpty, NText,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import {
  createSyncFolder, listSyncFolders, deleteSyncFolder,
  listConflicts, resolveConflict, deleteConflict,
} from '@/api/sync/syncApi'
import type { SyncFolder, SyncConflict } from '@/api/sync/syncTypes'

const message = useMessage()
const dialog = useDialog()
const inTauri = isTauri()

const running = ref(false)
const wsConnected = ref(false)
const wsMessage = ref('未连接')
const deviceId = ref('')
const syncRoot = ref('')

const folders = ref<SyncFolder[]>([])
const conflicts = ref<SyncConflict[]>([])

// 新建表单
const form = ref({ name: '', localPath: '', remotePath: '', direction: 'two_way' })
const directionOptions = [
  { label: '双向同步', value: 'two_way' },
  { label: '仅上传', value: 'upload_only' },
  { label: '仅下载', value: 'download_only' },
]

let unlistenWs: UnlistenFn | null = null
let unlistenConflict: UnlistenFn | null = null

const statusTag = computed(() => {
  if (!running.value) return { type: 'default' as const, text: '未启动' }
  return wsConnected.value
    ? { type: 'success' as const, text: '同步中（已连接）' }
    : { type: 'warning' as const, text: '同步中（连接断开）' }
})

async function refreshAll() {
  if (!inTauri) return
  try {
    running.value = await invoke<boolean>('is_sync_running')
    const cfg = await invoke<any>('get_sync_config')
    deviceId.value = cfg.device_id ?? ''
    syncRoot.value = cfg.sync_root ?? ''
    if (!form.value.localPath) form.value.localPath = syncRoot.value
    folders.value = await listSyncFolders()
    conflicts.value = await listConflicts()
  } catch (e) {
    message.error(String(e))
  }
}

async function pickLocal() {
  const sel = await open({ directory: true, multiple: false, defaultPath: syncRoot.value || undefined })
  if (sel && typeof sel === 'string') form.value.localPath = sel
}

async function handleCreate() {
  const { name, localPath, remotePath, direction } = form.value
  if (!localPath || !remotePath) {
    message.warning('请填写本地目录和远端目录')
    return
  }
  try {
    await createSyncFolder(name || localPath, localPath, remotePath, direction)
    message.success('同步文件夹已创建')
    form.value.name = ''
    form.value.remotePath = ''
    await refreshAll()
  } catch (e) {
    message.error(String(e))
  }
}

async function handleDeleteFolder(row: SyncFolder) {
  dialog.warning({
    title: '删除同步文件夹',
    content: `确认删除「${row.name}」的同步配置？本地与远端文件不会被删除。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteSyncFolder(row.id)
        message.success('已删除')
        await refreshAll()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

async function handleStart() {
  try {
    await invoke('start_sync')
    message.success('同步引擎已启动')
    await refreshAll()
  } catch (e) {
    message.error(String(e))
  }
}

async function handleStop() {
  try {
    await invoke('stop_sync')
    message.info('同步引擎已停止')
    await refreshAll()
  } catch (e) {
    message.error(String(e))
  }
}

async function handleResolve(row: SyncConflict, resolution: 'accept_server' | 'keep_local') {
  try {
    await resolveConflict(row.id, resolution)
    message.success(resolution === 'accept_server' ? '已接受服务器版本' : '已保留本地版本')
    conflicts.value = await listConflicts()
  } catch (e) {
    message.error(String(e))
  }
}

async function handleDeleteConflict(row: SyncConflict) {
  try {
    await deleteConflict(row.id)
    conflicts.value = await listConflicts()
  } catch (e) {
    message.error(String(e))
  }
}

const folderColumns: DataTableColumns<SyncFolder> = [
  { title: '名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '本地目录', key: 'local_path', ellipsis: { tooltip: true } },
  { title: '远端目录', key: 'remote_path', ellipsis: { tooltip: true } },
  {
    title: '方向', key: 'direction', width: 90,
    render: (r) => directionOptions.find((o) => o.value === r.direction)?.label ?? r.direction,
  },
  {
    title: '操作', key: 'actions', width: 80,
    render: (r) => h(NButton, { size: 'tiny', type: 'error', ghost: true, onClick: () => handleDeleteFolder(r) }, { default: () => '删除' }),
  },
]

const conflictColumns: DataTableColumns<SyncConflict> = [
  { title: '文件', key: 'file_name', ellipsis: { tooltip: true } },
  { title: '相对路径', key: 'relative_path', ellipsis: { tooltip: true } },
  {
    title: '处理', key: 'actions', width: 230,
    render: (r) => h(NSpace, { size: 4 }, {
      default: () => [
        h(NButton, { size: 'tiny', type: 'primary', onClick: () => handleResolve(r, 'accept_server') }, { default: () => '用服务器版' }),
        h(NButton, { size: 'tiny', onClick: () => handleResolve(r, 'keep_local') }, { default: () => '保留本地版' }),
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => handleDeleteConflict(r) }, { default: () => '忽略' }),
      ],
    }),
  },
]

onMounted(async () => {
  await refreshAll()
  if (!inTauri) return
  unlistenWs = await listen<{ connected: boolean; message: string }>('ws-status', (e) => {
    wsConnected.value = e.payload.connected
    wsMessage.value = e.payload.message
  })
  unlistenConflict = await listen('sync-conflict', async () => {
    conflicts.value = await listConflicts()
  })
})

onUnmounted(() => {
  unlistenWs?.()
  unlistenConflict?.()
})
</script>

<template>
  <div class="sync-manage">
    <n-card v-if="!inTauri" title="提示">
      <n-empty description="同步管理仅在桌面端（Tauri）可用" />
    </n-card>

    <template v-else>
      <n-card title="同步状态" size="small">
        <n-space align="center" justify="space-between">
          <n-space align="center">
            <n-tag :type="statusTag.type" round>{{ statusTag.text }}</n-tag>
            <n-text depth="3">{{ wsMessage }}</n-text>
          </n-space>
          <n-space>
            <n-button type="primary" :disabled="running" @click="handleStart">启动同步</n-button>
            <n-button :disabled="!running" @click="handleStop">停止同步</n-button>
            <n-button quaternary @click="refreshAll">刷新</n-button>
          </n-space>
        </n-space>
        <div class="meta">
          <span>设备 ID：<code>{{ deviceId || '-' }}</code></span>
          <span>默认同步根：<code>{{ syncRoot || '-' }}</code></span>
        </div>
      </n-card>

      <n-card title="新建同步文件夹" size="small">
        <n-space vertical>
          <n-space align="center">
            <span class="lbl">本地目录</span>
            <n-input v-model:value="form.localPath" placeholder="例如默认同步根目录" style="width: 360px" />
            <n-button size="small" @click="pickLocal">选择目录</n-button>
          </n-space>
          <n-space align="center">
            <span class="lbl">远端目录</span>
            <n-input v-model:value="form.remotePath" placeholder="服务器允许的盘符路径，如 E:/FileSync/docs" style="width: 360px" />
          </n-space>
          <n-space align="center">
            <span class="lbl">名称/方向</span>
            <n-input v-model:value="form.name" placeholder="可选，默认用本地目录名" style="width: 200px" />
            <n-select v-model:value="form.direction" :options="directionOptions" style="width: 150px" />
            <n-button type="primary" @click="handleCreate">创建</n-button>
          </n-space>
          <n-text depth="3" style="font-size: 12px">
            提示：创建后需点「启动同步」才会开始监听。直接往目录里粘贴文件，引擎未启动或目录未注册时不会同步。
          </n-text>
        </n-space>
      </n-card>

      <n-card title="同步文件夹" size="small">
        <n-data-table :columns="folderColumns" :data="folders" :bordered="false" size="small" :row-key="(r:any)=>r.id">
          <template #empty><n-empty description="还没有同步文件夹" /></template>
        </n-data-table>
      </n-card>

      <n-card size="small">
        <template #header>冲突待办 <n-tag v-if="conflicts.length" type="warning" size="small" round>{{ conflicts.length }}</n-tag></template>
        <n-data-table :columns="conflictColumns" :data="conflicts" :bordered="false" size="small" :row-key="(r:any)=>r.id">
          <template #empty><n-empty description="暂无冲突" /></template>
        </n-data-table>
      </n-card>
    </template>
  </div>
</template>

<style scoped>
.sync-manage { display: flex; flex-direction: column; gap: 16px; }
.meta { margin-top: 10px; display: flex; gap: 24px; font-size: 13px; color: #666; }
.meta code { background: #f5f5f5; padding: 1px 6px; border-radius: 4px; }
.lbl { display: inline-block; width: 64px; color: #555; font-size: 13px; }
</style>
