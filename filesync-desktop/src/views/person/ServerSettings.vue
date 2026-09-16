<script setup lang="ts">
/**
 * 网络节点设置 —— 服务器灾备的管理面板。
 *
 * 两个内置节点对应两条 frp 隧道（ddns / 东京），可以改地址但删不掉；用户还能添加
 * 自定义节点。切换是即时的：点一下就换，在途请求会被立刻放弃（见 api/net.ts）。
 *
 * 登录页也会挂这个面板 —— 服务器连不上的时候，用户得能在登录之前换条路。
 */
import {computed, ref, watch} from 'vue'
import {
  NModal, NForm, NFormItem, NInput, NButton, NSpace, NSwitch, NInputNumber,
  NTag, NTooltip, useMessage,
} from 'naive-ui'
import {
  netState, probeAll, switchNode, saveNode, removeNode, setFailover, healthOf,
  type ServerNode,
} from '@/api/net'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()

const message = useMessage()
const probing = ref(false)
const busyId = ref('')

// 编辑态：null = 没在编辑；id 为空字符串 = 新增
const editing = ref<{ id: string; name: string; serverUrl: string; wsUrl: string } | null>(null)

const nodes = computed(() => netState.nodes)

// 打开面板时顺手探测一次，用户看到的延迟数字才是当下的
watch(() => props.show, async (v) => {
  if (v) await handleProbe()
})

function statusOf(node: ServerNode) {
  const h = healthOf(node.id)
  if (!h) return {type: 'default' as const, text: '未检测'}
  if (!h.reachable) return {type: 'error' as const, text: h.message || '不可达'}
  return {type: 'success' as const, text: `${h.latencyMs} ms`}
}

/** 服务端自报的信息，鼠标悬停时显示，用来确认「这两个域名是不是同一台后端」 */
function detailOf(node: ServerNode): string {
  const h = healthOf(node.id)
  if (!h || !h.reachable) return h?.message || '尚未探测到该节点'
  const parts = [`地址：${h.serverUrl}`]
  if (h.serverNode) parts.push(`服务端节点：${h.serverNode}`)
  if (h.version) parts.push(`版本：${h.version}`)
  if (h.wsConns) parts.push(`在线连接：${h.wsConns}`)
  if (h.uptime) parts.push(`已运行：${formatUptime(h.uptime)}`)
  if (Math.abs(h.clockSkewMs) > 3000) parts.push(`时钟偏差：${Math.round(h.clockSkewMs / 1000)} 秒`)
  return parts.join('\n')
}

function formatUptime(sec: number): string {
  if (sec < 3600) return `${Math.round(sec / 60)} 分钟`
  if (sec < 86400) return `${Math.round(sec / 3600)} 小时`
  return `${Math.round(sec / 86400)} 天`
}

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

async function handleSwitch(node: ServerNode) {
  if (node.id === netState.activeId) return
  busyId.value = node.id
  try {
    await switchNode(node.id)
    message.success(`已切换到 ${node.name}`)
  } catch (e) {
    message.error(String(e))
  } finally {
    busyId.value = ''
  }
}

function startAdd() {
  editing.value = {id: '', name: '', serverUrl: '', wsUrl: ''}
}

function startEdit(node: ServerNode) {
  editing.value = {id: node.id, name: node.name, serverUrl: node.server_url, wsUrl: node.ws_url}
}

async function handleSaveNode(activate: boolean) {
  const e = editing.value
  if (!e) return
  if (!e.serverUrl.trim()) {
    message.warning('请输入服务器地址')
    return
  }
  busyId.value = e.id || 'new'
  try {
    await saveNode({
      id: e.id || null,
      name: e.name,
      serverUrl: e.serverUrl,
      wsUrl: e.wsUrl || null,
      activate,
    })
    message.success(activate ? '已保存并切换' : '节点已保存')
    editing.value = null
    await handleProbe()
  } catch (err) {
    message.error(String(err))
  } finally {
    busyId.value = ''
  }
}

async function handleRemove(node: ServerNode) {
  busyId.value = node.id
  try {
    await removeNode(node.id)
    message.success('节点已删除')
  } catch (e) {
    message.error(String(e))
  } finally {
    busyId.value = ''
  }
}

async function handleAuto(v: boolean) {
  try {
    await setFailover({autoFailover: v})
  } catch (e) {
    message.error(String(e))
  }
}

async function handleInterval(v: number | null) {
  if (!v || v < 1) return
  try {
    await setFailover({intervalMinutes: v})
  } catch (e) {
    message.error(String(e))
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="(v:boolean) => !v && emit('close')" preset="card"
           title="网络节点设置" style="width:640px" :mask-closable="true">
    <div class="tip">
      客户端所有请求（含 WebSocket）都走当前节点。节点不可用时会自动切到延迟最低的可用节点，
      切换时在途请求会立刻放弃并在新节点上重来，不用等它们超时。
    </div>

    <div class="node-list">
      <div v-for="node in nodes" :key="node.id" class="node-row"
           :class="{active: node.id === netState.activeId}">
        <div class="node-main">
          <div class="node-title">
            <span class="name">{{ node.name }}</span>
            <n-tag v-if="node.id === netState.activeId" size="small" type="info" :bordered="false">
              当前
            </n-tag>
            <n-tag v-if="node.builtin" size="small" :bordered="false">内置</n-tag>
            <n-tooltip trigger="hover">
              <template #trigger>
                <n-tag size="small" :type="statusOf(node).type" :bordered="false">
                  {{ statusOf(node).text }}
                </n-tag>
              </template>
              <pre class="detail">{{ detailOf(node) }}</pre>
            </n-tooltip>
          </div>
          <div class="node-url">{{ node.server_url }}</div>
        </div>
        <n-space :size="6">
          <n-button size="tiny" :disabled="node.id === netState.activeId"
                    :loading="busyId === node.id" @click="handleSwitch(node)">
            切换
          </n-button>
          <n-button size="tiny" quaternary @click="startEdit(node)">编辑</n-button>
          <n-button size="tiny" quaternary type="error" :disabled="node.builtin"
                    @click="handleRemove(node)">
            删除
          </n-button>
        </n-space>
      </div>
    </div>

    <!-- 新增 / 编辑 -->
    <div v-if="editing" class="editor">
      <n-form label-placement="left" label-width="90px" size="small">
        <n-form-item label="节点名称">
          <n-input v-model:value="editing.name" placeholder="留空则用地址作为名称"/>
        </n-form-item>
        <n-form-item label="服务器地址">
          <n-input v-model:value="editing.serverUrl" placeholder="https://example.com/file"/>
        </n-form-item>
        <n-form-item label="WebSocket">
          <n-input v-model:value="editing.wsUrl" placeholder="留空自动推导（https→wss）"/>
        </n-form-item>
      </n-form>
      <n-space justify="end" :size="8">
        <n-button size="small" @click="editing = null">取消</n-button>
        <n-button size="small" @click="handleSaveNode(false)">保存</n-button>
        <n-button size="small" type="primary" @click="handleSaveNode(true)">保存并切换</n-button>
      </n-space>
    </div>

    <div class="settings">
      <div class="row">
        <span class="label">自动灾备切换</span>
        <n-switch :value="netState.autoFailover" @update:value="handleAuto" size="small"/>
        <span class="hint">当前节点探测失败时自动切到可用节点</span>
      </div>
      <div class="row">
        <span class="label">巡检间隔</span>
        <n-input-number :value="netState.intervalMinutes" @update:value="handleInterval"
                        size="small" :min="1" :max="1440" style="width:110px">
          <template #suffix>分钟</template>
        </n-input-number>
        <span class="hint">低频确认链路存活，默认 15 分钟；请求失败时会立即额外探测一次</span>
      </div>
    </div>

    <template #footer>
      <n-space justify="space-between">
        <n-space>
          <n-button size="small" @click="startAdd" :disabled="!!editing">添加节点</n-button>
          <n-button size="small" @click="handleProbe" :loading="probing">立即检测</n-button>
        </n-space>
        <n-button size="small" type="primary" @click="emit('close')">关闭</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.7;
  margin-bottom: 12px;
}

.node-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.node-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
}

.node-row.active {
  border-color: #409eff;
  background: #f4f9ff;
}

.node-main {
  min-width: 0;
  flex: 1;
}

.node-title {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.node-title .name {
  font-size: 14px;
  color: #303133;
}

.node-url {
  font-size: 12px;
  color: #909399;
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail {
  margin: 0;
  font-family: inherit;
  font-size: 12px;
  white-space: pre-wrap;
}

.editor {
  margin-top: 14px;
  padding: 12px;
  border: 1px dashed #dcdfe6;
  border-radius: 8px;
}

.settings {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid #ebeef5;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.settings .row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.settings .label {
  font-size: 13px;
  color: #606266;
  width: 88px;
}

.settings .hint {
  font-size: 12px;
  color: #a8abb2;
}
</style>
