<script setup lang="ts">
// 网络监控：服务端网卡吞吐 + WS 在线连接概况，轮询 /v1/monitor/network。
// 速率由服务端按两次调用的计数器差值算出，所以首次进来是 0，第二次起才有值。
import { ref, onMounted, onUnmounted, computed, h } from 'vue'
import { NCard, NGrid, NGi, NStatistic, NSpace, NText, NDataTable, NSelect, NSpin, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getNetworkMetrics } from '@/api/admin/adminApi'
import type { NetworkMetrics } from '@/api/admin/adminTypes'

const metrics = ref<NetworkMetrics | null>(null)
const loading = ref(false)
const errorMsg = ref('')
const intervalMs = ref(3000)
let timer: number | undefined

/** 最近的速率采样，用来画一条简易走势（不引图表库，够看趋势即可）。 */
const HISTORY_LEN = 60
const sendHistory = ref<number[]>([])
const recvHistory = ref<number[]>([])

const intervalOptions = [
  { label: '1 秒', value: 1000 },
  { label: '3 秒', value: 3000 },
  { label: '5 秒', value: 5000 },
  { label: '暂停', value: 0 },
]

async function refresh() {
  loading.value = true
  try {
    const m = await getNetworkMetrics()
    metrics.value = m
    sendHistory.value = [...sendHistory.value, m.send_rate].slice(-HISTORY_LEN)
    recvHistory.value = [...recvHistory.value, m.recv_rate].slice(-HISTORY_LEN)
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
  if (intervalMs.value > 0) timer = window.setInterval(refresh, intervalMs.value)
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

const fmtRate = (n: number) => `${fmtBytes(n)}/s`

/** 把一串速率映射成 0-100 的柱高（按窗口内峰值归一，峰值为 0 时全 0）。 */
function bars(series: number[]): number[] {
  const peak = Math.max(...series, 1)
  return series.map((v) => Math.round((v / peak) * 100))
}

const sendBars = computed(() => bars(sendHistory.value))
const recvBars = computed(() => bars(recvHistory.value))

type NIC = NetworkMetrics['interfaces'][number]
const nicColumns: DataTableColumns<NIC> = [
  { title: '网卡', key: 'name', width: 160 },
  { title: '发送', key: 'bytes_sent', render: (r) => fmtBytes(r.bytes_sent), width: 110 },
  { title: '接收', key: 'bytes_recv', render: (r) => fmtBytes(r.bytes_recv), width: 110 },
  { title: '发包', key: 'packets_sent', width: 110 },
  { title: '收包', key: 'packets_recv', width: 110 },
  {
    title: '错误 / 丢弃',
    key: 'err',
    render: (r) => {
      const bad = r.errin + r.errout + r.dropin + r.dropout
      return h(
        NTag,
        { type: bad > 0 ? 'warning' : 'success', size: 'small' },
        { default: () => `${r.errin + r.errout} / ${r.dropin + r.dropout}` },
      )
    },
  },
]
</script>

<template>
  <div class="monitor-network">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <NSpace align="center">
        <h2 style="margin: 0">网络监控</h2>
        <NSpin :show="loading" size="small" />
      </NSpace>
      <NSpace align="center">
        <NText depth="3">刷新间隔</NText>
        <NSelect v-model:value="intervalMs" :options="intervalOptions" style="width: 110px"
                 @update:value="restartTimer" />
      </NSpace>
    </NSpace>

    <NCard v-if="errorMsg" size="small" style="margin-bottom: 12px">
      <NText type="error">读取失败：{{ errorMsg }}</NText>
    </NCard>

    <template v-if="metrics">
      <NGrid :cols="4" :x-gap="12" :y-gap="12" responsive="screen" item-responsive>
        <NGi span="4 m:2 l:1">
          <NCard size="small">
            <NStatistic label="上行速率" :value="fmtRate(metrics.send_rate)" />
            <NText depth="3" style="font-size: 12px">累计 {{ fmtBytes(metrics.bytes_sent) }}</NText>
          </NCard>
        </NGi>
        <NGi span="4 m:2 l:1">
          <NCard size="small">
            <NStatistic label="下行速率" :value="fmtRate(metrics.recv_rate)" />
            <NText depth="3" style="font-size: 12px">累计 {{ fmtBytes(metrics.bytes_recv) }}</NText>
          </NCard>
        </NGi>
        <NGi span="4 m:2 l:1">
          <NCard size="small">
            <NStatistic label="在线设备" :value="metrics.online_devices" />
            <NText depth="3" style="font-size: 12px">{{ metrics.online_users }} 个用户在线</NText>
          </NCard>
        </NGi>
        <NGi span="4 m:2 l:1">
          <NCard size="small">
            <NStatistic label="活跃连接" :value="metrics.active_connections" />
            <NText depth="3" style="font-size: 12px">一台设备可能有多条</NText>
          </NCard>
        </NGi>
      </NGrid>

      <NCard size="small" title="速率走势（按窗口峰值归一）" style="margin-top: 12px">
        <div class="spark-row">
          <NTag size="small" type="info">上行</NTag>
          <div class="spark">
            <div v-for="(v, i) in sendBars" :key="i" class="bar up" :style="{ height: `${v}%` }" />
          </div>
        </div>
        <div class="spark-row" style="margin-top: 12px">
          <NTag size="small" type="success">下行</NTag>
          <div class="spark">
            <div v-for="(v, i) in recvBars" :key="i" class="bar down" :style="{ height: `${v}%` }" />
          </div>
        </div>
      </NCard>

      <NCard size="small" title="网卡" style="margin-top: 12px">
        <NDataTable :columns="nicColumns" :data="metrics.interfaces" :bordered="false" size="small" />
      </NCard>
    </template>
  </div>
</template>

<style scoped>
.monitor-network {
  padding: 16px;
}

.spark-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
}

.spark {
  flex: 1;
  height: 64px;
  display: flex;
  align-items: flex-end;
  gap: 2px;
}

.bar {
  flex: 1;
  min-height: 2px;
  border-radius: 2px 2px 0 0;
}

.bar.up {
  background: #2080f0;
}

.bar.down {
  background: #18a058;
}
</style>
