<script setup lang="ts">
import { h, onMounted, ref } from "vue"
import { NButton, NSpace, NTag, useDialog, useMessage } from "naive-ui"
import type { DataTableColumns } from "naive-ui"
import { isTauri } from "@tauri-apps/api/core"
import { listShareLinks, revokeShareLink } from "@/api/file/fileApi"
import { getServerUrl } from "@/api/platform"
import { copyText } from "@/utils/clipboard"
import type { ShareLinkItem } from "@/api/file/fileTypes"

const message = useMessage()
const dialog = useDialog()

const list = ref<ShareLinkItem[]>([])
const loading = ref(false)
const total = ref(0)
const pageNum = ref(1)
const pageSize = ref(10)

const fetchList = async () => {
  loading.value = true
  try {
    const data = await listShareLinks(pageNum.value, pageSize.value)
    list.value = data.list || []
    total.value = data.total || 0
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

onMounted(fetchList)

const onPageChange = (p: number) => {
  pageNum.value = p
  fetchList()
}

const buildShareUrl = (row: ShareLinkItem) => {
  const base = isTauri() ? getServerUrl().replace(/\/+$/, "") : window.location.origin
  return base + "/v1/file/share-link/download/" + row.share_code
}

const handleCopy = async (row: ShareLinkItem) => {
  try {
    await copyText(buildShareUrl(row))
    message.success("链接已复制")
  } catch {
    message.error("复制失败")
  }
}

const handleRevoke = (row: ShareLinkItem) => {
  dialog.warning({
    title: "吊销分享链接",
    content: `确认吊销「${row.file_name}」的分享链接？吊销后该链接立即失效，此操作不可撤销。`,
    positiveText: "吊销",
    negativeText: "取消",
    onPositiveClick: async () => {
      try {
        await revokeShareLink(row.share_code)
        message.success("已吊销")
        fetchList()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

const formatTime = (isoString: string | null) => {
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

const statusInfo = (status: number): { text: string; type: "success" | "default" | "error" } => {
  if (status === 1) return { text: "有效", type: "success" }
  if (status === 2) return { text: "已吊销", type: "error" }
  return { text: "已过期", type: "default" }
}

const columns: DataTableColumns<ShareLinkItem> = [
  { title: "文件名", key: "file_name" },
  { title: "大小", key: "file_size", width: 100, render: (row) => formatSize(row.file_size) },
  { title: "创建时间", key: "created_at", width: 180, render: (row) => formatTime(row.created_at) },
  { title: "到期时间", key: "expire_time", width: 180, render: (row) => formatTime(row.expire_time) },
  {
    title: "状态", key: "status", width: 90,
    render(row) {
      const { text, type } = statusInfo(row.status)
      return h(NTag, { type, size: "small" }, { default: () => text })
    },
  },
  {
    title: "操作", key: "actions", width: 160,
    render(row) {
      const isActive = row.status === 1
      return h(NSpace, null, {
        default: () => [
          h(NButton, {
            size: "small", type: "info", ghost: true, disabled: !isActive,
            onClick: () => handleCopy(row),
          }, { default: () => "复制链接" }),
          h(NButton, {
            size: "small", type: "error", ghost: true, disabled: !isActive,
            onClick: () => handleRevoke(row),
          }, { default: () => "吊销" }),
        ],
      })
    },
  },
]
</script>

<template>
  <div class="share-manage-container">
    <div class="toolbar">
      <n-space align="center" justify="space-between">
        <div class="title">分享管理</div>
        <n-button size="small" @click="fetchList">刷新</n-button>
      </n-space>
    </div>

    <div class="table-wrapper">
      <n-data-table
          :columns="columns"
          :data="list"
          :loading="loading"
          :row-key="(row: ShareLinkItem) => row.id"
          :bordered="false"
          :striped="true"
          size="small"
      >
        <template #empty>
          <n-empty description="还没有创建过分享链接" />
        </template>
      </n-data-table>
    </div>

    <div class="pagination-wrapper">
      <n-pagination
          v-model:page="pageNum"
          :page-size="pageSize"
          :item-count="total"
          @update:page="onPageChange"
      />
    </div>
  </div>
</template>

<style scoped>
.share-manage-container {
  padding: 16px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  height: 100%;
  display: flex;
  flex-direction: column;
}
.toolbar { margin-bottom: 16px; padding-bottom: 12px; border-bottom: 1px solid #f0f0f0; }
.title { font-size: 16px; font-weight: bold; color: #303133; }
.table-wrapper { flex: 1; overflow: auto; }
.pagination-wrapper { margin-top: 16px; display: flex; justify-content: flex-end; }
</style>
