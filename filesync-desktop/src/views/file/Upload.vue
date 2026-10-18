<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { isTauri } from '@tauri-apps/api/core'
import { open } from '@tauri-apps/plugin-dialog'
import { NAlert, NButton, NCard, NEmpty, NProgress, NSpace, NTabPane, NTabs, NTag, useMessage } from 'naive-ui'
import UploadSpaceAdmin from '@/components/UploadSpaceAdmin.vue'
import UploadSpaceStatus from '@/components/UploadSpaceStatus.vue'
import { myUploadSpace, type UploadSpace } from '@/api/file/storageApi'
import { useTransferStore } from '@/store/useTransferStore'
import { controlQuickPaste } from '@/composables/useQuickPaste'
import { currentLevel } from '@/utils/level'
import { formatBytes, formatSpeed } from '@/utils/speedMeter'

const message = useMessage()
const router = useRouter()
const store = useTransferStore()
const desktop = isTauri()
const isSuper = currentLevel() >= 3
const canAllocate = currentLevel() >= 2
const space = ref<UploadSpace | null>(null)
const loading = ref(false)
const loadError = ref('')
const input = ref<HTMLInputElement | null>(null)
const queue = ref<Array<{ id: number; name: string; source: string | File; error?: string }>>([])
const running = ref(false)
const stopQueue = ref(false)
let nextId = 0
const canUpload = computed(() => !!space.value && !space.value.frozen && !loading.value && !loadError.value)
const tasks = computed(() => store.uploads.filter(t => t.kind === 'manual'))
const statusText = { uploading: '上传中', paused: '已暂停', cancelled: '已取消', done: '已完成', error: '失败' }

async function loadSpace() {
  loading.value = true; loadError.value = ''
  try { space.value = await myUploadSpace() }
  catch (e) { loadError.value = String(e) }
  finally { loading.value = false }
}
async function chooseFiles() {
  if (!canUpload.value) return
  if (!desktop) { input.value?.click(); return }
  try {
    const result = await open({ multiple: true, directory: false })
    const paths = result ? Array.isArray(result) ? result : [result] : []
    for (const path of paths) queue.value.push({ id: ++nextId, name: path.split(/[\\/]/).pop() ?? path, source: path })
  } catch (e) { message.error(String(e)) }
}
function browserFiles(event: Event) {
  const target = event.target as HTMLInputElement
  for (const file of Array.from(target.files ?? [])) queue.value.push({ id: ++nextId, name: file.name, source: file })
  target.value = ''
}
async function start() {
  if (!canUpload.value || running.value || !space.value) return
  running.value = true; stopQueue.value = false
  try {
    // 每批开始重新读取服务端状态，避免使用已经到期或被停用的旧空间。
    await loadSpace()
    if (!canUpload.value || !space.value) return
    for (const row of [...queue.value]) {
      if (stopQueue.value) break
      row.error = undefined
      try {
        await store.startManualUpload(row.source, space.value.default_path || space.value.path)
        queue.value = queue.value.filter(q => q.id !== row.id)
      } catch (e) {
        row.error = String(e); message.error(`${row.name}：${e}`)
        // 容量或权限错误时停止继续提交，保留待传文件供用户处理。
        break
      }
    }
  } finally { running.value = false; await loadSpace() }
}
async function control(id: string, action: 'pause' | 'resume' | 'cancel') {
  try { await controlQuickPaste(id, action) } catch (e) { message.error(String(e)) }
}
function browse() {
  if (space.value) void router.push({ path: '/file/catalog', query: { path: space.value.path } })
}
onMounted(async () => { await store.ensureMirror(); await loadSpace() })
</script>

<template>
  <div class="upload-page">
    <input ref="input" type="file" multiple hidden @change="browserFiles" />
    <n-tabs type="line" @update:value="loadSpace">
      <n-tab-pane name="mine" tab="我的上传空间">
        <n-space vertical :size="16">
          <n-alert v-if="loadError" type="error">{{ loadError }}</n-alert>
          <n-alert v-else-if="!space && !loading" type="info">尚未分配上传空间，请联系系统管理员。{{ isSuper ? '可在“空间管理”中为账号分配空间。' : '' }}</n-alert>
          <upload-space-status v-if="space" :space="space" :loading="loading" @browse="browse" @refresh="loadSpace" />
          <n-card title="上传文件" size="small">
            <n-space vertical>
              <span v-if="space">默认上传目录：{{ space.default_path || space.path }}</span>
              <n-space>
                <n-button :disabled="!canUpload" @click="chooseFiles">选择文件</n-button>
                <n-button type="primary" :disabled="!canUpload || !queue.length || running" @click="start">开始上传{{ queue.length ? `（${queue.length}）` : '' }}</n-button>
                <n-button v-if="running" @click="stopQueue = true">当前文件完成后停止队列</n-button>
                <n-button v-else :disabled="!queue.length" @click="queue = []">清空待传</n-button>
              </n-space>
              <div v-for="row in queue" :key="row.id" class="queue-row"><div class="filename">{{ row.name }}<div v-if="row.error" class="error">{{ row.error }}</div></div><n-button size="small" :disabled="running" @click="queue = queue.filter(q => q.id !== row.id)">移除</n-button></div>
              <n-empty v-if="!queue.length" description="选择文件后上传到自己的空间" />
            </n-space>
          </n-card>
          <n-card title="上传进度" size="small">
            <n-empty v-if="!tasks.length" description="暂无上传任务" />
            <div v-for="task in tasks" :key="task.id" class="task">
              <n-space justify="space-between"><span class="filename">{{ task.name }}</span><n-tag>{{ statusText[task.status] }}</n-tag></n-space>
              <n-progress type="line" :percentage="task.total ? Math.min(100, Math.round(task.sent / task.total * 100)) : 0" :processing="task.status === 'uploading'" />
              <n-space justify="space-between"><span>{{ formatBytes(task.sent) }} / {{ formatBytes(task.total) }} · {{ formatSpeed(task.speed) }}</span>
                <n-space v-if="desktop">
                  <n-button v-if="task.status === 'uploading'" size="small" @click="control(task.id, 'pause')">暂停</n-button>
                  <n-button v-if="task.status === 'paused'" size="small" @click="control(task.id, 'resume')">继续</n-button>
                  <n-button v-if="task.status === 'uploading' || task.status === 'paused'" size="small" @click="control(task.id, 'cancel')">取消</n-button>
                </n-space>
              </n-space>
              <div v-if="task.error" class="error">{{ task.error }}</div>
            </div>
          </n-card>
        </n-space>
      </n-tab-pane>
      <n-tab-pane v-if="canAllocate" name="admin" :tab="isSuper ? '空间管理' : '下级空间分配'"><upload-space-admin :can-configure-pools="isSuper" /></n-tab-pane>
    </n-tabs>
  </div>
</template>

<style scoped>
.upload-page { padding: 16px; max-width: 1200px; margin: 0 auto; }
.queue-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 8px 0; }
.filename { overflow-wrap: anywhere; min-width: 0; }
.error { color: var(--n-error-color, #d03050); font-size: 12px; overflow-wrap: anywhere; }
.task { padding: 12px 0; border-bottom: 1px solid rgba(128,128,128,.15); }
</style>
