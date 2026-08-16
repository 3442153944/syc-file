<script setup lang="ts">
// 剪贴板同步：开关 + 立即推送 + 历史列表（点一下写回本机剪贴板）。
//
// 设计取舍写在这里，别改坏了：
//  1. 默认关闭。剪贴板里常有密码和验证码，必须用户显式打开，页面上也要把「内容会经过服务器」讲明白。
//  2. 手机端读不到后台剪贴板（Android 10+ 限制），所以历史列表不是附加功能，
//     而是移动端**主要的取用方式**——桌面端也保留它，形态才一致。
//  3. 服务端只在 Redis 存 24 小时 / 50 条，不进数据库、不进操作日志。
import { ref, onMounted, onUnmounted } from 'vue'
import { invoke, isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import {
  NCard, NSpace, NSwitch, NButton, NList, NListItem, NThing, NEmpty,
  NText, NTag, NAlert, NPopconfirm, useMessage,
} from 'naive-ui'
import { pushClipboard, getClipboardHistory, clearClipboardHistory } from '@/api/clipboard/clipboardApi'
import type { ClipItem } from '@/api/clipboard/clipboardApi'

const message = useMessage()
const inTauri = isTauri()

const enabled = ref(false)
const autoApply = ref(true)
const history = ref<ClipItem[]>([])
const loading = ref(false)
let unlisten: UnlistenFn | null = null

async function loadSettings() {
  if (!inTauri) return
  try {
    const s = await invoke<{ enabled: boolean; auto_apply: boolean }>('get_clipboard_settings')
    enabled.value = s.enabled
    autoApply.value = s.auto_apply
  } catch { /* 取不到就用默认值 */ }
}

async function saveSettings() {
  if (!inTauri) return
  await invoke('set_clipboard_settings', { enabled: enabled.value, autoApply: autoApply.value })
}

async function loadHistory() {
  loading.value = true
  try {
    history.value = await getClipboardHistory(50)
  } catch (e: any) {
    message.error(`读取历史失败：${e?.message || e}`)
  } finally {
    loading.value = false
  }
}

async function pushNow() {
  try {
    if (inTauri) {
      // 桌面端由 Rust 读本机剪贴板再推，前端拿不到系统剪贴板
      await invoke('push_clipboard_now')
    } else {
      const text = await navigator.clipboard.readText()
      if (!text) return message.warning('剪贴板为空')
      await pushClipboard(text)
    }
    message.success('已推送到其它设备')
    await loadHistory()
  } catch (e: any) {
    message.error(`推送失败：${e?.message || e}`)
  }
}

/** 把某条历史写回本机剪贴板。 */
async function useItem(item: ClipItem) {
  try {
    if (inTauri) {
      // 走 Rust：写入的同时会记指纹 + 设静音窗口，否则监听会把它当成新复制再推一遍
      await invoke('apply_clipboard_text', { text: item.content })
    } else {
      await navigator.clipboard.writeText(item.content)
    }
    message.success('已复制到剪贴板')
  } catch (e: any) {
    message.error(`复制失败：${e?.message || e}`)
  }
}

async function doClear() {
  try {
    await clearClipboardHistory()
    history.value = []
    message.success('已清空')
  } catch (e: any) {
    message.error(`清空失败：${e?.message || e}`)
  }
}

onMounted(async () => {
  await loadSettings()
  await loadHistory()
  if (inTauri) {
    // 别的设备推来的内容：Rust 侧收到后转成事件，这里实时插到列表顶部
    unlisten = await listen<ClipItem>('clipboard-received', (e) => {
      history.value = [e.payload, ...history.value].slice(0, 50)
    })
  }
})
onUnmounted(() => {
  if (unlisten) unlisten()
})

function fmtTime(sec: number): string {
  if (!sec) return ''
  const d = new Date(sec * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function preview(s: string): string {
  const oneLine = s.replace(/\s+/g, ' ').trim()
  return oneLine.length > 160 ? `${oneLine.slice(0, 160)}…` : oneLine
}
</script>

<template>
  <div class="clipboard-sync">
    <NSpace justify="space-between" align="center" style="margin-bottom: 12px">
      <h2 style="margin: 0">剪贴板同步</h2>
      <NSpace>
        <NButton size="small" @click="loadHistory" :loading="loading">刷新</NButton>
        <NButton size="small" type="primary" @click="pushNow">推送当前剪贴板</NButton>
      </NSpace>
    </NSpace>

    <NAlert type="warning" style="margin-bottom: 12px" :bordered="false">
      剪贴板内容会经过服务器转发，并在服务器上保留 <b>24 小时</b>（最多 50 条，仅存于内存缓存，
      不写入数据库、不记入操作日志）。<b>复制密码、验证码期间请关闭同步。</b>
    </NAlert>

    <NCard size="small" title="设置" style="margin-bottom: 12px" v-if="inTauri">
      <NSpace vertical size="large">
        <NSpace align="center" justify="space-between">
          <div>
            <NText>自动同步本机剪贴板</NText>
            <NText depth="3" style="display: block; font-size: 12px">
              开启后每次复制都会自动推送到其它在线设备；关闭则只能用上方按钮手动推送
            </NText>
          </div>
          <NSwitch v-model:value="enabled" @update:value="saveSettings" />
        </NSpace>
        <NSpace align="center" justify="space-between">
          <div>
            <NText>自动写入本机剪贴板</NText>
            <NText depth="3" style="display: block; font-size: 12px">
              收到其它设备的内容时直接写入本机剪贴板；关闭则只进下方历史，由你手动取用
            </NText>
          </div>
          <NSwitch v-model:value="autoApply" @update:value="saveSettings" />
        </NSpace>
      </NSpace>
    </NCard>

    <NCard size="small" title="历史" :segmented="{ content: true }">
      <template #header-extra>
        <NPopconfirm @positive-click="doClear">
          <template #trigger>
            <NButton size="tiny" quaternary type="error">清空</NButton>
          </template>
          确定清空服务器上的剪贴板历史？
        </NPopconfirm>
      </template>

      <NList v-if="history.length" hoverable clickable>
        <NListItem v-for="item in history" :key="item.id" @click="useItem(item)">
          <NThing>
            <template #header>
              <NText style="word-break: break-all">{{ preview(item.content) }}</NText>
            </template>
            <template #description>
              <NSpace size="small" align="center">
                <NTag size="small" type="info">{{ item.device_name || item.device_id || '未知设备' }}</NTag>
                <NText depth="3" style="font-size: 12px">
                  {{ item.size }} 字节 · {{ fmtTime(item.created_at) }}
                </NText>
              </NSpace>
            </template>
          </NThing>
        </NListItem>
      </NList>
      <NEmpty v-else description="还没有记录。在任意设备上复制点什么，或点右上角推送当前剪贴板" size="small" />
    </NCard>
  </div>
</template>

<style scoped>
.clipboard-sync {
  padding: 16px;
}
</style>
