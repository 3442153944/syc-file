<script setup lang="ts">
// 专用日志窗口视图：监听 Rust 端 `app-log` 事件，实时滚动显示同步/连接/任务日志。
// 由 App.vue 在窗口 label === 'logs' 时全屏渲染。
import {ref, computed, onMounted, onBeforeUnmount, nextTick} from 'vue'
import {listen, type UnlistenFn} from '@tauri-apps/api/event'

interface LogLine {
  ts: number
  level: string
  source: string
  message: string
}

const MAX_LINES = 5000
const lines = ref<LogLine[]>([])
const autoScroll = ref(true)
const levelFilter = ref<string>('ALL')
const keyword = ref('')
const scroller = ref<HTMLElement | null>(null)
let unlisten: UnlistenFn | null = null

const levels = ['ALL', 'DEBUG', 'INFO', 'WARN', 'ERROR']

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return lines.value.filter((l) => {
    if (levelFilter.value !== 'ALL' && l.level !== levelFilter.value) return false
    if (kw && !(`${l.source} ${l.message}`.toLowerCase().includes(kw))) return false
    return true
  })
})

function fmtTime(ts: number): string {
  const d = new Date(ts)
  const p = (n: number, w = 2) => String(n).padStart(w, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`
}

function levelClass(level: string): string {
  return `lvl lvl-${level.toLowerCase()}`
}

async function append(line: LogLine) {
  lines.value.push(line)
  if (lines.value.length > MAX_LINES) {
    lines.value.splice(0, lines.value.length - MAX_LINES)
  }
  if (autoScroll.value) {
    await nextTick()
    const el = scroller.value
    if (el) el.scrollTop = el.scrollHeight
  }
}

function clearLogs() {
  lines.value = []
}

onMounted(async () => {
  unlisten = await listen<LogLine>('app-log', (e) => {
    append(e.payload)
  })
  await append({ts: Date.now(), level: 'INFO', source: 'logviewer', message: '日志窗口已就绪，开始接收日志流…'})
})

onBeforeUnmount(() => {
  if (unlisten) unlisten()
})
</script>

<template>
  <div class="logwin">
    <div class="toolbar">
      <select v-model="levelFilter" class="ctl">
        <option v-for="l in levels" :key="l" :value="l">{{ l }}</option>
      </select>
      <input v-model="keyword" class="ctl kw" placeholder="过滤关键字 / 来源"/>
      <label class="ctl chk"><input type="checkbox" v-model="autoScroll"/> 自动滚动</label>
      <span class="spacer"/>
      <span class="count">{{ filtered.length }} / {{ lines.length }}</span>
      <button class="ctl btn" @click="clearLogs">清空</button>
    </div>

    <div ref="scroller" class="stream">
      <div v-for="(l, i) in filtered" :key="i" class="row">
        <span class="time">{{ fmtTime(l.ts) }}</span>
        <span :class="levelClass(l.level)">{{ l.level }}</span>
        <span class="src">{{ l.source }}</span>
        <span class="msg">{{ l.message }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.logwin {
  height: 100vh;
  width: 100vw;
  display: flex;
  flex-direction: column;
  background: #1e1e1e;
  color: #d4d4d4;
  font-family: Consolas, 'Courier New', monospace;
  font-size: 12px;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  background: #252526;
  border-bottom: 1px solid #333;
}

.ctl {
  background: #3c3c3c;
  color: #d4d4d4;
  border: 1px solid #555;
  border-radius: 3px;
  padding: 2px 6px;
  font-size: 12px;
}

.kw {
  width: 220px;
}

.chk {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
}

.btn {
  cursor: pointer;
}

.btn:hover {
  background: #094771;
}

.spacer {
  flex: 1;
}

.count {
  color: #888;
}

.stream {
  flex: 1;
  overflow-y: auto;
  padding: 4px 8px;
}

.row {
  display: flex;
  gap: 8px;
  white-space: pre-wrap;
  word-break: break-all;
  line-height: 1.5;
}

.time {
  color: #6a9955;
  flex-shrink: 0;
}

.lvl {
  flex-shrink: 0;
  width: 46px;
  font-weight: bold;
}

.lvl-debug {
  color: #888;
}

.lvl-info {
  color: #4fc1ff;
}

.lvl-warn {
  color: #dcdcaa;
}

.lvl-error {
  color: #f48771;
}

.src {
  color: #c586c0;
  flex-shrink: 0;
  min-width: 90px;
}

.msg {
  color: #d4d4d4;
  flex: 1;
}
</style>
