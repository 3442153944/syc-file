<script setup lang="ts">
// 同步管理：创建同步文件夹、启动/停止同步引擎、查看状态与冲突待办。
import {ref, onMounted, onUnmounted, computed, h} from 'vue'
import {invoke, isTauri} from '@tauri-apps/api/core'
import {listen, type UnlistenFn} from '@tauri-apps/api/event'
import {open} from '@tauri-apps/plugin-dialog'
import {useRouter} from 'vue-router'
import {useMessage, useDialog} from 'naive-ui'
import {
  NCard, NSpace, NButton, NInput, NSelect, NTag, NDataTable, NEmpty, NText, NSwitch,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {
  saveSyncFolder, getSyncFolder, deleteSyncFolder, updateSyncFolder,
  listConflicts, resolveConflict, deleteConflict,
} from '@/api/sync/syncApi'
import type {SyncFolder, SyncConflict} from '@/api/sync/syncTypes'
import {useTransferStore} from '@/store/useTransferStore'

const message = useMessage()
const dialog = useDialog()
const router = useRouter()
const inTauri = isTauri()
const transferStore = useTransferStore()

const running = ref(false)
const wsConnected = computed(() => transferStore.wsConnected)
const wsMessage = ref('')
const deviceId = ref('')
const syncRoot = ref('')

// 系统始终只保留一个同步文件夹：null = 未配置
const folder = ref<SyncFolder | null>(null)
const conflicts = ref<SyncConflict[]>([])

// 未配置时自动展开表单；已配置时点「编辑」才展开
const editing = ref(false)
const formVisible = computed(() => !folder.value || editing.value)

const form = ref({name: '', localPath: '', remotePath: '', direction: 'two_way'})
const directionOptions = [
  {label: '双向同步', value: 'two_way'},
  {label: '仅上传', value: 'upload_only'},
  {label: '仅下载', value: 'download_only'},
]

let unlistenWs: UnlistenFn | null = null
let unlistenConflict: UnlistenFn | null = null

const statusTag = computed(() => {
  if (!running.value) return {type: 'default' as const, text: '未启动'}
  if (!wsConnected.value) return {type: 'warning' as const, text: '同步中（连接断开）'}
  return {type: 'success' as const, text: '同步中（已连接）'}
})

async function refreshAll() {
  if (!inTauri) return
  try {
    running.value = await invoke<boolean>('is_sync_running')
    const cfg = await invoke<any>('get_sync_config')
    deviceId.value = cfg.device_id ?? ''
    syncRoot.value = cfg.sync_root ?? ''
    folder.value = await getSyncFolder()
    if (!form.value.localPath) form.value.localPath = folder.value?.local_path || syncRoot.value
    conflicts.value = await listConflicts()
  } catch (e) {
    message.error(String(e))
  }
}

async function pickLocal() {
  const sel = await open({directory: true, multiple: false, defaultPath: syncRoot.value || undefined})
  if (sel) form.value.localPath = sel
}

function startEdit() {
  if (folder.value) {
    form.value = {
      name: folder.value.name,
      localPath: folder.value.local_path,
      remotePath: folder.value.remote_path,
      direction: folder.value.direction,
    }
  }
  editing.value = true
}

function cancelEdit() {
  editing.value = false
  form.value.name = ''
  form.value.remotePath = ''
}

async function handleSave() {
  const {name, localPath, remotePath, direction} = form.value
  if (!localPath || !remotePath) {
    message.warning('请填写本地目录和远端目录')
    return
  }
  try {
    await saveSyncFolder(name || localPath, localPath, remotePath, direction)
    message.success('同步文件夹已保存')
    editing.value = false
    form.value.name = ''
    form.value.remotePath = ''
    await refreshAll()
  } catch (e) {
    message.error(String(e))
  }
}

async function handleDeleteFolder() {
  if (!folder.value) return
  dialog.warning({
    title: '删除同步文件夹',
    content: `确认删除「${folder.value.name}」的同步配置？本地与远端文件不会被删除。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteSyncFolder()
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

async function handleToggleFolder(enabled: boolean) {
  if (!folder.value) return
  try {
    await updateSyncFolder({enabled})
    folder.value.enabled = enabled
    message.success(enabled ? '已启用同步' : '已停用同步')
  } catch (e) {
    message.error(String(e))
  }
}

const directionTagType = (d: string): 'info' | 'success' | 'warning' =>
    d === 'two_way' ? 'info' : d === 'upload_only' ? 'success' : 'warning'
const directionLabel = (d: string) => directionOptions.find((o) => o.value === d)?.label ?? d

const conflictColumns: DataTableColumns<SyncConflict> = [
  {title: '文件', key: 'file_name', ellipsis: {tooltip: true}},
  {title: '相对路径', key: 'relative_path', ellipsis: {tooltip: true}},
  {
    title: '处理', key: 'actions', width: 230,
    render: (r) => h(NSpace, {size: 4}, {
      default: () => [
        h(NButton, {
          size: 'tiny',
          type: 'primary',
          onClick: () => handleResolve(r, 'accept_server')
        }, {default: () => '用服务器版'}),
        h(NButton, {size: 'tiny', onClick: () => handleResolve(r, 'keep_local')}, {default: () => '保留本地版'}),
        h(NButton, {size: 'tiny', quaternary: true, onClick: () => handleDeleteConflict(r)}, {default: () => '忽略'}),
      ],
    }),
  },
]

onMounted(async () => {
  await refreshAll()
  if (!inTauri) return
  unlistenWs = await listen<{ connected: boolean; message: string }>('ws-status', (e) => {
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
      <n-empty description="同步管理仅在桌面端（Tauri）可用"/>
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
            <n-button quaternary @click="router.push('/transfers')">同步记录 →</n-button>
          </n-space>
        </n-space>
        <div class="meta">
          <span>设备 ID：<code>{{ deviceId || '-' }}</code></span>
          <span>默认同步根：<code>{{ syncRoot || '-' }}</code></span>
        </div>
      </n-card>

      <n-card title="同步文件夹" size="small">
        <template v-if="formVisible">
          <n-space vertical>
            <n-space align="center">
              <span class="lbl">本地目录</span>
              <n-input v-model:value="form.localPath" placeholder="例如默认同步根目录" style="width: 360px"/>
              <n-button size="small" @click="pickLocal">选择目录</n-button>
            </n-space>
            <n-space align="center">
              <span class="lbl">远端目录</span>
              <n-input v-model:value="form.remotePath" placeholder="服务器允许的盘符路径，如 E:/FileSync/docs"
                       style="width: 360px"/>
            </n-space>
            <n-space align="center">
              <span class="lbl">名称/方向</span>
              <n-input v-model:value="form.name" placeholder="可选，默认用本地目录名" style="width: 200px"/>
              <n-select v-model:value="form.direction" :options="directionOptions" style="width: 150px"/>
              <n-button type="primary" @click="handleSave">保存</n-button>
              <n-button v-if="folder" quaternary @click="cancelEdit">取消</n-button>
            </n-space>
            <n-text depth="3" style="font-size: 12px">
              提示：整个系统始终只保留一个同步文件夹。保存后需点「启动同步」才会开始监听。
            </n-text>
          </n-space>
        </template>
        <template v-else-if="folder">
          <n-space vertical>
            <div class="folder-row"><span class="lbl">名称</span>{{ folder.name || '-' }}</div>
            <div class="folder-row"><span class="lbl">本地目录</span><code>{{ folder.local_path }}</code></div>
            <div class="folder-row"><span class="lbl">远端目录</span><code>{{ folder.remote_path }}</code></div>
            <div class="folder-row">
              <span class="lbl">方向</span>
              <n-tag size="small" :type="directionTagType(folder.direction)" :bordered="false">
                {{ directionLabel(folder.direction) }}
              </n-tag>
            </div>
            <n-space align="center">
              <span class="lbl">启用</span>
              <n-switch size="small" :value="folder.enabled" @update:value="handleToggleFolder"/>
            </n-space>
            <n-space>
              <n-button size="small" @click="startEdit">编辑</n-button>
              <n-button size="small" type="error" ghost @click="handleDeleteFolder">删除</n-button>
            </n-space>
          </n-space>
        </template>
      </n-card>

      <n-card size="small">
        <template #header>冲突待办
          <n-tag v-if="conflicts.length" type="warning" size="small" round>{{ conflicts.length }}</n-tag>
        </template>
        <n-data-table :columns="conflictColumns" :data="conflicts" :bordered="false" size="small"
                      :row-key="(r:any)=>r.id">
          <template #empty>
            <n-empty description="暂无冲突"/>
          </template>
        </n-data-table>
      </n-card>
    </template>
  </div>
</template>

<style scoped>
.sync-manage {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.meta {
  margin-top: 10px;
  display: flex;
  gap: 24px;
  font-size: 13px;
  color: #666;
}

.meta code {
  background: #f5f5f5;
  padding: 1px 6px;
  border-radius: 4px;
}

.lbl {
  display: inline-block;
  width: 64px;
  color: #555;
  font-size: 13px;
}

.folder-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.folder-row code {
  background: #f5f5f5;
  padding: 1px 6px;
  border-radius: 4px;
}
</style>
