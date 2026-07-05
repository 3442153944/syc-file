<script setup lang="ts">
// 传输列表：上传 / 下载 / 同步 三个 tab。
// 数据来自 useTransferStore，store 在 home.vue 初始化时已挂载事件监听。
import { onMounted, onBeforeUnmount, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useTransferStore } from '@/store/useTransferStore'
import { NButton, NSpace, NEmpty, NTag, NProgress, NPopconfirm } from 'naive-ui'

const router = useRouter()
const store = useTransferStore()

onMounted(() => {
  store.refreshSyncData()
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

        <div class="block-title">活动日志</div>
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
    </n-tabs>
  </div>
</template>

<style scoped>
.transfer-page {
  max-width: 900px;
  margin: 0 auto;
  background: #fff;
  border-radius: 8px;
  padding: 16px 20px;
  height: 100%;
  display: flex;
  flex-direction: column;
}
.page-head { margin-bottom: 12px; }
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
</style>
