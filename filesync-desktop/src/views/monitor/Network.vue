<script setup lang="ts">
// 网络监控：实时上/下行速率 + 累计流量 + 网卡明细 + WS 连接概况。数据经 WS 推送。
import { computed, ref, watch } from 'vue'
import {
  NCard, NGrid, NGi, NStatistic, NSpace, NText, NTag, NDataTable, NEmpty,
} from 'naive-ui'
import { useMonitor, fmtBytes, fmtRate } from '@/api/monitor/useMonitor'

const { network, connected } = useMonitor(2)

// 速率曲线：保留最近 60 个点，用纯 SVG 画迷你折线（不引图表库）
const HISTORY = 60
const sendHist = ref<number[]>([])
const recvHist = ref<number[]>([])

watch(network, (n) => {
  if (!n) return
  sendHist.value = [...sendHist.value, n.send_rate].slice(-HISTORY)
  recvHist.value = [...recvHist.value, n.recv_rate].slice(-HISTORY)
})

/** 把一串速率值转成 SVG polyline 点集（宽 300 高 60，按当前窗口最大值归一）。 */
function sparkline(data: number[]): string {
  if (data.length < 2) return ''
  const w = 300
  const h = 60
  const max = Math.max(...data, 1)
  const step = w / (HISTORY - 1)
  return data
    .map((v, i) => {
      const x = (i + (HISTORY - data.length)) * step
      const y = h - (v / max) * h
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const interfaces = computed(() => network.value?.interfaces ?? [])

const nicColumns = [
  { title: '网卡', key: 'name' },
  { title: '发送', key: 'bytes_sent', render: (r: any) => fmtBytes(r.bytes_sent) },
  { title: '接收', key: 'bytes_recv', render: (r: any) => fmtBytes(r.bytes_recv) },
  { title: '错误(收/发)', key: 'err', render: (r: any) => `${r.errin ?? 0}/${r.errout ?? 0}` },
  { title: '丢包(收/发)', key: 'drop', render: (r: any) => `${r.dropin ?? 0}/${r.dropout ?? 0}` },
]
</script>

<template>
  <div class="monitor-network">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <h2 style="margin: 0">网络监控</h2>
      <NTag :type="connected ? 'success' : 'warning'" size="small" round>
        {{ connected ? '实时' : '连接中…' }}
      </NTag>
    </NSpace>

    <NGrid :cols="4" :x-gap="12" :y-gap="12" responsive="screen" item-responsive>
      <NGi span="2 m:1">
        <NCard size="small"><NStatistic label="↑ 上行速率" :value="fmtRate(network?.send_rate ?? 0)" /></NCard>
      </NGi>
      <NGi span="2 m:1">
        <NCard size="small"><NStatistic label="↓ 下行速率" :value="fmtRate(network?.recv_rate ?? 0)" /></NCard>
      </NGi>
      <NGi span="2 m:1">
        <NCard size="small"><NStatistic label="在线设备" :value="String(network?.online_devices ?? 0)" /></NCard>
      </NGi>
      <NGi span="2 m:1">
        <NCard size="small"><NStatistic label="活动连接" :value="String(network?.active_connections ?? 0)" /></NCard>
      </NGi>
    </NGrid>

    <NCard size="small" title="速率曲线" style="margin-top: 12px">
      <div class="chart">
        <svg viewBox="0 0 300 60" preserveAspectRatio="none" class="spark">
          <polyline :points="sparkline(recvHist)" fill="none" stroke="#409eff" stroke-width="1.5" />
          <polyline :points="sparkline(sendHist)" fill="none" stroke="#f0a020" stroke-width="1.5" />
        </svg>
      </div>
      <NSpace size="large" style="margin-top: 6px">
        <NText depth="3" style="font-size: 12px"><span class="dot recv" /> 下行</NText>
        <NText depth="3" style="font-size: 12px"><span class="dot send" /> 上行</NText>
        <NText depth="3" style="font-size: 12px">
          累计 ↑{{ fmtBytes(network?.bytes_sent ?? 0) }} · ↓{{ fmtBytes(network?.bytes_recv ?? 0) }}
        </NText>
      </NSpace>
    </NCard>

    <NCard size="small" title="网卡明细" style="margin-top: 12px">
      <NEmpty v-if="!interfaces.length" description="无网卡数据" size="small" />
      <NDataTable v-else :columns="nicColumns" :data="interfaces" size="small" :bordered="false" />
    </NCard>
  </div>
</template>

<style scoped>
.monitor-network {
  padding: 16px;
}
.chart {
  height: 80px;
  background: rgba(128, 128, 128, 0.06);
  border-radius: 6px;
  padding: 8px;
}
.spark {
  width: 100%;
  height: 100%;
}
.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 4px;
}
.dot.recv {
  background: #409eff;
}
.dot.send {
  background: #f0a020;
}
</style>
