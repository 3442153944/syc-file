<script setup lang="ts">
// 系统监控：CPU / 内存 / 主机 / 磁盘。数据经 WS 实时推送（见 useMonitor）。
import { computed } from 'vue'
import {
  NCard, NGrid, NGi, NProgress, NStatistic, NSpace, NText, NTag, NEmpty, NSpin,
} from 'naive-ui'
import { useMonitor, fmtBytes, fmtUptime } from '@/api/monitor/useMonitor'

const { system, connected } = useMonitor(2)

const cpu = computed(() => system.value?.cpu)
const mem = computed(() => system.value?.memory)
const host = computed(() => system.value?.host)
const disks = computed(() => system.value?.disks ?? [])

function usageColor(p: number): string {
  if (p >= 90) return '#e53935'
  if (p >= 70) return '#fb8c00'
  return '#409eff'
}
</script>

<template>
  <div class="monitor-system">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <h2 style="margin: 0">系统状态</h2>
      <NTag :type="connected ? 'success' : 'warning'" size="small" round>
        {{ connected ? '实时' : '连接中…' }}
      </NTag>
    </NSpace>

    <NSpin :show="!system">
      <template #description>正在获取系统指标…</template>
      <div v-if="!system" style="height: 160px" />

      <NGrid v-else :cols="2" :x-gap="12" :y-gap="12" responsive="screen" item-responsive>
        <!-- CPU -->
        <NGi span="2 m:1">
          <NCard size="small" title="CPU">
            <NSpace vertical>
              <NProgress
                type="line"
                :percentage="Math.round(cpu?.used_percent ?? 0)"
                :color="usageColor(cpu?.used_percent ?? 0)"
                :height="18"
                indicator-placement="inside"
              />
              <NText depth="3" style="font-size: 12px">
                {{ cpu?.model_name || '未知型号' }} · {{ cpu?.cores }} 核
              </NText>
              <NSpace v-if="cpu?.per_core?.length" size="small" wrap>
                <div v-for="(c, i) in cpu.per_core" :key="i" class="core-cell" :title="`核 ${i}: ${c.toFixed(0)}%`">
                  <div class="core-bar" :style="{ height: `${Math.max(2, c)}%`, background: usageColor(c) }" />
                </div>
              </NSpace>
              <NText v-if="(cpu?.load1 ?? 0) > 0" depth="3" style="font-size: 12px">
                负载 {{ cpu?.load1.toFixed(2) }} / {{ cpu?.load5.toFixed(2) }} / {{ cpu?.load15.toFixed(2) }}
              </NText>
            </NSpace>
          </NCard>
        </NGi>

        <!-- 内存 -->
        <NGi span="2 m:1">
          <NCard size="small" title="内存">
            <NSpace vertical>
              <NProgress
                type="line"
                :percentage="Math.round(mem?.used_percent ?? 0)"
                :color="usageColor(mem?.used_percent ?? 0)"
                :height="18"
                indicator-placement="inside"
              />
              <NText depth="3" style="font-size: 13px">
                {{ fmtBytes(mem?.used ?? 0) }} / {{ fmtBytes(mem?.total ?? 0) }}
              </NText>
              <NText v-if="(mem?.swap_total ?? 0) > 0" depth="3" style="font-size: 12px">
                交换区 {{ fmtBytes(mem?.swap_used ?? 0) }} / {{ fmtBytes(mem?.swap_total ?? 0) }}
              </NText>
            </NSpace>
          </NCard>
        </NGi>

        <!-- 主机 -->
        <NGi span="2">
          <NCard size="small" title="主机">
            <NGrid :cols="4" :x-gap="12" responsive="screen" item-responsive>
              <NGi span="2 m:1"><NStatistic label="主机名" :value="host?.hostname || '—'" /></NGi>
              <NGi span="2 m:1"><NStatistic label="系统" :value="`${host?.platform || host?.os || '—'} ${host?.platform_version || ''}`" /></NGi>
              <NGi span="2 m:1"><NStatistic label="运行时长" :value="fmtUptime(host?.uptime_seconds ?? 0)" /></NGi>
              <NGi span="2 m:1"><NStatistic label="进程数" :value="String(host?.procs ?? 0)" /></NGi>
            </NGrid>
          </NCard>
        </NGi>

        <!-- 磁盘 -->
        <NGi span="2">
          <NCard size="small" title="磁盘">
            <NEmpty v-if="!disks.length" description="无磁盘信息" size="small" />
            <NSpace v-else vertical size="large">
              <div v-for="d in disks" :key="d.path">
                <NSpace justify="space-between" style="margin-bottom: 4px">
                  <NText>{{ d.path }} <NText depth="3" style="font-size: 12px">{{ d.fstype }}</NText></NText>
                  <NText depth="3" style="font-size: 12px">
                    {{ fmtBytes(d.used) }} / {{ fmtBytes(d.total) }}
                  </NText>
                </NSpace>
                <NProgress
                  type="line"
                  :percentage="Math.round(d.used_percent)"
                  :color="usageColor(d.used_percent)"
                  :height="12"
                  :show-indicator="false"
                />
              </div>
            </NSpace>
          </NCard>
        </NGi>
      </NGrid>
    </NSpin>
  </div>
</template>

<style scoped>
.monitor-system {
  padding: 16px;
}
.core-cell {
  width: 10px;
  height: 40px;
  display: flex;
  align-items: flex-end;
  background: rgba(128, 128, 128, 0.12);
  border-radius: 2px;
  overflow: hidden;
}
.core-bar {
  width: 100%;
  border-radius: 2px;
  transition: height 0.4s ease;
}
</style>
