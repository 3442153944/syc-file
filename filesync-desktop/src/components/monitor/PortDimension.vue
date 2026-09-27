<script setup lang="ts">
// 端口分布维度：概览 = 当前监听端口表 + 总连接数趋势（简略模式，1 分钟一个点，
// 其余 WS 帧直接丢弃，最多留 1 天）。复用父组件已有的监控 WS 推送（见
// useMonitor 的 listeningPorts/portConnections 字段），不走 HTTP 轮询——直接用
// 已经开着的连接，没有额外的连接建立开销。点某个端口才按端口号拉完整历史。
// 切到别的维度这个组件被 v-if 卸载，状态跟着释放。
import { ref, computed, onMounted, watch } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { NCard, NDataTable, NModal, NSpin, NEmpty, NText, type DataTableColumns } from 'naive-ui'
import { fetchPortHistory, type ListeningPort, type PortConnCount, type PortFrame } from '@/api/monitor/useMonitor'
import { throttledAppend, createThrottleCursor, type Timestamped } from '@/utils/liveSeries'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent])

const props = defineProps<{
  liveListening?: ListeningPort[] | null
  liveConnections?: PortConnCount[] | null
}>()

const OVERVIEW_DAYS = 1
const DETAIL_DAYS = 365

interface TrendPoint extends Timestamped {
  totalConnections: number
}

const listening = ref<ListeningPort[]>([])
const trend = ref<TrendPoint[]>([])
const loading = ref(false)
const cursor = createThrottleCursor()

function sumConn(list: PortConnCount[]): number {
  return list.reduce((s, p) => s + p.connections, 0)
}

// 同一端口可能同时有 tcp/udp 两条记录，表里按 端口+协议 去重展示
function dedupPorts(list: ListeningPort[]): ListeningPort[] {
  const seen = new Set<string>()
  const out: ListeningPort[] = []
  for (const p of list) {
    const key = `${p.port}/${p.protocol}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(p)
  }
  return out.sort((a, b) => a.port - b.port)
}

// 首次加载：拉一天的历史把趋势图垫上底，不用干等第一帧 WS 推送
async function loadOverview() {
  loading.value = true
  try {
    const frames = await fetchPortHistory(OVERVIEW_DAYS)
    if (frames.length) {
      const last = frames[frames.length - 1]
      listening.value = dedupPorts(last.listening_ports)
      trend.value = frames.map((f) => ({ t: f.t, totalConnections: sumConn(f.port_connections) }))
    }
  } catch {
    // 首次加载失败就空着
  } finally {
    loading.value = false
  }
}

onMounted(loadOverview)

// 实时增量：WS 一来新帧，表格刷新成当前状态，趋势图按简略模式节流追加
watch(
  () => [props.liveListening, props.liveConnections] as const,
  ([listeningNow, connsNow]) => {
    if (listeningNow) listening.value = dedupPorts(listeningNow)
    if (connsNow) {
      trend.value = throttledAppend(
        trend.value,
        { t: Math.floor(Date.now() / 1000), totalConnections: sumConn(connsNow) },
        OVERVIEW_DAYS,
        cursor,
      )
    }
  },
)

const detailPort = ref<number | null>(null)
const detailFrames = ref<PortFrame[]>([])
const detailLoading = ref(false)

async function openDetail(port: number) {
  detailPort.value = port
  detailLoading.value = true
  detailFrames.value = []
  try {
    detailFrames.value = await fetchPortHistory(DETAIL_DAYS, port)
  } catch {
    detailFrames.value = []
  } finally {
    detailLoading.value = false
  }
}

function closeDetail() {
  detailPort.value = null
  detailFrames.value = []
}

const overviewOption = computed(() => ({
  backgroundColor: 'transparent',
  textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
  grid: { left: 48, right: 16, top: 16, bottom: 28 },
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
    axisLabel: { color: '#898781', fontSize: 11 },
    splitLine: { lineStyle: { color: '#e1e0d9' } },
  },
  series: [
    {
      name: '总连接数',
      type: 'line',
      data: trend.value.map((p) => [p.t * 1000, p.totalConnections]),
      showSymbol: false,
      lineStyle: { width: 2, color: '#1baf7a' },
      itemStyle: { color: '#1baf7a' },
      areaStyle: { color: '#1baf7a', opacity: 0.1 },
    },
  ],
}))

const detailOption = computed(() => {
  const points = detailFrames.value.flatMap((f) => f.port_connections.map((pc) => ({ t: f.t, ...pc })))
  return {
    backgroundColor: 'transparent',
    textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
    grid: { left: 48, right: 16, top: 16, bottom: 28 },
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
      axisLabel: { color: '#898781', fontSize: 11 },
      splitLine: { lineStyle: { color: '#e1e0d9' } },
    },
    series: [
      {
        name: `端口 ${detailPort.value} 连接数`,
        type: 'line',
        data: points.map((p) => [p.t * 1000, p.connections]),
        showSymbol: false,
        lineStyle: { width: 2, color: '#1baf7a' },
        itemStyle: { color: '#1baf7a' },
      },
    ],
  }
})

const columns: DataTableColumns<ListeningPort> = [
  { title: '端口', key: 'port' },
  { title: '协议', key: 'protocol', render: (row) => row.protocol.toUpperCase() },
  { title: '进程', key: 'process_name' },
  { title: 'PID', key: 'pid' },
]

function rowProps(row: ListeningPort) {
  return { style: 'cursor: pointer', onClick: () => openDetail(row.port) }
}
</script>

<template>
  <div>
    <NCard size="small" title="端口总连接数趋势" :segmented="{ content: true }" style="margin-bottom: 12px">
      <NSpin :show="loading">
        <VChart v-if="trend.length" :option="overviewOption" autoresize style="height: 180px" />
        <NEmpty v-else description="暂无数据" size="small" style="padding: 24px 0" />
      </NSpin>
    </NCard>
    <NCard size="small" title="当前监听端口" :segmented="{ content: true }">
      <template #header-extra>
        <NText depth="3" style="font-size: 12px">点端口行看连接数历史</NText>
      </template>
      <NDataTable :columns="columns" :data="listening" :row-props="rowProps" size="small" :bordered="false" />
    </NCard>

    <NModal
      :show="!!detailPort"
      preset="card"
      :title="`端口详情：${detailPort}`"
      style="width: 720px"
      @update:show="(v: boolean) => !v && closeDetail()"
    >
      <NSpin :show="detailLoading">
        <VChart v-if="detailFrames.length" :option="detailOption" autoresize style="height: 300px" />
        <NEmpty v-else description="暂无该端口的历史数据" size="small" style="padding: 40px 0" />
      </NSpin>
    </NModal>
  </div>
</template>
