<script setup lang="ts">
import { watch, h, ref, computed } from "vue"
import { useRoute, useRouter } from "vue-router"
import { storeToRefs } from "pinia"
import { useCatalogStore } from "./composeables/useCatalogStore"
import { NDataTable, NButton, NSpace, NEmpty, NRadioGroup, NRadioButton, NInputNumber, NSelect, NModal, NCard, NInput, NAlert, NIcon, useMessage, useDialog } from "naive-ui"
import { Folder } from '@vicons/fa'
import type { DataTableColumns } from "naive-ui"
import type { FileItem } from "@/api/file/fileTypes"
import { deleteFile, downloadFile, createDirectory, createShareLink } from "@/api/file/fileApi"
import { isTauri } from "@tauri-apps/api/core"
import { open, save } from "@tauri-apps/plugin-dialog"
import { useTransferStore } from "@/store/useTransferStore"
import { getServerUrl } from "@/api/platform"
import { copyText } from "@/utils/clipboard"
import { previewKind } from "@/utils/fileKind"
import FileThumb from "@/components/file/FileThumb.vue"
import FileTypeIcon from "@/components/file/FileTypeIcon.vue"
import FilePreviewModal from "@/components/file/FilePreviewModal.vue"

const route = useRoute()
const router = useRouter()
const catalogStore = useCatalogStore()
const message = useMessage()
const dialog = useDialog()
const inTauri = isTauri()
const uploading = ref(false)
const webFileInput = ref<HTMLInputElement | null>(null)
const transferStore = useTransferStore()

const { currentPath, parentPath, items, loading, error: directoryError } = storeToRefs(catalogStore)

// URL 是目录导航的唯一入口，前进、后退与文件夹点击保持一致。
watch(() => route.query.path, path => {
  if (typeof path === 'string' && path) void catalogStore.fetchDirectory(path)
}, { immediate: true })

const refresh = () => {
  const target = typeof route.query.path === 'string' ? route.query.path : currentPath.value
  if (target) void catalogStore.fetchDirectory(target)
}

// 所有文件均在应用内打开；无法预览的类型给出说明，由用户明确选择下载。
const previewList = ref<FileItem[]>([])
const previewOpen = ref(false)
const previewIndex = ref(-1)
const previewItem = computed(() => previewList.value[previewIndex.value] ?? null)

const openPreview = (row: FileItem) => {
  // 预览序列固定在打开时，浏览历史改变不能替换正在编辑的文本。
  previewList.value = items.value.filter(i => !i.is_dir)
  const idx = previewList.value.findIndex((i) => i.path === row.path)
  if (idx < 0) return
  previewIndex.value = idx
  previewOpen.value = true
}

const handleRowClick = (row: FileItem) => {
  if (row.is_dir) {
    void router.push({ query: { ...route.query, path: row.path } })
  } else {
    openPreview(row)
  }
}

const handleGoUp = () => {
  const target = parentPath.value
  if (target) void router.push({ query: { ...route.query, path: target } })
}

const downloading = ref(new Set<string>())
// 下载必须明确选择；桌面端使用系统保存对话框并交给 Rust 流式处理。
const handleDownload = async (row: FileItem) => {
  if (row.is_dir) {
    message.warning("暂不支持直接下载整个文件夹")
    return
  }
  if (downloading.value.has(row.path)) return
  try {
    const localPath = inTauri ? await save({ title: '保存文件', defaultPath: row.name }) : undefined
    if (inTauri && !localPath) return
    downloading.value.add(row.path)
    if (inTauri) await transferStore.ensureMirror()
    await downloadFile(row.path, row.name, localPath || undefined)
    message.success(inTauri ? `已保存：${row.name}` : `已交给浏览器保存：${row.name}`)
  } catch (e) {
    message.error(String(e))
  } finally {
    downloading.value.delete(row.path)
  }
}

const showCreateFolder = ref(false)
const folderName = ref('')
const creatingFolder = ref(false)
const createParent = ref('')
function openCreateFolder() {
  createParent.value = currentPath.value
  folderName.value = ''
  showCreateFolder.value = true
}
async function submitFolder() {
  const name = folderName.value.trim()
  if (!name || creatingFolder.value) return
  creatingFolder.value = true
  try {
    await createDirectory(createParent.value, name)
    showCreateFolder.value = false
    message.success('文件夹已创建')
    if (currentPath.value === createParent.value) refresh()
  } catch (e) { message.error(String(e)) }
  finally { creatingFolder.value = false }
}

const handleDelete = (row: FileItem) => {
  const directory = currentPath.value
  dialog.warning({
    title: "删除文件",
    content: `确认删除「${row.name}」？此操作不可恢复。`,
    positiveText: "删除",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        await deleteFile(directory, row.name)
        message.success("已删除")
        refresh()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

// 创建分享链接：选择有效期（预设或自定义）→ 服务端硬链接/打包到 temp 目录 → 自动写入剪贴板
const expirePreset = ref(60)
const customValue = ref(1)
const customUnit = ref("小时")
const customMax = computed(() => (customUnit.value === "分钟" ? 43200 : customUnit.value === "小时" ? 720 : 30))

const computeExpireMinutes = (): number => {
  if (expirePreset.value !== -1) return expirePreset.value
  const factor = customUnit.value === "分钟" ? 1 : customUnit.value === "小时" ? 60 : 1440
  return customValue.value * factor
}

const handleCreateShareLink = (row: FileItem) => {
  const directory = currentPath.value
  expirePreset.value = 60
  dialog.warning({
    title: `创建分享链接：${row.name}`,
    content: () =>
      h(NSpace, { vertical: true }, {
        default: () => [
          h("div", { style: { fontSize: "13px", color: "#666" } }, "选择文件存在时间（到期后自动删除）"),
          h(NRadioGroup, { value: expirePreset.value, onUpdateValue: (v: number) => { expirePreset.value = v } }, {
            default: () =>
              h(NSpace, null, {
                default: () => [
                  h(NRadioButton, { value: 60 }, { default: () => "1 小时" }),
                  h(NRadioButton, { value: 1440 }, { default: () => "1 天" }),
                  h(NRadioButton, { value: 4320 }, { default: () => "3 天" }),
                  h(NRadioButton, { value: 10080 }, { default: () => "7 天" }),
                  h(NRadioButton, { value: 43200 }, { default: () => "30 天" }),
                  h(NRadioButton, { value: -1 }, { default: () => "自定义" }),
                ],
              }),
          }),
          expirePreset.value === -1
            ? h(NSpace, { align: "center" }, {
                default: () => [
                  h(NInputNumber, {
                    value: customValue.value,
                    min: 1,
                    max: customMax.value,
                    onUpdateValue: (v: number | null) => { if (v) customValue.value = v },
                  }),
                  h(NSelect, {
                    value: customUnit.value,
                    options: [
                      { label: "分钟", value: "分钟" },
                      { label: "小时", value: "小时" },
                      { label: "天", value: "天" },
                    ],
                    onUpdateValue: (v: string) => { customUnit.value = v },
                    style: { width: "90px" },
                  }),
                ],
              })
            : null,
        ],
      }),
    positiveText: "创建",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        const expireMinutes = computeExpireMinutes()
        if (expireMinutes <= 0) {
          message.warning("请填写有效的有效期")
          return
        }
        const data = await createShareLink(directory, row.name, expireMinutes)
        const base = isTauri() ? getServerUrl().replace(/\/+$/, "") : window.location.origin
        const url = base + data.url_path
        let copied = true
        try {
          await copyText(url)
        } catch {
          copied = false
        }
        dialog.success({
          title: copied ? "分享链接已创建（已写入剪贴板）" : "分享链接已创建",
          content: () =>
            h(NSpace, { vertical: true }, {
              default: () => [
                h("div", { style: { wordBreak: "break-all", fontSize: "13px" } }, url),
                copied
                  ? null
                  : h("div", { style: { fontSize: "12px", color: "#e88080" } }, "自动写入剪贴板失败，请手动复制"),
              ],
            }),
          positiveText: "复制链接",
          onPositiveClick: async () => {
            try {
              await copyText(url)
              message.success("已复制")
            } catch {
              message.error("复制失败，请选中上方链接手动复制")
            }
          },
        })
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

// 上传：Tauri 选本地文件（传绝对路径），Web 用文件输入
const handleUpload = async () => {
  if (!currentPath.value) {
    message.warning("请先进入一个目录再上传")
    return
  }
  if (inTauri) {
    const sel = await open({ multiple: true, directory: false })
    if (!sel) return
    const paths = Array.isArray(sel) ? sel : [sel]
    await doUpload(paths)
  } else {
    webFileInput.value?.click()
  }
}

const onWebFiles = async (e: Event) => {
  const files = (e.target as HTMLInputElement).files
  if (!files || files.length === 0) return
  await doUpload(Array.from(files))
  ;(e.target as HTMLInputElement).value = ""
}

const doUpload = async (entries: (string | File)[]) => {
  uploading.value = true
  // 并行上传：每个文件独立进度，store 聚合；任一失败不中断其它
  const results = await Promise.allSettled(
    entries.map((entry) => transferStore.startManualUpload(entry, currentPath.value))
  )
  const ok = results.filter((r) => r.status === "fulfilled").length
  const fail = results.length - ok
  uploading.value = false
  if (ok > 0) {
    message.success(`已上传 ${ok} 个文件${fail ? `,失败 ${fail} 个` : ""}`)
    refresh()
  } else if (fail > 0) {
    message.error(`上传失败 ${fail} 个文件`)
  }
}

const formatTime = (isoString: string) => {
  if (!isoString) return "-"
  return new Date(isoString).toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  })
}

const formatSize = (n: number) => {
  if (!n) return "-"
  const u = ["B", "KB", "MB", "GB", "TB"]
  let i = 0, v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`
}

const columns: DataTableColumns<FileItem> = [
  {
    title: "名称", key: "name", sorter: "default",
    render(row) {
      return h("div", {
        style: { display: "flex", alignItems: "center", gap: "8px", cursor: "pointer" },
        onClick: (event: MouseEvent) => { event.stopPropagation(); handleRowClick(row) },
      }, [
        // 图片/视频/有封面的音频显示缩略图，其余（含加载失败）仍是图标
        row.is_dir
          ? h(NIcon, {size: 22, color: '#f0a020'}, {default: () => h(Folder)})
          : h(FileThumb, { path: row.path, name: row.name, size: row.size, boxSize: 32 }, {
              default: () => h(FileTypeIcon, { name: row.name, size: 22 }),
            }),
        h(NButton, {text: true, style: {fontWeight: row.is_dir ? 'bold' : 'normal'}, onClick: (event: MouseEvent) => {event.stopPropagation();handleRowClick(row)}}, {default: () => row.name}),
      ])
    },
  },
  { title: "大小", key: "size", width: 110, render: (row) => (row.is_dir ? "-" : formatSize(row.size)) },
  { title: "修改时间", key: "mod_time", width: 180, render: (row) => formatTime(row.mod_time) },
  { title: "包含项", key: "children_count", width: 90, render: (row) => (row.is_dir ? `${row.children_count} 项` : "-") },
  {
    title: "操作", key: "actions", width: 260,
    render(row) {
      if (row.is_dir) {
        return h(NSpace, { size: 4 }, {
          default: () => [
            h(NButton, { size: 'small', onClick: (e: MouseEvent) => {e.stopPropagation();handleRowClick(row)} }, {default: () => '打开'}),
            h(NButton, { size: "small", type: "info", ghost: true, onClick: (e: MouseEvent) => { e.stopPropagation(); handleCreateShareLink(row) } }, { default: () => "分享" }),
          ],
        })
      }
      return h(NSpace, { size: 4 }, {
        default: () => [
          previewKind(row.name) !== "none"
            ? h(NButton, { size: "small", ghost: true, onClick: (e: MouseEvent) => { e.stopPropagation(); openPreview(row) } }, { default: () => "预览" })
            : null,
          h(NButton, { size: "small", type: "primary", ghost: true, loading: downloading.value.has(row.path), onClick: (e: MouseEvent) => { e.stopPropagation(); handleDownload(row) } }, { default: () => "下载" }),
          h(NButton, { size: "small", type: "info", ghost: true, onClick: (e: MouseEvent) => { e.stopPropagation(); handleCreateShareLink(row) } }, { default: () => "分享" }),
          h(NButton, { size: "small", type: "error", ghost: true, onClick: (e: MouseEvent) => { e.stopPropagation(); handleDelete(row) } }, { default: () => "删除" }),
        ],
      })
    },
  },
]
</script>

<template>
  <div class="catalog-container">
    <div class="toolbar">
      <n-space align="center" justify="space-between">
        <n-space align="center">
          <n-button size="small" :disabled="!currentPath || loading || !!directoryError" @click="openCreateFolder">新建文件夹</n-button>
          <n-button v-if="parentPath" key="go-up" type="primary" ghost size="small" @click="handleGoUp">⬆ 返回上一级</n-button>
          <div class="current-path">
            <span class="path-label">当前路径：</span>
            <span class="path-value">{{ currentPath || "请从「文件列表」选择存储空间进入" }}</span>
          </div>
        </n-space>
        <n-space align="center">
          <n-button size="small" :disabled="!currentPath" :loading="uploading" type="primary" @click="handleUpload">上传文件</n-button>
          <n-button size="small" :disabled="!currentPath" @click="refresh">刷新</n-button>
        </n-space>
      </n-space>
    </div>

    <n-alert v-if="directoryError" type="error" title="读取目录失败" style="margin-bottom: 12px">{{ directoryError }}</n-alert>
    <div class="table-wrapper">
      <n-data-table
          :columns="columns"
          :data="items"
          :loading="loading"
          :row-key="(row) => row.path"
          :row-props="(row: FileItem) => ({style: {cursor: 'pointer'}, onClick: () => handleRowClick(row)})"
          :bordered="false"
          :striped="true"
          size="small"
          :max-height="'calc(100vh - 220px)'"
      >
        <template #empty>
          <n-empty description="此文件夹为空" />
        </template>
      </n-data-table>
    </div>

    <file-preview-modal
        v-model:show="previewOpen"
        :item="previewItem"
        :has-prev="previewIndex > 0"
        :has-next="previewIndex >= 0 && previewIndex < previewList.length - 1"
        @prev="previewIndex--"
        @next="previewIndex++"
        @download="previewItem && handleDownload(previewItem)"
    />

    <n-modal v-model:show="showCreateFolder" :mask-closable="!creatingFolder" :close-on-esc="!creatingFolder">
      <n-card title="新建文件夹" style="width: min(460px, 90vw)" :closable="!creatingFolder" @close="showCreateFolder = false">
        <n-space vertical>
          <span>创建位置：{{ createParent }}</span>
          <n-input v-model:value="folderName" placeholder="请输入文件夹名称" :disabled="creatingFolder" maxlength="255" autofocus @keyup.enter="submitFolder" />
        </n-space>
        <template #footer><n-space justify="end"><n-button :disabled="creatingFolder" @click="showCreateFolder = false">取消</n-button><n-button type="primary" :disabled="!folderName.trim()" :loading="creatingFolder" @click="submitFolder">创建</n-button></n-space></template>
      </n-card>
    </n-modal>

    <!-- Web 模式上传输入 -->
    <input ref="webFileInput" type="file" multiple style="display:none" @change="onWebFiles" />
  </div>
</template>

<style scoped>
.catalog-container {
  padding: 16px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  height: 100%;
  display: flex;
  flex-direction: column;
}
.toolbar { margin-bottom: 16px; padding-bottom: 12px; border-bottom: 1px solid #f0f0f0; }
.current-path { font-size: 14px; padding: 4px 8px; background-color: #f5f7fa; border-radius: 4px; }
.path-label { color: #909399; }
.path-value { color: #303133; font-family: monospace; font-weight: bold; }
.table-wrapper { flex: 1; overflow: hidden; }
:deep(.n-data-table-tr:hover) { background-color: #f0f7ff !important; }
</style>
