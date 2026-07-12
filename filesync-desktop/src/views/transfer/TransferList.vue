<script setup lang="ts">
// 传输列表：上传 / 下载 / 同步 三个 tab。
// 数据来自 useTransferStore，store 在 home.vue 初始化时已挂载事件监听；
// 「同步记录」为服务端分页数据（GET /sync/tasks?page=..），支持批量清理。
import { onMounted, onBeforeUnmount, computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useTransferStore } from '@/store/useTransferStore'
import { listSyncTasks, clearSyncTasks } from '@/api/sync/syncApi'
import type { SyncTask } from '@/api/sync/syncTypes'
import { useMessage } from 'naive-ui'
import { NButton, NSpace, NEmpty, NTag, NProgress, NPopconfirm, NPagination, NSelect, NSpin } from 'naive-ui'

const router = useRouter()
const store = useTransferStore()
const message = useMessage()

// ── 服务端同步记录（分页）────────────────────────────────────────────────
const taskList = ref<SyncTask[]>([])
const taskTotal = ref(0)
const taskPage = ref(1)
const taskPageSize = ref(20)
const taskStatus = ref('') // '' = 全部
const taskLoading = ref(false)
const taskStatusOptions = [
  { label: '全部状态', value: '' },
  { label: '已完成', value: 'completed' },
  { label: '失败', value: 'failed' },
  { label: '待处理', value: 'pending' },
  { label: '同步中', value: 'syncing' },
  { label: '等待解锁', value: 'waiting_unlock' },
]

async function loadTasks() {
  taskLoading.value = true
  try {
    const page = await listSyncTasks(taskPage.value, taskPageSize.value, taskStatus.value)
    taskList.value = page.list ?? []
    taskTotal.value = page.total
  } catch (e) {
    message.error(`加载同步记录失败: ${e}`)
  } finally {
    taskLoading.value = false
  }
}

function onTaskPageChange(p: number) {
  taskPage.value = p
  loadTasks()
}

function onTaskStatusChange() {
  taskPage.value = 1
  loadTasks()
}

async function clearFinishedTasks() {
  try {
    const n = await clearSyncTasks('')
    message.success(`已清理 ${n} 条已完成/失败记录`)
    taskPage.value = 1
    await loadTasks()
  } catch (e) {
    message.error(`清理失败: ${e}`)
  }
}

const taskStatusMeta = (s: string): { label: string; type: 'success' | 'error' | 'info' | 'warning' | 'default' } => {
  switch (s) {
    case 'completed': return { label: '已完成', type: 'success' }
    case 'failed': return { label: '失败', type: 'error' }
    case 'syncing': return { label: '同步中', type: 'info' }
    case 'pending': return { label: '待处理', type: 'default' }
    case 'waiting_unlock': return { label: '等待解锁', type: 'warning' }
    default: return { label: s, type: 'default' }
  }
}
const taskTypeLabel = (t: string): string =>
  ({ download: '下载', delete: '删除', mkdir: '建目录', upload: '上传' })[t] ?? t

onMounted(() => {
  store.refreshSyncData()
  loadTasks()
})
onBeforeUnmount(() => {
  /* 不 dispose，store 生命周期跟随主布局，页面卸载只停刷新 */
})

const formatBytes = (n: number): string => {
  if (!n) return '-'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0, v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`
}
const formatTime = (ts?: number): string => {
  if (!ts) return ''
  return new Date(ts).toLocaleTimeString('zh-CN', { hour12: false })
}

const uploadPercent = (u: { sent: number; total: number }): number => {
  if (!u.total) return 0
  return Math.min(100, Math.round((u.sent / u.total) * 100))
}

const uploadList = computed(() => store.uploads)
const downloadList = computed(() => store.downloads)
const syncList = computed(() => store.syncEvents)

const goBack = () => router.back()
</script>

<template>
  <div class="transfer-page">
    <div class="page-head">
      <n-space align="center" justify="space-between">
        <h2 class="title">传输列表</h2>
        <n-button size="small" @click="goBack">返回</n-button>
      </n-space>
    </div>

    <n-tabs type="line" animated>
      <!-- ── 上传 ─────────────────────────────────────────── -->
      <n-tab-pane name="upload" :tab="`上传 (${store.activeUploads.length})`">
        <div class="tab-actions">
          <n-button size="tiny" quaternary @click="store.clearFinished('uploads')">清除已完成</n-button>
        </div>
        <div v-if="uploadList.length === 0" class="empty">
          <n-empty description="暂无上传任务" />
        </div>
        <div v-else class="rows">
          <div v-for="u in uploadList" :key="u.id" class="row">
            <div class="row-main">
              <div class="row-name" :title="u.name">{{ u.name }}</div>
              <n-progress
                v-if="u.status === 'uploading'"
                :percentage="uploadPercent(u)"
                :height="8"
                :show-indicator="false"
                status="default"
              />
              <div class="row-meta">
                <span>{{ formatBytes(u.sent) }} / {{ u.total ? formatBytes(u.total) : '…' }}</span>
                <span>{{ formatTime(u.startedAt) }}</span>
              </div>
              <div v-if="u.error" class="row-err">{{ u.error }}</div>
            </div>
            <n-tag
              size="small"
              :type="u.status === 'done' ? 'success' : u.status === 'error' ? 'error' : 'info'"
            >
              {{ u.status === 'uploading' ? '上传中' : u.status === 'done' ? '完成' : '失败' }}
            </n-tag>
          </div>
        </div>
      </n-tab-pane>

      <!-- ── 下载 ─────────────────────────────────────────── -->
      <n-tab-pane name="download" :tab="`下载 (${store.activeDownloads.length})`">
        <div class="tab-actions">
          <n-button size="tiny" quaternary @click="store.clearFinished('downloads')">清除已完成</n-button>
        </div>
        <div v-if="downloadList.length === 0" class="empty">
          <n-empty description="暂无下载任务" />
        </div>
        <div v-else class="rows">
          <div v-for="d in downloadList" :key="d.id" class="row">
            <div class="row-main">
              <div class="row-name" :title="d.name">{{ d.name }}</div>
              <div class="row-meta">
                <span :title="d.path">{{ d.path }}</span>
                <span>{{ formatTime(d.startedAt) }}</span>
              </div>
              <div v-if="d.error" class="row-err">{{ d.error }}</div>
            </div>
            <n-tag
              size="small"
              :type="
                d.status === 'done'
                  ? 'success'
                  : d.status === 'error'
                    ? 'error'
                    : d.status === 'blocked'
                      ? 'warning'
                      : 'info'
              "
            >
              {{
                d.status === 'downloading'
                  ? '下载中'
                  : d.status === 'done'
                    ? '完成'
                    : d.status === 'blocked'
                      ? '等待解锁'
                      : '失败'
              }}
            </n-tag>
          </div>
        </div>
      </n-tab-pane>

      <!-- ── 同步 ─────────────────────────────────────────── -->
      <n-tab-pane name="sync" :tab="`同步${store.activeSyncUploads.length ? ' (' + store.activeSyncUploads.length + ')' : ''}`">
        <div class="sync-status">
          <n-tag :type="store.wsConnected ? 'success' : 'error'" size="small">
            {{ store.wsConnected ? 'WS 已连接' : 'WS 断开' }}
          </n-tag>
          <n-tag :type="store.syncEngineRunning ? 'success' : 'default'" size="small">
            {{ store.syncEngineRunning ? '引擎运行中' : '引擎未启动' }}
          </n-tag>
          <n-tag size="small">待处理任务 {{ store.pendingTasks.length }}</n-tag>
          <n-tag :type="store.conflicts.length ? 'warning' : 'default'" size="small">
            冲突 {{ store.conflicts.length }}
          </n-tag>
          <n-button size="tiny" quaternary @click="store.clearFinished('syncEvents')">清空日志</n-button>
        </div>

        <div v-if="store.conflicts.length > 0" class="conflict-block">
          <div class="block-title">冲突待办</div>
          <div v-for="c in store.conflicts" :key="c.id" class="row">
            <div class="row-main">
              <div class="row-name" :title="c.relative_path">{{ c.file_name }}</div>
              <div class="row-meta">
                <span>{{ c.relative_path }}</span>
              </div>
            </div>
            <n-popconfirm @positive-click="() => {}">
              <template #trigger>
                <n-button size="tiny" type="warning" ghost>处理</n-button>
              </template>
              请在「同步管理」页处理冲突
            </n-popconfirm>
          </div>
        </div>

        <div class="block-title">活动日志（本次运行）</div>
        <div v-if="syncList.length === 0" class="empty">
          <n-empty description="暂无同步活动" />
        </div>
        <div v-else class="rows">
          <div v-for="s in syncList" :key="s.id" class="row">
            <div class="row-main">
              <div class="row-name" :title="s.path">{{ s.path.split(/[\\/]/).pop() || s.path }}</div>
              <div class="row-meta">
                <span :title="s.path">{{ s.path }}</span>
                <span>{{ formatTime(s.time) }}</span>
              </div>
              <div v-if="s.error" class="row-err">{{ s.error }}</div>
            </div>
            <n-tag
              size="small"
              :type="
                s.status === 'done'
                  ? 'success'
                  : s.status === 'error'
                    ? 'error'
                    : s.status === 'uploading'
                      ? 'info'
                      : 'default'
              "
            >
              {{ s.kind }}{{ s.status === 'uploading' ? '(进行中)' : s.status === 'done' ? '(完成)' : s.status === 'error' ? '(失败)' : '' }}
            </n-tag>
          </div>
        </div>
      </n-tab-pane>

      <!-- ── 同步记录（服务端，分页）───────────────────────── -->
      <n-tab-pane name="records" :tab="`同步记录 (${taskTotal})`">
        <div class="tab-actions records-actions">
          <n-select
            v-model:value="taskStatus"
            :options="taskStatusOptions"
            size="small"
            style="width: 130px"
            @update:value="onTaskStatusChange"
          />
          <n-space>
            <n-button size="tiny" quaternary :loading="taskLoading" @click="loadTasks">刷新</n-button>
            <n-popconfirm @positive-click="clearFinishedTasks">
              <template #trigger>
                <n-button size="tiny" type="error" ghost>清理已完成/失败</n-button>
              </template>
              删除全部已完成与失败的任务记录？进行中的任务不受影响。
            </n-popconfirm>
          </n-space>
        </div>

        <n-spin :show="taskLoading">
          <div v-if="taskList.length === 0" class="empty">
            <n-empty description="暂无同步记录" />
          </div>
          <div v-else class="rows">
            <div v-for="t in taskList" :key="t.id" class="row">
              <div class="row-main">
                <div class="row-name" :title="t.relative_path">{{ t.file_name || t.relative_path }}</div>
                <div class="row-meta">
                  <span :title="t.relative_path">
                    #{{ t.id }} · {{ taskTypeLabel(t.task_type) }} · {{ t.relative_path }}
                  </span>
                  <span>{{ t.created_at?.slice(0, 19).replace('T', ' ') }}</span>
                </div>
                <div v-if="t.error" class="row-err">{{ t.error }}</div>
              </div>
              <n-tag size="small" :type="taskStatusMeta(t.sync_status).type">
                {{ taskStatusMeta(t.sync_status).label }}
              </n-tag>
            </div>
          </div>
        </n-spin>

        <div class="pager">
          <n-pagination
            :page="taskPage"
            :item-count="taskTotal"
            :page-size="taskPageSize"
            @update:page="onTaskPageChange"
          />
        </div>
      </n-tab-pane>
    </n-tabs>
  </div>
</template>

<style scoped>
.transfer-page {
  max-width: 900px;
  margin: 0 auto;
  background: #fff;
  border-radius: 8px;
  padding: 0 20px 16px;
  height: 100%;
  overflow-y: auto; /* 页面自身滚动，头部/Tab 栏黏着 */
}
/* 黏着定位：滚动时标题与 Tab 栏固定在顶部 */
.page-head {
  position: sticky;
  top: 0;
  z-index: 10;
  background: #fff;
  padding: 16px 0 12px;
}
.transfer-page :deep(.n-tabs-nav) {
  position: sticky;
  top: 56px;
  z-index: 9;
  background: #fff;
}
.title { margin: 0; font-size: 18px; }
.tab-actions { display: flex; justify-content: flex-end; margin-bottom: 8px; }
.empty { padding: 40px 0; }
.rows { display: flex; flex-direction: column; gap: 8px; }
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid #f0f0f0;
  border-radius: 6px;
}
.row-main { flex: 1; min-width: 0; }
.row-name {
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.row-meta {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: #999;
  margin-top: 4px;
}
.row-err { font-size: 11px; color: #f56c6c; margin-top: 2px; }
.sync-status { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 12px; }
.conflict-block { margin-bottom: 16px; }
.block-title { font-size: 13px; font-weight: 600; color: #606266; margin: 12px 0 8px; }
.records-actions { display: flex; justify-content: space-between; align-items: center; }
.pager { display: flex; justify-content: center; margin-top: 12px; }
</style>
