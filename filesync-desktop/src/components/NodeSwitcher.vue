<script setup lang="ts">
/**
 * 菜单栏上的网络节点指示器 + 快捷切换。
 *
 * 放在菜单栏是因为它是唯一「哪个界面都在」的位置 —— 包括登录页。服务器连不上的时候
 * 用户得能在登录之前就换条路，否则就只能卡在登录页干瞪眼。
 *
 * 显示逻辑：小圆点 = 当前节点的探测结果（绿=通 / 红=不通 / 灰=还没测），
 * 点开下拉可以直接切节点、立即检测、或打开完整的节点设置面板。
 *
 * 面板（ServerSettings）由这个组件自己挂，所以登录页只要放一个 <NodeSwitcher/> 就齐活；
 * 原生菜单栏的「网络节点设置…」会发 open-network-settings 事件，这里接住并弹面板。
 */
import {computed, onMounted, onBeforeUnmount, ref} from 'vue'
import {NDropdown, NTag, useMessage} from 'naive-ui'
import type {DropdownOption} from 'naive-ui'
import {isTauri} from '@tauri-apps/api/core'
import {netState, probeAll, switchNode, healthOf} from '@/api/net'
import ServerSettings from '@/views/person/ServerSettings.vue'

const message = useMessage()
const showSettings = ref(false)
const probing = ref(false)
let unlisten: (() => void) | null = null

/** 当前节点的健康状态，决定小圆点的颜色 */
const activeHealth = computed(() => healthOf(netState.activeId))

const dotClass = computed(() => {
  if (netState.offline) return 'dot offline'
  const h = activeHealth.value
  if (!h) return 'dot unknown'
  return h.reachable ? 'dot ok' : 'dot offline'
})

const label = computed(() => netState.activeName || '未选择节点')

const tooltip = computed(() => {
  const h = activeHealth.value
  if (!h) return `${netState.serverUrl || '未配置'}（尚未检测）`
  if (!h.reachable) return `${h.serverUrl}\n不可达：${h.message}`
  return `${h.serverUrl}\n延迟 ${h.latencyMs} ms`
})

const options = computed<DropdownOption[]>(() => {
  const nodeItems: DropdownOption[] = netState.nodes.map(n => {
    const h = healthOf(n.id)
    const suffix = !h ? '未检测' : h.reachable ? `${h.latencyMs} ms` : '不可达'
    return {
      key: 'node:' + n.id,
      label: `${n.id === netState.activeId ? '● ' : '○ '}${n.name}  ·  ${suffix}`,
      disabled: n.id === netState.activeId,
    }
  })
  return [
    ...nodeItems,
    {type: 'divider', key: 'd1'},
    {key: 'probe', label: probing.value ? '检测中…' : '立即检测所有节点'},
    {key: 'settings', label: '网络节点设置…'},
  ]
})

async function handleSelect(key: string) {
  if (key === 'settings') {
    showSettings.value = true
    return
  }
  if (key === 'probe') {
    probing.value = true
    try {
      await probeAll()
    } catch (e) {
      message.error(String(e))
    } finally {
      probing.value = false
    }
    return
  }
  if (key.startsWith('node:')) {
    const id = key.slice(5)
    try {
      await switchNode(id)
      message.success(`已切换到 ${netState.activeName}`)
    } catch (e) {
      message.error(String(e))
    }
  }
}

onMounted(async () => {
  if (!isTauri()) return
  // 原生菜单栏「网络 → 网络节点设置…」发来的事件
  const {listen} = await import('@tauri-apps/api/event')
  unlisten = await listen('open-network-settings', () => {
    showSettings.value = true
  })
})

onBeforeUnmount(() => {
  unlisten?.()
  unlisten = null
})

// 菜单栏上的齿轮图标也复用这个面板，不必再挂一份
defineExpose({open: () => (showSettings.value = true)})
</script>

<template>
  <n-dropdown trigger="click" :options="options" @select="handleSelect">
    <div class="node-switcher" :title="tooltip">
      <span :class="dotClass"></span>
      <span class="node-name">{{ label }}</span>
      <n-tag v-if="netState.offline" size="small" type="error" :bordered="false">离线</n-tag>
    </div>
  </n-dropdown>

  <ServerSettings :show="showSettings" @close="showSettings = false"/>
</template>

<style scoped>
.node-switcher {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 6px;
  transition: background-color 0.2s;
  max-width: 180px;
}

.node-switcher:hover {
  background-color: rgba(0, 0, 0, 0.05);
}

.node-name {
  font-size: 13px;
  color: #666;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
