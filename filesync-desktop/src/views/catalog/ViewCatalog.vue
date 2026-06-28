<script setup lang="ts">
import { onMounted, h, ref } from "vue"
import { useRoute, useRouter } from "vue-router"
import { storeToRefs } from "pinia"
import { useCatalogStore } from "./composeables/useCatalogStore"
import { NDataTable, NButton, NSpace, NEmpty, useMessage, useDialog } from "naive-ui"
import type { DataTableColumns } from "naive-ui"
import type { FileItem } from "@/api/file/fileTypes"
import { uploadFile, deleteFile, buildDownloadUrl } from "@/api/file/fileApi"
import { isTauri } from "@tauri-apps/api/core"
import { open } from "@tauri-apps/plugin-dialog"

const route = useRoute()
const router = useRouter()
const catalogStore = useCatalogStore()
const message = useMessage()
const dialog = useDialog()
const inTauri = isTauri()
const uploading = ref(false)
const webFileInput = ref<HTMLInputElement | null>(null)

const { currentPath, parentPath, items, loading } = storeToRefs(catalogStore)

onMounted(() => {
  const initPath = route.query.path as string
  if (initPath) catalogStore.fetchDirectory(initPath)
})

const refresh = () => {
  if (currentPath.value) catalogStore.fetchDirectory(currentPath.value)
}

const handleRowClick = (row: FileItem) => {
  if (row.is_dir) {
    catalogStore.fetchDirectory(row.path)
    router.push({ query: { path: row.path } })
  } else {
    handleDownload(row)
  }
}

const handleGoUp = () => {
  catalogStore.goParent()
  router.push({ query: { path: parentPath.value } })
}

// 下载：用后端真实下载地址（带 token 的 query），动态 a 标签触发
const handleDownload = async (row: FileItem) => {
  if (row.is_dir) {
    message.warning("暂不支持直接下载整个文件夹")
    return
  }
  try {
    const url = await buildDownloadUrl(currentPath.value, row.name, "")
    const a = document.createElement("a")
    a.href = url
    a.download = row.name
    a.target = "_blank"
    a.style.display = "none"
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    message.success(`已开始下载: ${row.name}`)
  } catch (e) {
    message.error(String(e))
  }
}

const handleDelete = (row: FileItem) => {
  dialog.warning({
    title: "删除文件",
    content: `确认删除「${row.name}」？此操作不可恢复。`,
    positiveText: "删除",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        await deleteFile(currentPath.value, row.name)
        message.success("已删除")
        refresh()
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
  let ok = 0
  for (const entry of entries) {
    const label = typeof entry === "string" ? entry.split(/[\\/]/).pop() : entry.name
    try {
      await uploadFile(entry, currentPath.value)
      ok++
    } catch (err) {
      message.error(`上传失败 ${label}: ${String(err)}`)
    }
  }
  uploading.value = false
  if (ok > 0) {
    message.success(`已上传 ${ok} 个文件`)
    refresh()
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
        onClick: () => handleRowClick(row),
      }, [
        h("span", { style: { fontSize: "18px" } }, row.is_dir ? "📁" : "📄"),
        h("span", { style: { fontWeight: row.is_dir ? "bold" : "normal" } }, row.name),
      ])
    },
  },
  { title: "大小", key: "size", width: 110, render: (row) => (row.is_dir ? "-" : formatSize(row.size)) },
  { title: "修改时间", key: "mod_time", width: 180, render: (row) => formatTime(row.mod_time) },
  { title: "包含项", key: "children_count", width: 90, render: (row) => (row.is_dir ? `${row.children_count} 项` : "-") },
  {
    title: "操作", key: "actions", width: 140,
    render(row) {
      if (row.is_dir) return "-"
      return h(NSpace, { size: 4 }, {
        default: () => [
          h(NButton, { size: "small", type: "primary", ghost: true, onClick: (e: MouseEvent) => { e.stopPropagation(); handleDownload(row) } }, { default: () => "下载" }),
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
          <n-button v-if="parentPath" type="primary" ghost size="small" @click="handleGoUp">⬆ 返回上一级</n-button>
          <div class="current-path">
            <span class="path-label">当前路径：</span>
            <span class="path-value">{{ currentPath || "请从「文件列表」选择磁盘进入" }}</span>
          </div>
        </n-space>
        <n-space align="center">
          <n-button size="small" :disabled="!currentPath" :loading="uploading" type="primary" @click="handleUpload">上传文件</n-button>
          <n-button size="small" :disabled="!currentPath" @click="refresh">刷新</n-button>
        </n-space>
      </n-space>
    </div>

    <div class="table-wrapper">
      <n-data-table
          :columns="columns"
          :data="items"
          :loading="loading"
          :row-key="(row) => row.path"
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
