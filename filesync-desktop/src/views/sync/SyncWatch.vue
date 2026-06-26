<script setup lang="ts">
import {ref, onMounted, onUnmounted} from "vue"
import {useMessage} from "naive-ui"
import {invoke, isTauri} from "@tauri-apps/api/core"
import {listen, type UnlistenFn} from "@tauri-apps/api/event"
import {open} from "@tauri-apps/plugin-dialog"
import {NButton, NInput, NSpace, NTag, NList, NListItem, NScrollbar, NEmpty, NCard} from "naive-ui"

interface FileChangeEvent {
  paths: string[]
  kind: string
}

interface EventLog {
  id: number
  time: string
  kind: string
  paths: string[]
}

const message = useMessage()
const inTauri = isTauri()
const watchedPaths = ref<string[]>([])
const manualPath = ref("")
const eventLogs = ref<EventLog[]>([])
const logCounter = ref(0)
const MAX_LOGS = 200

let unlistenChange: UnlistenFn | null = null
let unlistenError: UnlistenFn | null = null

const kindColor = (kind: string): "success" | "warning" | "error" | "info" | "default" => {
  switch (kind) {
    case "create": return "success"
    case "modify": return "warning"
    case "remove": return "error"
    case "access": return "info"
    default: return "default"
  }
}

const kindLabel = (kind: string) => {
  switch (kind) {
    case "create": return "新建"
    case "modify": return "修改"
    case "remove": return "删除"
    case "access": return "访问"
    default: return kind
  }
}

async function refreshList() {
  if (!inTauri) return
  watchedPaths.value = await invoke<string[]>("list_watches")
}

async function pickFolder() {
  if (!inTauri) {
    message.warning("选择目录功能仅在桌面端（Tauri）可用")
    return
  }
  const selected = await open({directory: true, multiple: false})
  if (selected && typeof selected === "string") {
    await addWatch(selected)
  }
}

async function addWatchManual() {
  const p = manualPath.value.trim()
  if (!p) return
  await addWatch(p)
  manualPath.value = ""
}

async function addWatch(path: string) {
  if (!inTauri) {
    message.warning("监听功能仅在桌面端（Tauri）可用")
    return
  }
  try {
    await invoke("add_watch", {path})
    await refreshList()
  } catch (e) {
    message.error(String(e))
  }
}

async function removeWatch(path: string) {
  if (!inTauri) return
  try {
    await invoke("remove_watch", {path})
    await refreshList()
  } catch (e) {
    message.error(String(e))
  }
}

function clearLogs() {
  eventLogs.value = []
}

onMounted(async () => {
  await refreshList()
  if (!inTauri) return

  unlistenChange = await listen<FileChangeEvent>("file-change", (event) => {
    const entry: EventLog = {
      id: logCounter.value++,
      time: new Date().toLocaleTimeString(),
      kind: event.payload.kind,
      paths: event.payload.paths,
    }
    eventLogs.value.unshift(entry)
    if (eventLogs.value.length > MAX_LOGS) {
      eventLogs.value = eventLogs.value.slice(0, MAX_LOGS)
    }
  })

  unlistenError = await listen<string>("watch-error", (event) => {
    console.error("Watch error:", event.payload)
  })
})

onUnmounted(() => {
  unlistenChange?.()
  unlistenError?.()
})
</script>

<template>
  <div class="sync-watch">
    <n-card title="目录实时监听" size="small" class="top-card">
      <n-space vertical>
        <n-space align="center">
          <n-input
              v-model:value="manualPath"
              placeholder="手动输入目录路径"
              style="width: 400px"
              @keyup.enter="addWatchManual"
          />
          <n-button type="primary" @click="addWatchManual">添加</n-button>
          <n-button @click="pickFolder" :disabled="!inTauri" :title="inTauri ? '' : '仅桌面端可用'">选择目录</n-button>
        </n-space>

        <div v-if="watchedPaths.length > 0">
          <div class="section-label">监听中的目录</div>
          <n-list bordered size="small">
            <n-list-item v-for="p in watchedPaths" :key="p">
              <n-space justify="space-between" style="width: 100%">
                <span class="path-text">{{ p }}</span>
                <n-button size="tiny" type="error" @click="removeWatch(p)">移除</n-button>
              </n-space>
            </n-list-item>
          </n-list>
        </div>
        <n-empty v-else description="暂无监听目录" size="small"/>
      </n-space>
    </n-card>

    <n-card size="small" class="log-card">
      <template #header>
        <n-space justify="space-between" style="width: 100%">
          <span>实时事件流 <span class="log-count">({{ eventLogs.length }})</span></span>
          <n-button size="tiny" @click="clearLogs">清空</n-button>
        </n-space>
      </template>

      <n-scrollbar style="max-height: 420px">
        <n-empty v-if="eventLogs.length === 0" description="等待文件变化..." style="padding: 40px 0"/>
        <div v-else class="log-list">
          <div v-for="log in eventLogs" :key="log.id" class="log-item">
            <span class="log-time">{{ log.time }}</span>
            <n-tag :type="kindColor(log.kind)" size="small" class="log-kind">{{ kindLabel(log.kind) }}</n-tag>
            <span class="log-path">{{ log.paths.join(", ") }}</span>
          </div>
        </div>
      </n-scrollbar>
    </n-card>
  </div>
</template>

<style scoped>
.sync-watch {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.top-card, .log-card {
  background: white;
  border-radius: 8px;
}

.section-label {
  font-size: 13px;
  color: #666;
  margin-bottom: 6px;
}

.path-text {
  font-family: monospace;
  font-size: 13px;
  color: #333;
}

.log-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.log-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.5;
}

.log-item:hover {
  background: #f5f5f5;
}

.log-time {
  color: #999;
  white-space: nowrap;
  min-width: 80px;
}

.log-kind {
  min-width: 40px;
  text-align: center;
}

.log-path {
  font-family: monospace;
  color: #333;
  word-break: break-all;
}

.log-count {
  font-size: 12px;
  color: #999;
}
</style>
