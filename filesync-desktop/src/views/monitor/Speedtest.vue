<script setup lang="ts">
// 节点测速：桌面端多路径核心（sync_core::upload_planner）的探测面板。
// 对全部节点并发跑一轮「微样本 RTT + 2MiB 上行吞吐样本」，父链节点（如 hk 的上游是 ddns）
// 按共享瓶颈折算有效带宽；结果同时写入本地 planner 样本缓存，10 分钟内的上传直接复用。
import {computed, h, ref} from 'vue'
import {
  NAlert, NButton, NCard, NDataTable, NEmpty, NGi, NGrid, NSpace, NStatistic, NTag, NText, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import {netState, runSpeedtest, speedtestSupported, type NodeSpeed, type SpeedtestResult} from '@/api/net'
import {fmtRate} from '@/api/monitor/useMonitor'

const message = useMessage()
const supported = speedtestSupported()
const running = ref(false)
const error = ref('')
const result = ref<SpeedtestResult | null>(null)

async function handleRun() {
  running.value = true
  error.value = ''
  try {
    result.value = await runSpeedtest()
    message.success('测速完成，样本已缓存（10 分钟内的上传直接复用）')
  } catch (e: any) {
    error.value = e?.message || String(e)
    message.error(error.value)
  } finally {
    running.value = false
  }
}

const rows = computed(() => result.value?.nodes ?? [])
const totalEffective = computed(() => rows.value.reduce((s, n) => s + (n.effectiveBps || 0), 0))
const fastest = computed(() =>
    rows.value.filter(n => n.reachable && n.effectiveBps > 0)
        .sort((a, b) => b.effectiveBps - a.effectiveBps)[0],
)
const testedAt = computed(() => result.value ? new Date(result.value.testedAt).toLocaleString() : '—')

const columns: DataTableColumns<NodeSpeed> = [
  {
    title: '节点', key: 'name', minWidth: 170,
    render: (r) => h(NSpace, {size: 6, align: 'center', wrap: false}, () => [
      h('span', r.name),
      r.id === netState.activeId
          ? h(NTag, {size: 'tiny', type: 'info', bordered: false}, () => '当前')
          : null,
      r.parent
          ? h(NTag, {size: 'tiny', bordered: false}, () => `父: ${r.parent}`)
          : null,
    ]),
  },
  {title: '地址', key: 'serverUrl', minWidth: 200, ellipsis: {tooltip: true}},
  {
    title: '状态', key: 'reachable', width: 110,
    render: (r) => !r.reachable
        ? h(NTag, {size: 'small', type: 'error', bordered: false}, () => r.message || '不可达')
        : r.uploadBps > 0
            ? h(NTag, {size: 'small', type: 'success', bordered: false}, () => '可达')
            : h(NTag, {size: 'small', type: 'warning', bordered: false}, () => r.message || '仅 RTT'),
  },
  {title: 'RTT', key: 'rttMs', width: 90, render: (r) => r.reachable ? `${r.rttMs} ms` : '—'},
  {title: '上行带宽', key: 'uploadBps', width: 110, render: (r) => r.uploadBps > 0 ? fmtRate(r.uploadBps) : '—'},
  {
    title: '有效带宽', key: 'effectiveBps', width: 120,
    render: (r) => r.effectiveBps > 0
        ? h('span', {style: r.effectiveBps < r.uploadBps ? 'color: #e6a23c' : ''}, fmtRate(r.effectiveBps))
        : '—',
  },
  {title: '初始并发', key: 'window', width: 90, render: (r) => r.window > 0 ? String(r.window) : '—'},
  {title: '后端', key: 'backendNode', width: 100, render: (r) => r.backendNode || '—'},
]
</script>

<template>
  <div class="speedtest-page">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <h2 style="margin: 0">节点测速</h2>
      <NButton type="primary" :loading="running" :disabled="!supported" @click="handleRun">
        {{ running ? '测速中…' : '开始测速' }}
      </NButton>
    </NSpace>

    <NAlert v-if="!supported" type="info" :bordered="false" style="margin-bottom: 12px">
      测速依赖桌面端多路径核心（sync_core::upload_planner），Web 端暂不支持，请在桌面客户端使用本页面。
    </NAlert>
    <NAlert v-else title="测速说明" :bordered="false" style="margin-bottom: 12px">
      对全部节点并发探测：每个节点先发微样本测 RTT，再上传 2 MiB 样本测上行带宽。
      父链节点（如 hk 的上游是 ddns）按共享瓶颈折算「有效带宽」，初始并发是核心给出的起跑窗口
      （有效带宽 8 MB/s 折算 1 路）。测速结果会写入本地 planner 样本缓存，10 分钟内的上传直接复用、不再重复探测。
    </NAlert>

    <NAlert v-if="error" type="error" :bordered="false" style="margin-bottom: 12px">{{ error }}</NAlert>

    <NGrid v-if="result" :cols="3" :x-gap="12" :y-gap="12" responsive="screen" item-responsive
           style="margin-bottom: 12px">
      <NGi span="3 m:1">
        <NCard size="small">
          <NStatistic label="最快节点" :value="fastest?.name || '—'"/>
        </NCard>
      </NGi>
      <NGi span="3 m:1">
        <NCard size="small">
          <NStatistic label="多路径总有效带宽" :value="fmtRate(totalEffective)"/>
        </NCard>
      </NGi>
      <NGi span="3 m:1">
        <NCard size="small">
          <NStatistic label="测速时间" :value="testedAt"/>
        </NCard>
      </NGi>
    </NGrid>

    <NCard size="small" title="各节点结果">
      <NEmpty v-if="!rows.length" size="small"
              :description="running ? '测速中…' : '还没有结果，点右上角「开始测速」'"/>
      <template v-else>
        <NDataTable :columns="columns" :data="rows" size="small" :bordered="false"/>
        <NText depth="3" style="font-size: 12px; display: block; margin-top: 8px">
          有效带宽低于上行带宽的节点（橙色）与其他节点共享瓶颈；后端名不一致说明各入口指向了不同后端，
          上传会自动把这类节点排出路径池。
        </NText>
      </template>
    </NCard>
  </div>
</template>

<style scoped>
.speedtest-page {
  padding: 16px;
}
</style>
