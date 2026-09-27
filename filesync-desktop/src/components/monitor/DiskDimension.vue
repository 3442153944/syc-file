<script setup lang="ts">
// 硬盘资源维度：磁盘"占用了多少空间"在概览区的磁盘卡片已经有了（静态快照，
// 见 System.vue 顶部），这里画的是磁盘"活跃度"——Top-N 进程的读/写字节数总和
// 随时间变化，数据来自进程采集（disk_read_bytes/disk_write_bytes，见
// sys_detail.go），没有单独的磁盘 IO 采集，复用同一份进程明细（父组件的
// useMonitor().processes，走已有的监控 WS 推送，不额外轮询）。
// 简略模式：1 分钟一个点，其余 WS 帧丢弃，最多留 1 天。
import { ref, computed, onMounted, watch } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { NCard, NSpin, NEmpty } from 'naive-ui'
import { fetchProcessHistory, fmtRate, type ProcessInfo } from '@/api/monitor/useMonitor'
import { throttledAppend, createThrottleCursor, type Timestamped } from '@/utils/liveSeries'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent])

const props = defineProps<{ live?: ProcessInfo[] | null }>()

const OVERVIEW_DAYS = 1

interface TrendPoint extends Timestamped {
  readBytes: number
  writeBytes: number
}

const trend = ref<TrendPoint[]>([])
const loading = ref(false)
const cursor = createThrottleCursor()

function sumDisk(list: ProcessInfo[]): { r: number; w: number } {
  return list.reduce(
    (acc, p) => ({ r: acc.r + p.disk_read_bytes, w: acc.w + p.disk_write_bytes }),
    { r: 0, w: 0 },
  )
}

// 首次加载：拉一天的历史把趋势图垫上底，不用干等第一帧 WS 推送
async function loadOverview() {
  loading.value = true
  try {
    const frames = await fetchProcessHistory(OVERVIEW_DAYS)
    trend.value = frames.map((f) => {
      const { r, w } = sumDisk(f.processes)
      return { t: f.t, readBytes: r, writeBytes: w }
    })
  } catch {
    // 首次加载失败就空着
  } finally {
    loading.value = false
  }
}

onMounted(loadOverview)

watch(
  () => props.live,
  (procs) => {
    if (!procs) return
    const { r, w } = sumDisk(procs)
    trend.value = throttledAppend(
      trend.value,
      { t: Math.floor(Date.now() / 1000), readBytes: r, writeBytes: w },
      OVERVIEW_DAYS,
      cursor,
    )
  },
)

// 磁盘 IO 是"采集间隔内的增量字节数"，不是速率，但换算成 /s 更符合直觉
// （sys_detail_interval_seconds 默认 30，可配，这里按最近两帧的实际间隔算）
function toRate(bytes: number, index: number): number {
  const prev = trend.value[index - 1]
  if (!prev) return 0
  const dt = trend.value[index].t - prev.t
  return dt > 0 ? bytes / dt : 0
}

const option = computed(() => ({
  backgroundColor: 'transparent',
  textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
  grid: { left: 56, right: 16, top: 36, bottom: 28 },
  legend: {
    top: 0,
    right: 0,
    itemWidth: 16,
    itemHeight: 2,
    textStyle: { color: '#52514e', fontSize: 12 },
  },
  tooltip: {
    trigger: 'axis',
    axisPointer: { type: 'line' },
    valueFormatter: (v: number) => fmtRate(Number(v)),
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
    axisLabel: { formatter: (v: number) => fmtRate(v), color: '#898781', fontSize: 11 },
    splitLine: { lineStyle: { color: '#e1e0d9' } },
  },
  series: [
    {
      name: '读取',
      type: 'line',
      data: trend.value.map((p, i) => [p.t * 1000, Math.round(toRate(p.readBytes, i))]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#2a78d6' },
      itemStyle: { color: '#2a78d6' },
      areaStyle: { color: '#2a78d6', opacity: 0.1 },
    },
    {
      name: '写入',
      type: 'line',
      data: trend.value.map((p, i) => [p.t * 1000, Math.round(toRate(p.writeBytes, i))]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#eb6834' },
      itemStyle: { color: '#eb6834' },
      areaStyle: { color: '#eb6834', opacity: 0.1 },
    },
  ],
}))
</script>

<template>
  <NCard size="small" title="Top 进程磁盘读写趋势" :segmented="{ content: true }">
    <template #header-extra>
      <span style="font-size: 12px; color: var(--n-text-color-3)">磁盘空间占用见页面顶部「磁盘」卡片</span>
    </template>
    <NSpin :show="loading">
      <VChart v-if="trend.length" :option="option" autoresize style="height: 260px" />
      <NEmpty v-else description="暂无历史数据" size="small" style="padding: 40px 0" />
    </NSpin>
  </NCard>
</template>
