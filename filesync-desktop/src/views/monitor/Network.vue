<script setup lang="ts">
// 网络监控：实时上/下行速率 + 累计流量 + 网卡明细 + WS 连接概况。数据经 WS 推送。
import { computed, ref, watch } from 'vue'
import {
  NCard, NGrid, NGi, NStatistic, NSpace, NText, NTag, NDataTable, NEmpty, NButton, useMessage,
} from 'naive-ui'
import { useMonitor, fmtBytes, fmtRate } from '@/api/monitor/useMonitor'
import { netState, probeAll, switchNode, healthOf } from '@/api/net'

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

// ── 节点灾备概览 ────────────────────────────────────────────────────────────
// 客户端自己的出网链路状态（和上面那些服务端指标是两回事）：当前走哪个节点、
// 各节点最近一次探测的延迟。默认 15 分钟巡检一次，这里可以手动立刻测一遍。
const message = useMessage()
const probing = ref(false)

const nodeRows = computed(() =>
  netState.nodes.map((n) => {
    const h = healthOf(n.id)
    return {
      id: n.id,
      name: n.name,
      url: n.server_url,
      active: n.id === netState.activeId,
      reachable: h?.reachable ?? null,
      latency: h?.reachable ? `${h.latencyMs} ms` : h ? (h.message || '不可达') : '未检测',
      checkedAt: h?.checkedAt ? new Date(h.checkedAt).toLocaleTimeString() : '—',
    }
  }),
)

async function handleProbe() {
  probing.value = true
  try {
    await probeAll()
  } catch (e) {
    message.error(String(e))
  } finally {
    probing.value = false
  }
}

async function handleSwitch(id: string) {
  try {
    await switchNode(id)
    message.success(`已切换到 ${netState.activeName}`)
  } catch (e) {
    message.error(String(e))
  }
}

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

    <NCard size="small" style="margin-top: 12px">
      <template #header>
        <NSpace align="center" :size="8">
          <span>节点灾备</span>
          <NTag size="small" :type="netState.offline ? 'error' : 'success'" :bordered="false" round>
            {{ netState.offline ? '全部不可达' : netState.activeName || '未选择' }}
          </NTag>
          <NText depth="3" style="font-size: 12px">
            自动切换{{ netState.autoFailover ? '已开启' : '已关闭' }} · 每 {{ netState.intervalMinutes }} 分钟巡检
          </NText>
        </NSpace>
      </template>
      <template #header-extra>
        <NButton size="tiny" :loading="probing" @click="handleProbe">立即检测</NButton>
      </template>
      <NEmpty v-if="!nodeRows.length" description="尚未加载节点列表" size="small" />
      <div v-else class="node-grid">
        <div v-for="row in nodeRows" :key="row.id" class="node-item" :class="{ active: row.active }">
          <div class="node-head">
            <span class="node-dot" :class="row.reachable === null ? 'unknown' : row.reachable ? 'ok' : 'bad'" />
            <span class="node-name">{{ row.name }}</span>
            <NTag v-if="row.active" size="tiny" type="info" :bordered="false">当前</NTag>
          </div>
          <div class="node-meta">{{ row.url }}</div>
          <div class="node-meta">{{ row.latency }} · {{ row.checkedAt }}</div>
          <NButton v-if="!row.active" size="tiny" quaternary @click="handleSwitch(row.id)">切换到此节点</NButton>
        </div>
      </div>
    </NCard>

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
.node-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 10px;
}
.node-item {
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 8px;
  padding: 10px 12px;
}
.node-item.active {
  border-color: #409eff;
  background: rgba(64, 158, 255, 0.06);
}
.node-head {
  display: flex;
  align-items: center;
  gap: 6px;
}
.node-name {
  font-size: 13px;
}
.node-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #c0c4cc;
  flex-shrink: 0;
}
.node-dot.ok {
  background: #67c23a;
}
.node-dot.bad {
  background: #f56c6c;
}
.node-meta {
  font-size: 12px;
  color: #909399;
  margin-top: 3px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
