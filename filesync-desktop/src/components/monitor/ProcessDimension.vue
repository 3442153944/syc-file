<script setup lang="ts">
// 进程维度：概览 = 当前 Top-N 进程表 + Top-N 总 CPU 占用趋势（简略模式，1 分钟
// 一个点，其余通过 WS 收到的帧直接丢弃，最多留 1 天，见 utils/liveSeries）。
// 进程数据复用父组件已有的监控 WS 推送（见 useMonitor 的 processes 字段），
// 不走 HTTP 轮询——HTTP 有额外的连接建立开销，直接用已经开着的 WS 没有这份
// 开销。点某一行才按进程名拉完整历史——PID 在进程重启后会变，跨时间范围认
// 同一个进程只能按名字。切到别的维度这个组件被 v-if 卸载，这里的状态跟着
// 释放，不会常驻内存。
import { ref, computed, onMounted, watch } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { NCard, NDataTable, NModal, NSpin, NEmpty, NText, type DataTableColumns } from 'naive-ui'
import { fetchProcessHistory, fmtBytes, type ProcessInfo, type ProcessFrame } from '@/api/monitor/useMonitor'
import { throttledAppend, createThrottleCursor, type Timestamped } from '@/utils/liveSeries'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent])

const props = defineProps<{ live?: ProcessInfo[] | null }>()

const OVERVIEW_DAYS = 1
const DETAIL_DAYS = 365 // 后端 queryMaxDays 的上限，详情按名字查，不受简略模式的 1 天限制

interface TrendPoint extends Timestamped {
  totalCpu: number
}

const latest = ref<ProcessInfo[]>([])
const trend = ref<TrendPoint[]>([])
const loading = ref(false)
const cursor = createThrottleCursor()

function sumCpu(list: ProcessInfo[]): number {
  return Math.round(list.reduce((s, p) => s + p.cpu_percent, 0) * 10) / 10
}

// 首次加载：拉一天的历史把趋势图垫上底，不用干等第一帧 WS 推送
async function loadOverview() {
  loading.value = true
  try {
    const frames = await fetchProcessHistory(OVERVIEW_DAYS)
    if (frames.length) {
      const last = frames[frames.length - 1]
      latest.value = [...last.processes].sort((a, b) => b.score - a.score)
      trend.value = frames.map((f) => ({ t: f.t, totalCpu: sumCpu(f.processes) }))
    }
  } catch {
    // 首次加载失败就空着，NEmpty 兜底
  } finally {
    loading.value = false
  }
}

onMounted(loadOverview)

// 实时增量：WS 推来新的 Top-N 快照，表格直接刷新成最新（本来就该显示"当前"），
// 趋势图按简略模式节流追加，不是每帧都进
watch(
  () => props.live,
  (procs) => {
    if (!procs) return
    latest.value = [...procs].sort((a, b) => b.score - a.score)
    trend.value = throttledAppend(
      trend.value,
      { t: Math.floor(Date.now() / 1000), totalCpu: sumCpu(procs) },
      OVERVIEW_DAYS,
      cursor,
    )
  },
)

// 详情：点某一行才发请求，弹窗关闭即释放，不留在内存里
const detailName = ref<string | null>(null)
const detailFrames = ref<ProcessFrame[]>([])
const detailLoading = ref(false)

async function openDetail(name: string) {
  detailName.value = name
  detailLoading.value = true
  detailFrames.value = []
  try {
    detailFrames.value = await fetchProcessHistory(DETAIL_DAYS, name)
  } catch {
    detailFrames.value = []
  } finally {
    detailLoading.value = false
  }
}

function closeDetail() {
  detailName.value = null
  detailFrames.value = []
}

const overviewOption = computed(() => ({
  backgroundColor: 'transparent',
  textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
  grid: { left: 44, right: 16, top: 16, bottom: 28 },
  tooltip: {
    trigger: 'axis',
    axisPointer: { type: 'line' },
    valueFormatter: (v: number) => `${Number(v).toFixed(1)}%`,
  },
  xAxis: {
    type: 'time',
    axisLine: { lineStyle: { color: '#c3c2b7' } },
    axisLabel: { color: '#898781', fontSize: 11 },
    axisTick: { show: false },
  },
  yAxis: {
    type: 'value',
    min: 0,
    axisLabel: { formatter: '{value}%', color: '#898781', fontSize: 11 },
    splitLine: { lineStyle: { color: '#e1e0d9' } },
  },
  series: [
    {
      name: 'Top-N 进程 CPU 总和',
      type: 'line',
      data: trend.value.map((p) => [p.t * 1000, p.totalCpu]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#2a78d6' },
      itemStyle: { color: '#2a78d6' },
      areaStyle: { color: '#2a78d6', opacity: 0.1 },
    },
  ],
}))

const detailOption = computed(() => {
  const points = detailFrames.value.flatMap((f) => f.processes.map((p) => ({ t: f.t, ...p })))
  return {
    backgroundColor: 'transparent',
    textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
    grid: { left: 44, right: 16, top: 36, bottom: 28 },
    legend: { top: 0, right: 0, itemWidth: 16, itemHeight: 2, textStyle: { color: '#52514e', fontSize: 12 } },
    tooltip: { trigger: 'axis', axisPointer: { type: 'line' } },
    xAxis: {
      type: 'time',
      axisLine: { lineStyle: { color: '#c3c2b7' } },
      axisLabel: { color: '#898781', fontSize: 11 },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      max: 100,
      axisLabel: { formatter: '{value}%', color: '#898781', fontSize: 11 },
      splitLine: { lineStyle: { color: '#e1e0d9' } },
    },
    series: [
      {
        name: 'CPU %',
        type: 'line',
        data: points.map((p) => [p.t * 1000, Number(p.cpu_percent.toFixed(1))]),
        showSymbol: false,
        lineStyle: { width: 2, color: '#2a78d6' },
        itemStyle: { color: '#2a78d6' },
      },
      {
        name: '内存 %',
        type: 'line',
        data: points.map((p) => [p.t * 1000, Number(p.mem_percent.toFixed(1))]),
        showSymbol: false,
        lineStyle: { width: 2, color: '#eb6834' },
        itemStyle: { color: '#eb6834' },
      },
    ],
  }
})

const columns: DataTableColumns<ProcessInfo> = [
  { title: '进程', key: 'name' },
  { title: 'PID', key: 'pid' },
  { title: 'CPU', key: 'cpu_percent', render: (row) => `${row.cpu_percent.toFixed(1)}%` },
  {
    title: '内存',
    key: 'mem_bytes',
    render: (row) => `${fmtBytes(row.mem_bytes)} (${row.mem_percent.toFixed(1)}%)`,
  },
  {
    title: '磁盘 读/写',
    key: 'disk',
    render: (row) => `${fmtBytes(row.disk_read_bytes)} / ${fmtBytes(row.disk_write_bytes)}`,
  },
  { title: '连接数', key: 'connections' },
]

function rowProps(row: ProcessInfo) {
  return { style: 'cursor: pointer', onClick: () => openDetail(row.name) }
}
</script>

<template>
  <div>
    <NCard size="small" title="Top 进程 CPU 占用趋势" :segmented="{ content: true }" style="margin-bottom: 12px">
      <NSpin :show="loading">
        <VChart v-if="trend.length" :option="overviewOption" autoresize style="height: 180px" />
        <NEmpty v-else description="暂无数据" size="small" style="padding: 24px 0" />
      </NSpin>
    </NCard>
    <NCard size="small" title="当前 Top 进程" :segmented="{ content: true }">
      <template #header-extra>
        <NText depth="3" style="font-size: 12px">点进程行看完整历史</NText>
      </template>
      <NDataTable :columns="columns" :data="latest" :row-props="rowProps" size="small" :bordered="false" />
    </NCard>

    <NModal
      :show="!!detailName"
      preset="card"
      :title="`进程详情：${detailName}`"
      style="width: 720px"
      @update:show="(v: boolean) => !v && closeDetail()"
    >
      <NSpin :show="detailLoading">
        <VChart v-if="detailFrames.length" :option="detailOption" autoresize style="height: 300px" />
        <NEmpty v-else description="暂无该进程的历史数据" size="small" style="padding: 40px 0" />
      </NSpin>
    </NModal>
  </div>
</template>
