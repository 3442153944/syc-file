<script setup lang="ts">
// 文件在线预览：图片（滚轮缩放 / 拖动 / 双击放大）、视频、音频、文本（查看 + 在线编辑，见 TextEditor）。
// 图片打开时先垫一张缩略图（列表里已缓存，几乎立刻出来），原图加载完再替换；
// 视频用封面当海报、音频显示专辑封面。←/→ 切换同目录里上一个/下一个可预览的文件。
// 不支持预览的类型由调用方走下载，这里只在被误开时给个提示。
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { NModal, NButton, NSpace, NSpin, NEmpty, NText, useDialog } from 'naive-ui'
import type { FileItem } from '@/api/file/fileTypes'
import { buildDownloadUrl, buildThumbnailUrl } from '@/api/file/fileApi'
import { previewKind } from '@/utils/fileKind'
import TextEditor from './TextEditor.vue'

const props = defineProps<{
  show: boolean
  item: FileItem | null
  hasPrev: boolean
  hasNext: boolean
}>()
const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  (e: 'prev'): void
  (e: 'next'): void
  (e: 'download'): void
}>()

const kind = computed(() => (props.item ? previewKind(props.item.name) : 'none'))

// 文本编辑中有未保存的修改时，关闭弹窗、切换上一个/下一个之前先确认，免得一个手滑丢掉一大段修改
const dialog = useDialog()
const textDirty = ref(false)
function guarded(action: () => void) {
  if (!textDirty.value) return action()
  dialog.warning({
    title: '有未保存的修改',
    content: '现在离开，这些修改会丢失。确定离开吗？',
    positiveText: '放弃修改并离开',
    negativeText: '继续编辑',
    onPositiveClick: () => {
      textDirty.value = false
      action()
    },
  })
}
function onUpdateShow(v: boolean) {
  if (v) emit('update:show', true)
  else guarded(() => emit('update:show', false))
}

const url = ref('') // 原文件（下载地址，支持 Range，所以视频/音频能拖动进度）
const thumbUrl = ref('') // 缩略图：图片的占位 / 视频海报 / 音频封面
const loaded = ref(false)
const failed = ref(false)
const coverFailed = ref(false)

// 图片缩放：1 = 适应窗口；放大后可拖动
const scale = ref(1)
const tx = ref(0)
const ty = ref(0)
let dragging = false
let lastX = 0
let lastY = 0

function resetView() {
  scale.value = 1
  tx.value = 0
  ty.value = 0
}

watch(
  () => [props.show, props.item?.path] as const,
  async ([show]) => {
    url.value = ''
    thumbUrl.value = ''
    loaded.value = false
    failed.value = false
    coverFailed.value = false
    resetView()
    textDirty.value = false
    const it = props.item
    // 文本由 TextEditor 自己通过接口读取，不需要下载地址
    if (!show || !it || kind.value === 'none' || kind.value === 'text') return
    try {
      // it.path 已是完整路径（末段即文件名），服务端与「目录 + 文件名」同样识别
      url.value = await buildDownloadUrl(it.path, it.name, '')
      // 256 档与列表一致，服务端浏览时已预生成，命中即取
      thumbUrl.value = await buildThumbnailUrl(it.path, it.name, 256, it.size)
    } catch {
      failed.value = true
    }
  },
  { immediate: true },
)

function setScale(s: number) {
  scale.value = Math.min(8, Math.max(1, s))
  if (scale.value === 1) {
    tx.value = 0
    ty.value = 0
  }
}
function onWheel(e: WheelEvent) {
  setScale(scale.value * (e.deltaY < 0 ? 1.15 : 1 / 1.15))
}
function onDblClick() {
  if (scale.value > 1) resetView()
  else scale.value = 2.5
}
function onDown(e: MouseEvent) {
  if (scale.value <= 1) return
  dragging = true
  lastX = e.clientX
  lastY = e.clientY
}
function onMove(e: MouseEvent) {
  if (!dragging) return
  tx.value += e.clientX - lastX
  ty.value += e.clientY - lastY
  lastX = e.clientX
  lastY = e.clientY
}
function onUp() {
  dragging = false
}

function onKey(e: KeyboardEvent) {
  // 在输入框/文本框里按方向键是移动光标，不能当成切换文件
  const el = e.target as HTMLElement | null
  if (el && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable)) return
  if (e.key === 'ArrowLeft' && props.hasPrev) guarded(() => emit('prev'))
  else if (e.key === 'ArrowRight' && props.hasNext) guarded(() => emit('next'))
}
watch(
  () => props.show,
  (show) => {
    if (show) window.addEventListener('keydown', onKey)
    else window.removeEventListener('keydown', onKey)
  },
  { immediate: true },
)
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

const imgStyle = computed(() => ({
  transform: `translate(${tx.value}px, ${ty.value}px) scale(${scale.value})`,
  cursor: scale.value > 1 ? 'grab' : 'zoom-in',
}))
</script>

<template>
  <n-modal
    :show="show"
    preset="card"
    :bordered="false"
    :title="item?.name"
    :style="{ width: 'min(94vw, 1100px)' }"
    @update:show="onUpdateShow"
  >
    <template #header-extra>
      <n-space size="small">
        <n-button size="small" :disabled="!hasPrev" @click="guarded(() => emit('prev'))">上一个</n-button>
        <n-button size="small" :disabled="!hasNext" @click="guarded(() => emit('next'))">下一个</n-button>
        <n-button size="small" type="primary" @click="emit('download')">下载</n-button>
      </n-space>
    </template>

    <div class="stage" :class="{ text: kind === 'text' }" @mousemove="onMove" @mouseup="onUp" @mouseleave="onUp">
      <n-empty v-if="failed" description="预览失败，可以下载后查看" />

      <template v-else-if="kind === 'image'">
        <img v-if="thumbUrl && !loaded" class="ph" :src="thumbUrl" alt="" draggable="false" />
        <img
          v-if="url"
          class="main"
          :class="{ pending: !loaded }"
          :src="url"
          :alt="item?.name"
          :style="imgStyle"
          draggable="false"
          @load="loaded = true"
          @error="failed = true"
          @wheel.prevent="onWheel"
          @dblclick="onDblClick"
          @mousedown.prevent="onDown"
        />
        <n-spin v-if="!loaded && !thumbUrl" class="center" />
      </template>

      <video
        v-else-if="kind === 'video' && url"
        class="media"
        :src="url"
        :poster="thumbUrl"
        controls
        preload="metadata"
        @error="failed = true"
      />

      <div v-else-if="kind === 'audio' && url" class="audio-box">
        <img v-if="thumbUrl && !coverFailed" class="cover" :src="thumbUrl" alt="" @error="coverFailed = true" />
        <audio class="audio" :src="url" controls preload="metadata" @error="failed = true" />
      </div>

      <text-editor
        v-else-if="kind === 'text' && item"
        :key="item.path"
        :path="item.path"
        :name="item.name"
        @dirty="textDirty = $event"
      />

      <n-empty v-else-if="kind === 'none'" description="暂不支持预览该类型，请下载后查看" />
      <n-spin v-else class="center" />
    </div>

    <template #footer>
      <n-text depth="3" style="font-size: 12px">
        <template v-if="kind === 'text'">Ctrl+S 保存 · 他人同时修改时，改动位置不重叠会自动合并</template>
        <template v-else>
          <template v-if="kind === 'image'">滚轮缩放 · 双击放大/还原 · 放大后可拖动 · </template>
          ← → 切换上一个/下一个
        </template>
      </n-text>
    </template>
  </n-modal>
</template>

<style scoped>
.stage {
  position: relative;
  height: 70vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #111;
  border-radius: 6px;
  overflow: hidden;
  user-select: none;
}
.stage.text {
  background: #fff;
  align-items: stretch;
  justify-content: stretch;
  user-select: text;
}
.stage :deep(.n-empty) {
  color: #bbb;
}
.ph,
.main {
  position: absolute;
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}
.ph {
  filter: blur(2px);
}
.main {
  transition: transform 0.05s linear;
}
.main.pending {
  opacity: 0;
}
.media {
  max-width: 100%;
  max-height: 100%;
  background: #000;
}
.audio-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20px;
  padding: 20px;
}
.cover {
  max-width: min(60vh, 100%);
  max-height: 45vh;
  border-radius: 8px;
  object-fit: contain;
}
.audio {
  width: min(520px, 90%);
}
.center {
  position: absolute;
}
</style>
