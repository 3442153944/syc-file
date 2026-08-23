<script setup lang="ts">
// 首页仪表盘：一屏看清「系统在不在、同步顺不顺、盘还有多少、最近发生了什么」。
// 数据全部来自既有接口 + 新增的监控/存储接口，页面本身不做任何业务逻辑。
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import {
  NCard, NGrid, NGi, NStatistic, NSpace, NText, NProgress, NButton, NTag, NList,
  NListItem, NThing, NEmpty, NSpin, NAlert,
} from 'naive-ui'
import { getAvailableDisks, getDownloadHistory } from '@/api/file/fileApi'
import { listSyncTasks, listConflicts } from '@/api/sync/syncApi'
import { myStorage } from '@/api/admin/adminApi'
import type { DiskInfo } from '@/api/file/fileTypes'
import type { SyncTask, SyncConflict } from '@/api/sync/syncTypes'
import { useTransferStore } from '@/store/useTransferStore'
import { useMonitor } from '@/api/monitor/useMonitor'

const router = useRouter()
const transferStore = useTransferStore()

// 系统/网络指标走 WS 实时推送（3s 一帧），不再 HTTP 轮询——概览页跟着实时刷。
const { system, network } = useMonitor(3)
const disks = ref<DiskInfo[]>([])
const recentTasks = ref<SyncTask[]>([])
const conflicts = ref<SyncConflict[]>([])
const recentDownloads = ref<Array<{ file_name: string; created_at?: string; file_size?: number }>>([])
const quota = ref<{ used: number; total: number; percent: number } | null>(null)

const loading = ref(true)
const errors = ref<string[]>([])
let timer: number | undefined

const wsConnected = computed(() => transferStore.wsConnected)

/** 每块数据独立失败：任何一个接口挂了不该让整个首页空白。 */
async function loadAll() {
  errors.value = []
  const jobs: Array<Promise<void>> = [
    getAvailableDisks().then((d) => { disks.value = d.allowed_disks ?? [] }).catch((e) => { errors.value.push(`磁盘: ${e}`) }),
    listSyncTasks(1, 8).then((d) => { recentTasks.value = d.list ?? [] }).catch(() => {}),
    listConflicts().then((d) => { conflicts.value = d ?? [] }).catch(() => {}),
    getDownloadHistory(1, 5).then((d) => { recentDownloads.value = (d.list as any) ?? [] }).catch(() => {}),
    myStorage().then((d) => {
      quota.value = {
        used: d.config.used_quota,
        total: d.config.total_quota,
        percent: d.used_percent,
      }
    }).catch(() => {}),
  ]
  await Promise.all(jobs)
  loading.value = false
}

onMounted(() => {
  loadAll()
  timer = window.setInterval(loadAll, 15000) // 首页轮询放慢，不跟监控页抢
})
onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})

function fmtBytes(n?: number): string {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function usageStatus(p: number): 'success' | 'warning' | 'error' {
  if (p >= 90) return 'error'
  if (p >= 70) return 'warning'
  return 'success'
}

function taskStatusType(s: string): 'success' | 'warning' | 'error' | 'info' | 'default' {
  switch (s) {
    case 'completed': return 'success'
    case 'failed': return 'error'
    case 'syncing': return 'info'
    case 'waiting_unlock': return 'warning'
    default: return 'default'
  }
}

const activeTaskCount = computed(
  () => recentTasks.value.filter((t) => t.sync_status === 'pending' || t.sync_status === 'syncing').length,
)
</script>

<template>
  <div class="dashboard">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <NSpace align="center">
        <h2 style="margin: 0">概览</h2>
        <NTag :type="wsConnected ? 'success' : 'error'" size="small" round>
          {{ wsConnected ? '已连接服务器' : '未连接' }}
        </NTag>
        <NSpin :show="loading" size="small" />
      </NSpace>
      <NSpace>
        <NButton size="small" @click="loadAll">刷新</NButton>
        <NButton size="small" type="primary" @click="router.push('/file/upload')">上传文件</NButton>
      </NSpace>
    </NSpace>

    <NAlert v-if="errors.length" type="warning" closable style="margin-bottom: 12px">
      部分数据加载失败：{{ errors.join('；') }}
    </NAlert>

    <!-- 关键指标 -->
    <NGrid :cols="4" :x-gap="12" :y-gap="12" responsive="screen" item-responsive>
      <NGi span="4 m:2 l:1">
        <NCard size="small" hoverable @click="router.push('/monitor/system')" style="cursor: pointer">
          <NStatistic label="服务器 CPU">
            {{ system ? `${system.cpu.used_percent.toFixed(1)}%` : '—' }}
          </NStatistic>
          <NProgress v-if="system" type="line" :height="6" :show-indicator="false"
                     :percentage="Math.min(100, system.cpu.used_percent)"
                     :status="usageStatus(system.cpu.used_percent)" style="margin-top: 6px" />
          <NText depth="3" style="font-size: 12px">{{ system?.cpu.cores ?? '—' }} 核</NText>
        </NCard>
      </NGi>

      <NGi span="4 m:2 l:1">
        <NCard size="small" hoverable @click="router.push('/monitor/system')" style="cursor: pointer">
          <NStatistic label="服务器内存">
            {{ system ? `${system.memory.used_percent.toFixed(1)}%` : '—' }}
          </NStatistic>
          <NProgress v-if="system" type="line" :height="6" :show-indicator="false"
                     :percentage="Math.min(100, system.memory.used_percent)"
                     :status="usageStatus(system.memory.used_percent)" style="margin-top: 6px" />
          <NText depth="3" style="font-size: 12px">
            {{ system ? `${fmtBytes(system.memory.used)} / ${fmtBytes(system.memory.total)}` : '—' }}
          </NText>
        </NCard>
      </NGi>

      <NGi span="4 m:2 l:1">
        <NCard size="small" hoverable @click="router.push('/sync/manage')" style="cursor: pointer">
          <NStatistic label="在线设备" :value="network?.online_devices ?? 0" />
          <NText depth="3" style="font-size: 12px">
            {{ activeTaskCount }} 个同步任务进行中
          </NText>
        </NCard>
      </NGi>

      <NGi span="4 m:2 l:1">
        <NCard size="small" hoverable @click="router.push('/sync/manage')" style="cursor: pointer">
          <NStatistic label="待处理冲突" :value="conflicts.length" />
          <NText :depth="conflicts.length ? undefined : 3"
                 :type="conflicts.length ? 'warning' : undefined" style="font-size: 12px">
            {{ conflicts.length ? '需要人工选择保留哪一份' : '暂无冲突' }}
          </NText>
        </NCard>
      </NGi>
    </NGrid>

    <NGrid :cols="3" :x-gap="12" :y-gap="12" responsive="screen" item-responsive style="margin-top: 12px">
      <!-- 存储 -->
      <NGi span="3 m:3 l:1">
        <NCard size="small" title="我的存储" :segmented="{ content: true }">
          <template v-if="quota">
            <NProgress type="line" :percentage="Math.min(100, Math.round(quota.percent * 10) / 10)"
                       :status="usageStatus(quota.percent)" />
            <NText depth="3" style="font-size: 12px">
              已用 {{ fmtBytes(quota.used) }} / 配额 {{ quota.total > 0 ? fmtBytes(quota.total) : '不限' }}
            </NText>
          </template>
          <NEmpty v-else description="配额信息不可用" size="small" />
        </NCard>
      </NGi>

      <!-- 磁盘 -->
      <NGi span="3 m:3 l:2">
        <NCard size="small" title="服务器磁盘" :segmented="{ content: true }">
          <NSpace vertical size="small" v-if="disks.length">
            <div v-for="d in disks" :key="d.path">
              <NSpace justify="space-between" align="center" style="margin-bottom: 2px">
                <NText>{{ d.path }}</NText>
                <NText depth="3" style="font-size: 12px">可用 {{ d.free_gb }} / 总计 {{ d.total_gb }}</NText>
              </NSpace>
              <NProgress type="line" :height="8" :percentage="Math.round(d.used_percent * 10) / 10"
                         :status="usageStatus(d.used_percent)" />
            </div>
          </NSpace>
          <NEmpty v-else description="无可用磁盘" size="small" />
        </NCard>
      </NGi>
    </NGrid>

    <NGrid :cols="2" :x-gap="12" :y-gap="12" responsive="screen" item-responsive style="margin-top: 12px">
      <!-- 最近同步 -->
      <NGi span="2 m:2 l:1">
        <NCard size="small" title="最近同步" :segmented="{ content: true }">
          <template #header-extra>
            <NButton text size="small" @click="router.push('/sync/manage')">全部</NButton>
          </template>
          <NList v-if="recentTasks.length" hoverable clickable>
            <NListItem v-for="t in recentTasks" :key="t.id">
              <NThing :title="t.file_name">
                <template #description>
                  <NSpace size="small" align="center">
                    <NTag size="small" :type="taskStatusType(t.sync_status)">{{ t.sync_status }}</NTag>
                    <NText depth="3" style="font-size: 12px">{{ t.task_type }} · {{ fmtBytes(t.file_size) }}</NText>
                  </NSpace>
                </template>
              </NThing>
            </NListItem>
          </NList>
          <NEmpty v-else description="暂无同步记录" size="small" />
        </NCard>
      </NGi>

      <!-- 最近下载 -->
      <NGi span="2 m:2 l:1">
        <NCard size="small" title="最近下载" :segmented="{ content: true }">
          <template #header-extra>
            <NButton text size="small" @click="router.push('/transfers')">传输列表</NButton>
          </template>
          <NList v-if="recentDownloads.length" hoverable clickable>
            <NListItem v-for="(d, i) in recentDownloads" :key="i">
              <NThing :title="d.file_name">
                <template #description>
                  <NText depth="3" style="font-size: 12px">
                    {{ fmtBytes(d.file_size) }}{{ d.created_at ? ` · ${d.created_at}` : '' }}
                  </NText>
                </template>
              </NThing>
            </NListItem>
          </NList>
          <NEmpty v-else description="暂无下载记录" size="small" />
        </NCard>
      </NGi>
    </NGrid>
  </div>
</template>

<style scoped>
.dashboard {
  padding: 16px;
}
</style>
