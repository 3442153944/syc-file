<script setup lang="ts">
// 网络资源维度：发送/接收速率随时间变化。数据本来就在系统级历史里
//（send_rate/recv_rate，1 分钟一份，见 history.go），只是之前没画出来。
// 有 WS 实时推送（父组件的 useMonitor().network），走和 CPU/内存图一样的
// 节流追加规则：简略模式 1 分钟一个点，最多留 1 天。
import { ref, computed, onMounted, watch } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { NCard, NSpin, NEmpty } from 'naive-ui'
import { fetchMonitorHistory, fmtRate, type HistoryPoint, type NetworkMetrics } from '@/api/monitor/useMonitor'
import { throttledAppend, createThrottleCursor } from '@/utils/liveSeries'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent])

const props = defineProps<{ live?: NetworkMetrics | null }>()

const OVERVIEW_DAYS = 1
const points = ref<HistoryPoint[]>([])
const loading = ref(false)
const cursor = createThrottleCursor()

async function load() {
  loading.value = true
  try {
    points.value = await fetchMonitorHistory(OVERVIEW_DAYS)
  } catch {
    points.value = []
  } finally {
    loading.value = false
  }
}

onMounted(load)

watch(
  () => props.live,
  (n) => {
    if (!n) return
    points.value = throttledAppend(
      points.value,
      {
        t: Math.floor(Date.now() / 1000),
        cpu: 0,
        mem: 0,
        mem_used: 0,
        send_rate: n.send_rate,
        recv_rate: n.recv_rate,
        online_devices: n.online_devices,
        active_connections: n.active_connections,
      },
      OVERVIEW_DAYS,
      cursor,
    )
  },
)

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
      name: '发送',
      type: 'line',
      data: points.value.map((p) => [p.t * 1000, Math.round(p.send_rate)]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#2a78d6' },
      itemStyle: { color: '#2a78d6' },
      areaStyle: { color: '#2a78d6', opacity: 0.1 },
    },
    {
      name: '接收',
      type: 'line',
      data: points.value.map((p) => [p.t * 1000, Math.round(p.recv_rate)]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#eb6834' },
      itemStyle: { color: '#eb6834' },
      areaStyle: { color: '#eb6834', opacity: 0.1 },
    },
  ],
}))
</script>

<template>
  <NCard size="small" title="网络吞吐趋势" :segmented="{ content: true }">
    <NSpin :show="loading">
      <VChart v-if="points.length" :option="option" autoresize style="height: 260px" />
      <NEmpty v-else description="暂无历史数据" size="small" style="padding: 40px 0" />
    </NSpin>
  </NCard>
</template>
