<script setup lang="ts">
// 系统状态历史趋势：CPU / 内存占用率随时间变化。历史部分一次性拉取，之后不额外
// 轮询——直接复用父组件已经建好的 WS 监控推送（useMonitor）做增量追加，跟
// Dashboard 卡片缩略图是同一份数据源，图表就跟着实时涨。
import { ref, computed, onMounted, watch } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { NCard, NButtonGroup, NButton, NEmpty, NSpin } from 'naive-ui'
import { fetchMonitorHistory, type HistoryPoint, type SystemMetrics } from '@/api/monitor/useMonitor'
import { throttledAppend, createThrottleCursor } from '@/utils/liveSeries'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent])

// 父组件传自己已有的 useMonitor() 的 system ref——不在这里另开一条 WS 订阅
// （同一个连接的 subscribe/unsubscribe 是全局状态，嵌套组件各开一份容易互相踩）。
const props = defineProps<{ live?: SystemMetrics | null }>()

// 7 天内直接查 Redis，更早的由服务端从 MySQL 长期归档表补——见 new_server
// internal/monitor/history.go，对前端透明，这里只是多给几个更长的预设。
const ranges = [
  { label: '1 天', days: 1 },
  { label: '3 天', days: 3 },
  { label: '7 天', days: 7 },
  { label: '30 天', days: 30 },
]
const selectedDays = ref(1)
const points = ref<HistoryPoint[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    points.value = await fetchMonitorHistory(selectedDays.value)
  } catch {
    points.value = []
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch(selectedDays, load)

// 实时增量：WS 一推新帧不代表就追一个点——简略模式按 1 分钟一个点节流
//（throttledAppend），丢弃更密的推送，超过选中天数的旧点从队首裁掉。这样长时间
// 挂着页面也不会让点数无限膨胀，分辨率也和历史记录（本来就是 1 分钟一份）对得上，
// x 轴用真正的时间轴而不是等距 category 轴，避免"3 秒"和"1 分钟"被画成一样宽。
const liveCursor = createThrottleCursor()
watch(
  () => props.live,
  (s) => {
    if (!s) return
    points.value = throttledAppend(
      points.value,
      {
        t: Math.floor(Date.now() / 1000),
        cpu: s.cpu.used_percent,
        mem: s.memory.used_percent,
        mem_used: s.memory.used,
        send_rate: 0,
        recv_rate: 0,
        online_devices: 0,
        active_connections: 0,
      },
      selectedDays.value,
      liveCursor,
    )
  },
)

// 配色取自数据可视化规范的默认分类色板（1=blue/2=orange），已跑过 CVD 校验器全部通过
const option = computed(() => ({
  backgroundColor: 'transparent',
  textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
  grid: { left: 40, right: 16, top: 36, bottom: 28 },
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
    max: 100,
    axisLabel: { formatter: '{value}%', color: '#898781', fontSize: 11 },
    splitLine: { lineStyle: { color: '#e1e0d9', type: 'solid' } },
  },
  series: [
    {
      name: 'CPU',
      type: 'line',
      data: points.value.map((p) => [p.t * 1000, Number(p.cpu.toFixed(1))]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#2a78d6' },
      itemStyle: { color: '#2a78d6' },
      areaStyle: { color: '#2a78d6', opacity: 0.1 },
    },
    {
      name: '内存',
      type: 'line',
      data: points.value.map((p) => [p.t * 1000, Number(p.mem.toFixed(1))]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#eb6834' },
      itemStyle: { color: '#eb6834' },
      areaStyle: { color: '#eb6834', opacity: 0.1 },
    },
  ],
}))
</script>

<template>
  <NCard size="small" title="系统状态趋势" :segmented="{ content: true }">
    <template #header-extra>
      <NButtonGroup size="small">
        <NButton
          v-for="r in ranges"
          :key="r.days"
          :type="selectedDays === r.days ? 'primary' : 'default'"
          @click="selectedDays = r.days"
        >
          {{ r.label }}
        </NButton>
      </NButtonGroup>
    </template>
    <NSpin :show="loading">
      <VChart v-if="points.length" :option="option" autoresize style="height: 260px" />
      <NEmpty v-else description="暂无历史数据" size="small" style="padding: 40px 0" />
    </NSpin>
  </NCard>
</template>
