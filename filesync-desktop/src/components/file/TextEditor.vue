<script setup lang="ts">
// 文本文件在线查看 / 编辑（配置文件、TXT、日志、代码等）。
//
// 多人同时编辑：打开时记下文件的版本号；保存时带上它，服务端发现文件在此期间被别人改过就不覆盖，
// 把对方的最新内容还回来。这里拿「我打开时的内容 / 我现在的内容 / 对方的最新内容」做三方合并：
//   - 改的是不同位置 → 自动合并后直接保存（并提示去核对）；
//   - 改到了同一处   → 不自动决定，给出三个选择：手动合并（冲突处带标记）/ 用我的覆盖 / 用对方的版本。
// 内容里的换行统一成 \n、不含 BOM（服务端保存时会还原原文件的换行风格和 BOM）。
import { ref, computed, watch, nextTick, onMounted } from 'vue'
import { NButton, NSpace, NSpin, NAlert, NTag, NSwitch, NText, useMessage, useDialog } from 'naive-ui'
import { readTextFile, saveTextFile } from '@/api/file/fileApi'
import type { TextFileData, SaveTextResult } from '@/api/file/fileApi'
import { merge3, hasConflictMarkers } from '@/utils/merge3'

const props = defineProps<{
  /** 文件完整路径 */
  path: string
  name: string
}>()
const emit = defineEmits<{
  /** 有无未保存的修改，外层据此决定关闭/切换前要不要确认 */
  (e: 'dirty', v: boolean): void
}>()

const message = useMessage()
const dialog = useDialog()

const loading = ref(true)
const loadError = ref('')
const text = ref('') // 编辑框里的内容
const baseText = ref('') // 我这次编辑所基于的版本的内容（三方合并的 base）
const baseHash = ref('') // 对应的版本号，保存时交给服务端比对
const eol = ref<'lf' | 'crlf'>('lf')
const bom = ref(false)
const editing = ref(false)
const saving = ref(false)
const wrap = ref(false)
const textarea = ref<HTMLTextAreaElement | null>(null)

interface PendingConflict {
  theirs: TextFileData
  merged: string
  conflicts: number
  attempted: boolean
}
const conflict = ref<PendingConflict | null>(null)

const dirty = computed(() => editing.value && text.value !== baseText.value)
watch(dirty, (v) => emit('dirty', v), { immediate: true })

const lineCount = computed(() => text.value.split('\n').length)

function applySnapshot(d: TextFileData) {
  text.value = d.content
  baseText.value = d.content
  baseHash.value = d.hash
  eol.value = d.eol
  bom.value = d.bom
}

async function load() {
  loading.value = true
  loadError.value = ''
  conflict.value = null
  editing.value = false
  try {
    applySnapshot(await readTextFile(props.path, props.name))
  } catch (e) {
    loadError.value = String(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function startEdit() {
  editing.value = true
  nextTick(() => textarea.value?.focus())
}

function confirm(content: string, positive: string): Promise<boolean> {
  return new Promise((resolve) => {
    dialog.warning({
      title: '请确认',
      content,
      positiveText: positive,
      negativeText: '取消',
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false),
      onClose: () => resolve(false),
    })
  })
}

async function discard() {
  if (dirty.value && !(await confirm('放弃所有未保存的修改？', '放弃修改'))) return
  text.value = baseText.value
  conflict.value = null
  editing.value = false
}

function onSaved(res: SaveTextResult, autoMerged: boolean) {
  baseText.value = text.value
  baseHash.value = res.hash ?? baseHash.value
  conflict.value = null
  if (autoMerged) message.warning('他人在你编辑期间也改了此文件，已自动合并并保存，请核对合并结果')
  else if (res.unchanged) message.info('内容没有变化')
  else message.success(res.synced ? '已保存，并同步到各设备' : '已保存')
}

async function save(force = false) {
  if (saving.value) return
  if (!force && hasConflictMarkers(text.value)) {
    if (!(await confirm('文本里还有未解决的冲突标记（<<<<<<< / ======= / >>>>>>>），确定要保存吗？', '仍然保存'))) return
  }
  saving.value = true
  try {
    let autoMerged = false
    // 最多重试几次：自动合并后再保存的这一瞬间，也可能又有人改了
    for (let attempt = 0; attempt < 4; attempt++) {
      const res = await saveTextFile({
        path: props.path,
        name: props.name,
        content: text.value,
        ...(force ? { force: true } : { base_hash: baseHash.value }),
      })
      if (res.saved) return onSaved(res, autoMerged)
      if (!res.conflict || !res.current) return void message.error('保存失败')

      const theirs = res.current
      const m = merge3(baseText.value, text.value, theirs.content)
      if (m.attempted && m.conflicts === 0) {
        // 改的是不同位置：基准换成对方的最新版，用合并结果重新保存
        text.value = m.merged
        baseText.value = theirs.content
        baseHash.value = theirs.hash
        autoMerged = true
        continue
      }
      conflict.value = { theirs, merged: m.merged, conflicts: m.conflicts, attempted: m.attempted }
      return
    }
    message.warning('文件正在被频繁修改，请稍后再试')
  } catch (e) {
    message.error(`保存失败：${e}`)
  } finally {
    saving.value = false
  }
}

// ── 冲突的三种处理 ──
function manualMerge() {
  const c = conflict.value
  if (!c) return
  text.value = c.merged // 冲突处带 <<<<<<< / ======= / >>>>>>> 标记
  baseText.value = c.theirs.content
  baseHash.value = c.theirs.hash
  conflict.value = null
  nextTick(() => textarea.value?.focus())
}
async function overwriteWithMine() {
  if (!(await confirm('这会覆盖对方的修改，且无法自动找回（同步目录里的文件可以在版本历史里回滚）。确定覆盖？', '用我的覆盖'))) return
  conflict.value = null
  await save(true)
}
function takeTheirs() {
  const c = conflict.value
  if (!c) return
  applySnapshot(c.theirs)
  conflict.value = null
}

function onKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    if (editing.value && dirty.value) void save()
  }
}
</script>

<template>
  <div class="te">
    <div class="bar">
      <n-space align="center" size="small">
        <template v-if="!editing">
          <n-button size="small" type="primary" :disabled="loading || !!loadError" @click="startEdit">编辑</n-button>
          <n-button size="small" :disabled="loading" @click="load">重新加载</n-button>
        </template>
        <template v-else>
          <n-button size="small" type="primary" :loading="saving" :disabled="!dirty" @click="save()">保存 (Ctrl+S)</n-button>
          <n-button size="small" :disabled="saving" @click="discard">放弃修改</n-button>
          <n-tag v-if="dirty" size="small" type="warning" :bordered="false">未保存</n-tag>
        </template>
      </n-space>
      <n-space align="center" size="small">
        <n-text depth="3" style="font-size: 12px">自动换行</n-text>
        <n-switch v-model:value="wrap" size="small" />
        <n-tag size="small" :bordered="false">{{ eol === 'crlf' ? 'CRLF' : 'LF' }}</n-tag>
        <n-tag v-if="bom" size="small" :bordered="false">UTF-8 BOM</n-tag>
      </n-space>
    </div>

    <n-alert v-if="conflict" type="warning" title="文件已被他人修改" class="conflict">
      <template v-if="conflict.attempted">
        你和对方改到了同一处（{{ conflict.conflicts }} 处冲突），无法自动合并。
      </template>
      <template v-else>两个版本差异太大，无法自动合并。</template>
      <n-space style="margin-top: 8px" size="small">
        <n-button v-if="conflict.attempted" size="small" type="primary" @click="manualMerge">手动合并（冲突处带标记）</n-button>
        <n-button size="small" @click="overwriteWithMine">用我的覆盖</n-button>
        <n-button size="small" @click="takeTheirs">放弃我的，用对方的版本</n-button>
      </n-space>
    </n-alert>

    <div class="body">
      <n-spin v-if="loading" class="center" />
      <div v-else-if="loadError" class="center err">{{ loadError }}</div>
      <textarea
        v-else
        ref="textarea"
        v-model="text"
        class="editor"
        :class="{ wrap }"
        :readonly="!editing"
        spellcheck="false"
        @keydown="onKeydown"
      />
    </div>

    <div class="status">
      <n-text depth="3" style="font-size: 12px">
        共 {{ lineCount }} 行 · {{ text.length }} 字符 · {{ editing ? '编辑中' : '只读' }}
      </n-text>
    </div>
  </div>
</template>

<style scoped>
.te {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  background: #fff;
}
.bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 10px;
  border-bottom: 1px solid #eee;
}
.conflict {
  margin: 8px 10px 0;
}
.body {
  position: relative;
  flex: 1;
  min-height: 0;
}
.editor {
  width: 100%;
  height: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  border: none;
  outline: none;
  resize: none;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace;
  font-size: 13px;
  line-height: 1.55;
  tab-size: 4;
  white-space: pre; /* 默认不换行：配置文件、日志逐行看更清楚 */
  overflow: auto;
  color: #222;
  background: #fff;
}
.editor.wrap {
  white-space: pre-wrap;
  word-break: break-all;
}
.editor[readonly] {
  background: #fafafa;
}
.center {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.err {
  color: #d03050;
  padding: 20px;
  text-align: center;
}
.status {
  padding: 4px 12px;
  border-top: 1px solid #eee;
}
</style>
