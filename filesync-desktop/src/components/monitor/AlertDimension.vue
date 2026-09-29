<script setup lang="ts">
// 资源告警历史：某进程 CPU 连续多次越过阈值时后端记的一条记录（见 new_server
// 的 internal/monitor/resource_alert.go）。这里只是历史列表 + 详情查看，实时提醒
// 走 ServerNotificationListener.vue（WS 推送 → naive-ui 通知），两者独立，不复用
// 同一份数据——历史列表是主动拉取的，通知是被动推送的，混在一起没必要。
import { ref, onMounted, computed, h } from 'vue'
import {
  NCard, NDataTable, NModal, NSpin, NEmpty, NTag, NButton, NRadioGroup, NRadioButton, NText,
  type DataTableColumns,
} from 'naive-ui'
import { fetchResourceAlerts, fmtBytes, type ResourceAlert, type ProcessInfo } from '@/api/monitor/useMonitor'

type DaysPreset = 1 | 7 | 30
const DAYS_OPTIONS: { value: DaysPreset; label: string }[] = [
  { value: 1, label: '最近 1 天' },
  { value: 7, label: '最近 7 天' },
  { value: 30, label: '最近 30 天' },
]

const days = ref<DaysPreset>(7)
const alerts = ref<ResourceAlert[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    alerts.value = await fetchResourceAlerts(days.value)
  } catch {
    alerts.value = []
  } finally {
    loading.value = false
  }
}

function applyDays(v: DaysPreset) {
  days.value = v
  load()
}

onMounted(load)

const activeCount = computed(() => alerts.value.filter((a) => a.status === 'active').length)

function fmtTime(unixSec: number): string {
  return unixSec ? new Date(unixSec * 1000).toLocaleString() : '—'
}

const columns: DataTableColumns<ResourceAlert> = [
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) =>
      row.status === 'active'
        ? h(NTag, { size: 'small', type: 'error' }, { default: () => '进行中' })
        : h(NTag, { size: 'small', type: 'default' }, { default: () => '已恢复' }),
  },
  { title: '触发时间', key: 'triggered_at', render: (row) => fmtTime(row.triggered_at) },
  { title: '触发核心', key: 'trigger_core', render: (row) => `核心 #${row.trigger_core}` },
  {
    title: '该核心占用',
    key: 'trigger_cpu_percent',
    render: (row) => `${row.trigger_cpu_percent.toFixed(1)}%`,
  },
  { title: '恢复时间', key: 'resolved_at', render: (row) => fmtTime(row.resolved_at) },
]

function rowProps(row: ResourceAlert) {
  return { style: 'cursor: pointer', onClick: () => (detail.value = row) }
}

const detail = ref<ResourceAlert | null>(null)

const detailColumns: DataTableColumns<ProcessInfo> = [
  { title: '进程', key: 'name' },
  { title: 'PID', key: 'pid' },
  { title: 'CPU', key: 'cpu_percent', render: (row) => `${row.cpu_percent.toFixed(1)}%` },
  {
    title: '内存',
    key: 'mem_bytes',
    render: (row) => `${fmtBytes(row.mem_bytes)} (${row.mem_percent.toFixed(1)}%)`,
  },
  { title: '连接数', key: 'connections' },
]
</script>

<template>
  <div>
    <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px">
      <NRadioGroup :value="days" size="small" @update:value="(v) => applyDays(v as DaysPreset)">
        <NRadioButton v-for="opt in DAYS_OPTIONS" :key="opt.value" :value="opt.value">
          {{ opt.label }}
        </NRadioButton>
      </NRadioGroup>
      <NText v-if="activeCount > 0" type="error" depth="1" style="font-size: 12px">
        {{ activeCount }} 条告警进行中
      </NText>
    </div>

    <NCard size="small" title="资源告警历史" :segmented="{ content: true }">
      <template #header-extra>
        <NButton size="tiny" :loading="loading" @click="load">刷新</NButton>
      </template>
      <NSpin :show="loading">
        <NDataTable
          v-if="alerts.length"
          :columns="columns"
          :data="alerts"
          :row-props="rowProps"
          size="small"
          :bordered="false"
        />
        <NEmpty v-else description="该时间段内没有告警" size="small" style="padding: 24px 0" />
      </NSpin>
    </NCard>

    <NModal
      :show="!!detail"
      preset="card"
      :title="detail ? `告警详情 · ${fmtTime(detail.triggered_at)}` : ''"
      style="width: 720px"
      @update:show="(v: boolean) => !v && (detail = null)"
    >
      <template v-if="detail">
        <NText depth="3" style="font-size: 12px; display: block; margin-bottom: 8px">
          触发核心：#{{ detail.trigger_core }}
          · {{ detail.trigger_cpu_percent.toFixed(1) }}%
          · {{ detail.status === 'active' ? '仍在进行中' : `已于 ${fmtTime(detail.resolved_at)} 恢复` }}
        </NText>
        <NText depth="3" style="font-size: 12px; display: block; margin-bottom: 8px">
          下表是触发那一刻的进程快照，供排查参考——告警本身是按单核占用触发的，不是某一个进程超阈值。
        </NText>
        <NDataTable :columns="detailColumns" :data="detail.top_processes" size="small" :bordered="false" />
      </template>
    </NModal>
  </div>
</template>
