<script setup lang="ts">
// 系统监控：CPU / 内存 / 磁盘 / 主机信息，定时轮询 /v1/monitor/system。
// 指标全部取自服务端 gopsutil，反映的是**服务器**而非本机。
import { ref, onMounted, onUnmounted, h } from 'vue'
import { NCard, NGrid, NGi, NProgress, NSpace, NStatistic, NTag, NText, NDataTable, NSelect, NSpin } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getSystemMetrics } from '@/api/admin/adminApi'
import type { SystemMetrics } from '@/api/admin/adminTypes'

const metrics = ref<SystemMetrics | null>(null)
const loading = ref(false)
const errorMsg = ref('')
const intervalMs = ref(3000)
let timer: number | undefined

const intervalOptions = [
  { label: '1 秒', value: 1000 },
  { label: '3 秒', value: 3000 },
  { label: '5 秒', value: 5000 },
  { label: '10 秒', value: 10000 },
  { label: '暂停', value: 0 },
]

async function refresh() {
  loading.value = true
  try {
    metrics.value = await getSystemMetrics()
    errorMsg.value = ''
  } catch (e: any) {
    errorMsg.value = String(e?.message || e)
  } finally {
    loading.value = false
  }
}

function restartTimer() {
  if (timer) window.clearInterval(timer)
  timer = undefined
  if (intervalMs.value > 0) {
    timer = window.setInterval(refresh, intervalMs.value)
  }
}

onMounted(() => {
  refresh()
  restartTimer()
})
onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})

function fmtBytes(n: number): string {
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

function fmtUptime(sec: number): string {
  if (!sec) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return d > 0 ? `${d} 天 ${h} 小时` : h > 0 ? `${h} 小时 ${m} 分` : `${m} 分`
}

/** 用量配色与安卓 DiskCard 一致：70% 起黄、90% 起红 */
function usageStatus(percent: number): 'success' | 'warning' | 'error' {
  if (percent >= 90) return 'error'
  if (percent >= 70) return 'warning'
  return 'success'
}

const diskColumns: DataTableColumns<SystemMetrics['disks'][number]> = [
  { title: '挂载点', key: 'path', width: 120 },
  { title: '文件系统', key: 'fstype', width: 100 },
  { title: '总容量', key: 'total', render: (r) => fmtBytes(r.total), width: 110 },
  { title: '已用', key: 'used', render: (r) => fmtBytes(r.used), width: 110 },
  { title: '可用', key: 'free', render: (r) => fmtBytes(r.free), width: 110 },
  {
    title: '使用率',
    key: 'used_percent',
    render: (r) =>
      h(NProgress, {
        type: 'line',
        percentage: Math.round(r.used_percent * 10) / 10,
        status: usageStatus(r.used_percent),
        height: 12,
      }),
  },
]

</script>

<template>
  <div class="monitor-system">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <NSpace align="center">
        <h2 style="margin: 0">系统监控</h2>
        <NText depth="3" v-if="metrics">
          {{ metrics.host.hostname }} · {{ metrics.host.platform }} {{ metrics.host.platform_version }}
          ({{ metrics.host.kernel_arch }})
        </NText>
        <NSpin :show="loading" size="small" />
      </NSpace>
      <NSpace align="center">
        <NText depth="3">刷新间隔</NText>
        <NSelect
          v-model:value="intervalMs"
          :options="intervalOptions"
          style="width: 110px"
          @update:value="restartTimer"
        />
      </NSpace>
    </NSpace>

    <NCard v-if="errorMsg" size="small" style="margin-bottom: 12px">
      <NText type="error">读取失败：{{ errorMsg }}</NText>
    </NCard>

    <template v-if="metrics">
      <NGrid :cols="4" :x-gap="12" :y-gap="12" responsive="screen" item-responsive>
        <NGi span="4 m:2 l:1">
          <NCard size="small" title="CPU">
            <NProgress
              type="dashboard"
              :percentage="Math.round(metrics.cpu.used_percent * 10) / 10"
              :status="usageStatus(metrics.cpu.used_percent)"
            />
            <NText depth="3" style="display: block; margin-top: 8px; font-size: 12px">
              {{ metrics.cpu.cores }} 核 · {{ metrics.cpu.model_name || '未知型号' }}
            </NText>
            <NText v-if="metrics.cpu.load1 > 0" depth="3" style="font-size: 12px">
              负载 {{ metrics.cpu.load1.toFixed(2) }} / {{ metrics.cpu.load5.toFixed(2) }} /
              {{ metrics.cpu.load15.toFixed(2) }}
            </NText>
          </NCard>
        </NGi>

        <NGi span="4 m:2 l:1">
          <NCard size="small" title="内存">
            <NProgress
              type="dashboard"
              :percentage="Math.round(metrics.memory.used_percent * 10) / 10"
              :status="usageStatus(metrics.memory.used_percent)"
            />
            <NText depth="3" style="display: block; margin-top: 8px; font-size: 12px">
              {{ fmtBytes(metrics.memory.used) }} / {{ fmtBytes(metrics.memory.total) }}
            </NText>
            <NText v-if="metrics.memory.swap_total > 0" depth="3" style="font-size: 12px">
              交换区 {{ fmtBytes(metrics.memory.swap_used) }} / {{ fmtBytes(metrics.memory.swap_total) }}
            </NText>
          </NCard>
        </NGi>

        <NGi span="4 m:4 l:2">
          <NCard size="small" title="主机">
            <NGrid :cols="2" :y-gap="10">
              <NGi><NStatistic label="运行时长" :value="fmtUptime(metrics.host.uptime_seconds)" /></NGi>
              <NGi><NStatistic label="进程数" :value="metrics.host.procs" /></NGi>
              <NGi><NStatistic label="操作系统" :value="metrics.host.os" /></NGi>
              <NGi><NStatistic label="Go 版本" :value="metrics.host.go_version" /></NGi>
            </NGrid>
          </NCard>
        </NGi>
      </NGrid>

      <NCard size="small" title="每核使用率" style="margin-top: 12px"
             v-if="metrics.cpu.per_core && metrics.cpu.per_core.length > 1">
        <NSpace vertical size="small">
          <div v-for="(v, i) in metrics.cpu.per_core" :key="i" style="display: flex; align-items: center; gap: 8px">
            <NTag size="small" style="width: 56px; justify-content: center">#{{ i }}</NTag>
            <NProgress
              type="line"
              :percentage="Math.round(v * 10) / 10"
              :status="usageStatus(v)"
              :height="10"
              style="flex: 1"
            />
          </div>
        </NSpace>
      </NCard>

      <NCard size="small" title="磁盘" style="margin-top: 12px">
        <NDataTable :columns="diskColumns" :data="metrics.disks" :bordered="false" size="small" />
      </NCard>
    </template>
  </div>
</template>

<style scoped>
.monitor-system {
  padding: 16px;
}
</style>
