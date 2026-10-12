<script setup lang="ts">
import {computed, onMounted, onUnmounted, ref} from 'vue'
import {invoke, isTauri} from '@tauri-apps/api/core'
import {listen, type UnlistenFn} from '@tauri-apps/api/event'
import {NModal, NButton, NProgress, NSpace, NTag, useMessage} from 'naive-ui'
import {useRouter} from 'vue-router'

interface UpdateStatus {
  currentVersion: string; platform: string; arch: string; bundleType: string
  stage: string; version: string; notes: string; mandatory: boolean
  downloaded: number; total: number | null; error: string | null
}
const status = ref<UpdateStatus | null>(null)
const visible = ref(false)
const message = useMessage()
const router = useRouter()
const unlisteners: UnlistenFn[] = []
const percent = computed(() => status.value?.total ? Math.min(100, Math.floor(status.value.downloaded / status.value.total * 100)) : 0)
const working = computed(() => ['checking','downloading','installing'].includes(status.value?.stage || ''))
function receive(value: UpdateStatus) {
  status.value = value
  if (['available','ready'].includes(value.stage) && value.mandatory) visible.value = true
  else if (value.stage === 'available') visible.value = true
}
async function check(manual = false) {
  if (!isTauri()) return
  if (manual) visible.value = true
  try {
    const result = await invoke<UpdateStatus>('desktop_check_update')
    receive(result)
    if (manual && result.stage === 'idle') message.success('当前已是最新版本')
  } catch (e) { if (manual) message.error(String(e)) }
}
async function download() {
  try { await invoke('desktop_download_update') }
  catch (e) { message.error(String(e)) }
}
async function install() {
  try { await invoke('desktop_install_update') }
  catch (e) { message.error(String(e)) }
}
function open() { visible.value = true; void check(true) }
function transfers() { visible.value = false; void router.push('/transfers') }
onMounted(async () => {
  if (!isTauri()) return
  unlisteners.push(await listen<UpdateStatus>('desktop-update-changed', event => receive(event.payload)))
  unlisteners.push(await listen<{platform: string; arch?: string; bundle_type?: string}>('app-update', event => {
    const t = status.value
    if (t && event.payload.platform === t.platform && event.payload.arch === t.arch && event.payload.bundle_type === t.bundleType) void check()
  }))
  // 先订阅再取快照，主窗口关闭重开后仍可恢复下载进度及安装入口。
  const snapshot = await invoke<UpdateStatus>('desktop_update_status')
  if (!status.value) receive(snapshot)
  window.addEventListener('desktop-update-open', open)
})
onUnmounted(() => { unlisteners.forEach(fn => fn()); window.removeEventListener('desktop-update-open', open) })
</script>

<template>
  <n-modal v-model:show="visible" preset="card" title="桌面端更新" style="width:min(480px,95vw)" :mask-closable="!status?.mandatory && !working" :closable="!status?.mandatory && !working">
    <template v-if="status">
      <p>当前版本 {{ status.currentVersion }} · {{ status.platform }} {{ status.arch }}</p>
      <p v-if="status.version">新版本 {{ status.version }} <n-tag v-if="status.mandatory" type="warning" size="small">需要更新</n-tag></p>
      <p v-if="status.stage === 'checking'">正在检查更新…</p>
      <p v-if="status.stage === 'idle'">当前已是最新版本</p>
      <p v-if="status.notes" style="white-space:pre-wrap">{{ status.notes }}</p>
      <template v-if="status.stage === 'downloading'">
        <n-progress type="line" :percentage="percent" :processing="true" />
        <p>已下载 {{ (status.downloaded / 1048576).toFixed(1) }} MB{{ status.total ? ' / ' + (status.total / 1048576).toFixed(1) + ' MB' : '' }}</p>
      </template>
      <p v-if="status.stage === 'ready'">已下载并验证签名，安装后会重新启动。</p>
      <p v-if="status.stage === 'installing'">正在安装，请完成系统的权限确认…</p>
      <p v-if="status.error" style="color:#d03050;word-break:break-word">{{ status.error }}</p>
      <n-space justify="end">
        <n-button v-if="status.mandatory && !working" @click="transfers">先处理传输</n-button>
        <n-button v-if="!status.mandatory && !working" @click="visible = false">稍后</n-button>
        <n-button v-if="status.stage === 'available'" type="primary" @click="download">下载更新</n-button>
        <n-button v-if="status.stage === 'ready'" type="primary" @click="install">安装并重启</n-button>
        <n-button v-if="status.stage === 'error' || status.stage === 'idle'" @click="check(true)">重新检查</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
