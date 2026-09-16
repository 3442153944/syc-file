<script setup lang="ts">
// 专用日志窗口视图：监听 Rust 端 `app-log` 事件，实时滚动显示同步/连接/任务日志。
// 由 App.vue 在窗口 label === 'logs' 时全屏渲染。
//
// 内容包含前后端两部分：Rust 自己的日志，以及前端 console.* 经 consoleBridge 转发来的
// （source 为 web / web/<窗口名>）。打开窗口时先从 Rust 取最近的日志补齐，否则窗口打开
// 之前发生的事一条都看不到。
import {ref, computed, onMounted, onBeforeUnmount, nextTick} from 'vue'
import {invoke} from '@tauri-apps/api/core'
import {listen, type UnlistenFn} from '@tauri-apps/api/event'

interface LogLine {
  /** Rust 端递增序号，用于补齐历史时与实时事件去重（本窗口自己插入的提示行没有） */
  seq?: number
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

/** 来源筛选：前端 console 转发来的日志 source 以 web 开头 */
const originFilter = ref<'all' | 'rust' | 'web'>('all')

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return lines.value.filter((l) => {
    if (levelFilter.value !== 'ALL' && l.level !== levelFilter.value) return false
    if (originFilter.value !== 'all') {
      const isWeb = l.source === 'web' || l.source.startsWith('web/')
      if ((originFilter.value === 'web') !== isWeb) return false
    }
    return !(kw && !(`${l.source} ${l.message}`.toLowerCase().includes(kw)));

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
  // 先挂监听再取历史：反过来的话，两步之间产生的日志两边都拿不到。
  // 代价是同一条可能两边都拿到，用 seq 去重。
  unlisten = await listen<LogLine>('app-log', (e) => {
    append(e.payload)
  })
  try {
    const history = await invoke<LogLine[]>('get_recent_logs')
    const seen = new Set(lines.value.map((l) => l.seq).filter((v) => v !== undefined))
    const missing = history.filter((l) => l.seq === undefined || !seen.has(l.seq))
    lines.value = [...missing, ...lines.value]
        .sort((a, b) => (a.seq ?? Number.MAX_SAFE_INTEGER) - (b.seq ?? Number.MAX_SAFE_INTEGER))
        .slice(-MAX_LINES)
  } catch {
    // 取不到历史不影响实时日志
  }
  await append({ts: Date.now(), level: 'INFO', source: 'logviewer', message: '日志窗口已就绪，以上为打开前的最近日志'})
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
      <select v-model="originFilter" class="ctl">
        <option value="all">全部来源</option>
        <option value="rust">后台</option>
        <option value="web">前端</option>
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
