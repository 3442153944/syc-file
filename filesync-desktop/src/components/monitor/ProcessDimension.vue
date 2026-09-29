<script setup lang="ts">
// 进程维度：概览 = 当前 Top-N 进程表 + 按进程拆开的 CPU/内存堆叠趋势图（简略模式，1 分钟
// 一个点，其余通过 WS 收到的帧直接丢弃，最多留 1 天，见 utils/liveSeries）。
// 进程数据复用父组件已有的监控 WS 推送（见 useMonitor 的 processes 字段），
// 不走 HTTP 轮询——HTTP 有额外的连接建立开销，直接用已经开着的 WS 没有这份
// 开销。点某一行才按进程名拉完整历史——PID 在进程重启后会变，跨时间范围认
// 同一个进程只能按名字。切到别的维度这个组件被 v-if 卸载，这里的状态跟着
// 释放，不会常驻内存。
//
// 堆叠图按进程拆开是为了回答"这次峰值是哪个进程造成的"：以前只画 Top-N 总和一条线，
// 峰值稳定出现但看不出元凶，得点进单个进程一个个翻。点堆叠图上任意时间点，下方
// 表格会切成那一刻的 Top-N 快照（数据本来就存着，只是以前没有入口展示）。
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { invoke, isTauri } from '@tauri-apps/api/core'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import {
  NCard, NDataTable, NModal, NSpin, NEmpty, NText, NButton, NTag, NRadioGroup, NRadioButton,
  type DataTableColumns,
} from 'naive-ui'
import { fetchProcessHistory, fmtBytes, type ProcessInfo, type ProcessFrame } from '@/api/monitor/useMonitor'
import { throttledAppend, createThrottleCursor, type Timestamped } from '@/utils/liveSeries'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent, LegendComponent])

const props = defineProps<{ live?: ProcessInfo[] | null }>()

const OVERVIEW_DAYS = 1
const DETAIL_DAYS = 365 // 后端 queryMaxDays 的上限，详情按名字查，不受简略模式的 1 天限制
/** 堆叠图固定画几条进程线，其余合并进"其他"——全画开图例会挤爆，也没法看 */
const TOP_K = 6
const OTHER_NAME = '其他'

// ── 时间区间预设 ──────────────────────────────────────────────────────────────
// 5/15/30 分钟这三档窗口太短，30s 一个点的默认采集根本看不出细节（甚至可能一个点
// 都没几个），需要后端临时把 sys_detail 的采集频率顶到秒级（见 set_process_detail_boost
// / new_server 的 SetDetailBoost）。1 小时以上维持默认间隔——图不需要那么密，
// 秒级数据量也扛不住拉那么长。
type RangePreset = '5m' | '15m' | '30m' | '1h' | '6h' | '24h'
const RANGE_OPTIONS: { value: RangePreset; label: string }[] = [
  { value: '5m', label: '5 分钟' },
  { value: '15m', label: '15 分钟' },
  { value: '30m', label: '30 分钟' },
  { value: '1h', label: '1 小时' },
  { value: '6h', label: '6 小时' },
  { value: '24h', label: '24 小时' },
]
const RANGE_SECONDS: Record<RangePreset, number> = {
  '5m': 300, '15m': 900, '30m': 1800, '1h': 3600, '6h': 21600, '24h': 86400,
}
const BOOST_PRESETS = new Set<RangePreset>(['5m', '15m', '30m'])
/** 秒级模式下多久重新拉一次历史——用轮询而不是节流追加，见 refreshBoostedTrend 的注释 */
const BOOST_POLL_MS = 3000

const rangePreset = ref<RangePreset>('24h')
let boostPollTimer: ReturnType<typeof setInterval> | null = null

interface TrendPoint extends Timestamped {
  processes: ProcessInfo[]
}

const latest = ref<ProcessInfo[]>([])
const trend = ref<TrendPoint[]>([])
const loading = ref(false)
const cursor = createThrottleCursor()

/** 点堆叠图选中的历史时刻；null 表示表格显示"当前"。 */
const snapshot = ref<TrendPoint | null>(null)
const displayedProcesses = computed(() => snapshot.value?.processes ?? latest.value)
const snapshotTimeText = computed(() =>
  snapshot.value ? new Date(snapshot.value.t * 1000).toLocaleString() : '',
)

/** 后端本轮归一化用的逻辑核数，诊断用：和任务管理器报的核数对不上就是有 bug。 */
const numCpus = ref<number | null>(null)

// 首次加载：拉一天的历史把趋势图垫上底，不用干等第一帧 WS 推送
async function loadOverview() {
  loading.value = true
  try {
    const frames = await fetchProcessHistory(OVERVIEW_DAYS)
    if (frames.length) {
      const last = frames[frames.length - 1]
      latest.value = [...last.processes].sort((a, b) => b.score - a.score)
      trend.value = frames.map((f) => ({ t: f.t, processes: f.processes }))
      numCpus.value = last.num_cpus ?? null
    }
  } catch {
    // 首次加载失败就空着，NEmpty 兜底
  } finally {
    loading.value = false
  }
}

onMounted(loadOverview)

// 实时增量：WS 推来新的 Top-N 快照，表格直接刷新成最新（本来就该显示"当前"），
// 趋势图按简略模式节流追加，不是每帧都进——秒级模式下这条节流追加太粗（1 分钟
// 才收一个点），交给 refreshBoostedTrend 的轮询接管，这里只跳过 trend 不动它。
watch(
  () => props.live,
  (procs) => {
    if (!procs) return
    latest.value = [...procs].sort((a, b) => b.score - a.score)
    if (boostPollTimer) return
    trend.value = throttledAppend(
      trend.value,
      { t: Math.floor(Date.now() / 1000), processes: procs },
      OVERVIEW_DAYS,
      cursor,
    )
  },
)

/** 当前预设窗口对应的秒数；只用来裁剪已加载的 trend，不影响拉取多少天历史。 */
const visibleTrend = computed(() => {
  const rangeSec = RANGE_SECONDS[rangePreset.value]
  if (rangeSec >= 86400) return trend.value
  const cutoff = Math.floor(Date.now() / 1000) - rangeSec
  return trend.value.filter((p) => p.t >= cutoff)
})

/**
 * 秒级模式专用刷新：整份重拉最近一天的历史再整体替换 trend。不能沿用节流追加
 * 那一套——那是为"1 分钟一个点、留一整天"的简略模式设计的，秒级视图需要的是
 * "尽量贴近服务端刚采到的东西"，轮询重拉比等节流窗口攒够时间划算，数据量也
 * 就近几十分钟的秒级数据，重拉开销不大。
 */
async function refreshBoostedTrend() {
  try {
    const frames = await fetchProcessHistory(OVERVIEW_DAYS)
    if (!frames.length) return
    trend.value = frames.map((f) => ({ t: f.t, processes: f.processes }))
    const last = frames[frames.length - 1]
    latest.value = [...last.processes].sort((a, b) => b.score - a.score)
    numCpus.value = last.num_cpus ?? numCpus.value
  } catch {
    // 保留上一轮的数据，下一次轮询再试
  }
}

/** 切换时间区间预设：短窗口要求后端把采集顶到秒级，并本地轮询拉新数据顶替节流追加。 */
function applyRangePreset(preset: RangePreset) {
  rangePreset.value = preset
  const boosted = BOOST_PRESETS.has(preset)

  if (boostPollTimer) {
    clearInterval(boostPollTimer)
    boostPollTimer = null
  }
  if (isTauri()) {
    invoke('set_process_detail_boost', { on: boosted }).catch(() => {
      // 大概率是还没订阅监控（页面刚打开、WS 还没连上），秒级请求先丢了，
      // 不影响本地照常轮询/裁剪已有数据
    })
  }
  if (boosted) {
    refreshBoostedTrend()
    boostPollTimer = setInterval(refreshBoostedTrend, BOOST_POLL_MS)
  }
}

onUnmounted(() => {
  if (boostPollTimer) clearInterval(boostPollTimer)
  if (isTauri()) invoke('set_process_detail_boost', { on: false }).catch(() => {})
})

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

/**
 * 把逐帧的 Top-N 进程列表拆成"按进程名分组的堆叠序列"：
 * 1. 每帧内先按名字合并同名多进程（比如好几个 chrome.exe 子进程），更符合"这个
 *    应用占多少"的直觉；
 * 2. 取整个窗口内出现过的最大合并值排前 TOP_K 的名字做固定序列，图例/配色稳定；
 * 3. 其余名字每帧求和归进"其他"；
 * 4. 某进程在某帧的 Top-N 里没出现就记 0——数据源本来就只存了每帧的 Top-N，
 *    不在其中说明那一刻它占用小到没挤进前列，按 0 处理是合理近似。
 */
function buildStackedSeries(points: TrendPoint[], metric: 'cpu_percent' | 'mem_percent'): any[] {
  const perFrame = points.map((p) => {
    const byName = new Map<string, number>()
    for (const proc of p.processes) {
      byName.set(proc.name, (byName.get(proc.name) ?? 0) + proc[metric])
    }
    return { t: p.t, byName }
  })

  const totals = new Map<string, number>()
  for (const f of perFrame) {
    for (const [name, v] of f.byName) {
      totals.set(name, Math.max(totals.get(name) ?? 0, v))
    }
  }
  const topNames = [...totals.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, TOP_K)
    .map(([name]) => name)
  const topSet = new Set(topNames)

  const series: any[] = topNames.map((name) => ({
    name,
    type: 'line',
    stack: 'total',
    areaStyle: {},
    showSymbol: false,
    lineStyle: { width: 1 },
    data: perFrame.map((f) => [f.t * 1000, Number((f.byName.get(name) ?? 0).toFixed(1))]),
  }))

  const otherData = perFrame.map((f) => {
    let sum = 0
    for (const [name, v] of f.byName) {
      if (!topSet.has(name)) sum += v
    }
    return [f.t * 1000, Number(sum.toFixed(1))]
  })
  if (otherData.some(([, v]) => (v as number) > 0)) {
    series.push({
      name: OTHER_NAME,
      type: 'line',
      stack: 'total',
      areaStyle: {},
      showSymbol: false,
      lineStyle: { width: 1 },
      itemStyle: { color: '#b8b6ad' },
      color: '#b8b6ad',
      data: otherData,
    })
  }
  return series
}

/**
 * 内存版堆叠序列：光看百分比不直观（"3.2%"没人知道是多少 MB），每个数据点在携带
 * 堆叠用的百分比之外，另存一份合并后的字节数（同名进程一并加总），tooltip 里两个
 * 一起显示。Y 轴仍按百分比堆叠——字节数量纲差异太大（几十 KB 到几 GB 都有），
 * 堆成面积图没法看，百分比才有统一的 0~100 刻度。
 */
function buildMemStackedSeries(points: TrendPoint[]): any[] {
  const perFrame = points.map((p) => {
    const byName = new Map<string, { percent: number; bytes: number }>()
    for (const proc of p.processes) {
      const cur = byName.get(proc.name) ?? { percent: 0, bytes: 0 }
      cur.percent += proc.mem_percent
      cur.bytes += proc.mem_bytes
      byName.set(proc.name, cur)
    }
    return { t: p.t, byName }
  })

  const totals = new Map<string, number>()
  for (const f of perFrame) {
    for (const [name, v] of f.byName) {
      totals.set(name, Math.max(totals.get(name) ?? 0, v.percent))
    }
  }
  const topNames = [...totals.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, TOP_K)
    .map(([name]) => name)
  const topSet = new Set(topNames)

  const point = (f: (typeof perFrame)[number], name: string) => {
    const v = f.byName.get(name)
    return { value: [f.t * 1000, Number((v?.percent ?? 0).toFixed(1))], bytes: v?.bytes ?? 0 }
  }

  const series: any[] = topNames.map((name) => ({
    name,
    type: 'line',
    stack: 'total',
    areaStyle: {},
    showSymbol: false,
    lineStyle: { width: 1 },
    data: perFrame.map((f) => point(f, name)),
  }))

  const otherData = perFrame.map((f) => {
    let percent = 0
    let bytes = 0
    for (const [name, v] of f.byName) {
      if (!topSet.has(name)) {
        percent += v.percent
        bytes += v.bytes
      }
    }
    return { value: [f.t * 1000, Number(percent.toFixed(1))], bytes }
  })
  if (otherData.some((d) => (d.value[1] as number) > 0)) {
    series.push({
      name: OTHER_NAME,
      type: 'line',
      stack: 'total',
      areaStyle: {},
      showSymbol: false,
      lineStyle: { width: 1 },
      itemStyle: { color: '#b8b6ad' },
      color: '#b8b6ad',
      data: otherData,
    })
  }
  return series
}

const cpuStackedSeries = computed(() => buildStackedSeries(visibleTrend.value, 'cpu_percent'))
const memStackedSeries = computed(() => buildMemStackedSeries(visibleTrend.value))

function stackedOption(series: any[], tooltip: Record<string, unknown>) {
  return {
    backgroundColor: 'transparent',
    textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
    grid: { left: 44, right: 16, top: 28, bottom: 28 },
    legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 2, textStyle: { color: '#52514e', fontSize: 11 } },
    // appendToBody：tooltip 默认挂在图表容器内，NCard 内容区有 overflow 裁剪时
    // 悬浮层刚探出卡片边界就被切掉。挂到 body 上去，不受任何祖先的 overflow/层叠上下文影响。
    tooltip: { trigger: 'axis', axisPointer: { type: 'line' }, appendToBody: true, ...tooltip },
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
    series,
  }
}

const cpuOverviewOption = computed(() =>
  stackedOption(cpuStackedSeries.value, { valueFormatter: (v: number) => `${Number(v).toFixed(1)}%` }),
)
const memOverviewOption = computed(() =>
  stackedOption(memStackedSeries.value, {
    // 走自定义 formatter 而不是 valueFormatter：需要从 params.data.bytes 里取百分比之外
    // 另存的字节数，valueFormatter 只拿得到堆叠值本身。
    formatter: (params: any[]) => {
      if (!params.length) return ''
      const time = new Date(params[0].value[0]).toLocaleString()
      const lines = params.map(
        (p) => `${p.marker} ${p.seriesName}：${Number(p.value[1]).toFixed(1)}% · ${fmtBytes(p.data?.bytes ?? 0)}`,
      )
      return [time, ...lines].join('<br/>')
    },
  }),
)

/** 堆叠图里所有系列共享同一份 x 轴点（来自 visibleTrend），dataIndex 直接对应它的下标。 */
function onChartClick(params: { dataIndex: number }) {
  const point = visibleTrend.value[params.dataIndex]
  if (point) snapshot.value = point
}

function resetSnapshot() {
  snapshot.value = null
}

const detailOption = computed(() => {
  const points = detailFrames.value.flatMap((f) => f.processes.map((p) => ({ t: f.t, ...p })))
  return {
    backgroundColor: 'transparent',
    textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif' },
    grid: { left: 44, right: 16, top: 36, bottom: 28 },
    legend: { top: 0, right: 0, itemWidth: 16, itemHeight: 2, textStyle: { color: '#52514e', fontSize: 12 } },
    tooltip: { trigger: 'axis', axisPointer: { type: 'line' }, appendToBody: true },
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
    <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px">
      <NRadioGroup :value="rangePreset" size="small" @update:value="(v) => applyRangePreset(v as RangePreset)">
        <NRadioButton v-for="opt in RANGE_OPTIONS" :key="opt.value" :value="opt.value">
          {{ opt.label }}
        </NRadioButton>
      </NRadioGroup>
      <NText v-if="BOOST_PRESETS.has(rangePreset)" depth="3" style="font-size: 12px">
        秒级采集中，仅覆盖开启之后的新数据
      </NText>
      <NText v-else depth="3" style="font-size: 12px">
        点图上某一刻看当时的进程快照<template v-if="numCpus">· 归一化按 {{ numCpus }} 核算</template>
      </NText>
    </div>

    <NCard size="small" title="各进程 CPU 占用趋势" :segmented="{ content: true }" style="margin-bottom: 12px">
      <NSpin :show="loading">
        <VChart
          v-if="visibleTrend.length"
          :option="cpuOverviewOption"
          autoresize
          style="height: 200px"
          @click="onChartClick"
        />
        <NEmpty v-else description="该时间段暂无数据" size="small" style="padding: 24px 0" />
      </NSpin>
    </NCard>

    <NCard size="small" title="各进程内存占用趋势" :segmented="{ content: true }" style="margin-bottom: 12px">
      <NSpin :show="loading">
        <VChart
          v-if="visibleTrend.length"
          :option="memOverviewOption"
          autoresize
          style="height: 200px"
          @click="onChartClick"
        />
        <NEmpty v-else description="该时间段暂无数据" size="small" style="padding: 24px 0" />
      </NSpin>
    </NCard>

    <NCard size="small" :segmented="{ content: true }">
      <template #header>
        <span v-if="snapshot">历史快照 · {{ snapshotTimeText }}</span>
        <span v-else>当前 Top 进程</span>
      </template>
      <template #header-extra>
        <NButton v-if="snapshot" size="tiny" @click="resetSnapshot">返回实时</NButton>
        <NText v-else depth="3" style="font-size: 12px">点进程行看完整历史</NText>
      </template>
      <NTag v-if="snapshot" size="small" type="info" style="margin-bottom: 8px">
        历史快照，不是实时数据
      </NTag>
      <NDataTable :columns="columns" :data="displayedProcesses" :row-props="rowProps" size="small" :bordered="false" />
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
