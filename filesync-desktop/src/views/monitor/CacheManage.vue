<script setup lang="ts">
// 缓存管理（管理员）：跨用户查看粘贴快传缓存，可以按用户名过滤，手动清理不用等自动过期
import {h, onMounted, ref} from 'vue'
import {NButton, NSpace, NTag, NInput, useDialog, useMessage} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {listAdminQuickShare, revokeAdminQuickShare} from '@/api/admin/quickShareAdminApi'
import type {AdminQuickShareItem} from '@/api/admin/quickShareAdminApi'

const message = useMessage()
const dialog = useDialog()

const list = ref<AdminQuickShareItem[]>([])
const loading = ref(false)
const total = ref(0)
const pageNum = ref(1)
const pageSize = ref(20)
const username = ref('')

const fetchList = async () => {
  loading.value = true
  try {
    const data = await listAdminQuickShare(pageNum.value, pageSize.value, username.value.trim() || undefined)
    list.value = data.list || []
    total.value = data.total || 0
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

onMounted(fetchList)

const onSearch = () => {
  pageNum.value = 1
  fetchList()
}

const onPageChange = (p: number) => {
  pageNum.value = p
  fetchList()
}

const handleRevoke = (row: AdminQuickShareItem) => {
  dialog.warning({
    title: '清理缓存',
    content: `确认清理「${row.username}」的「${row.file_name}」？清理后立即失效，此操作不可撤销。`,
    positiveText: '清理',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await revokeAdminQuickShare(row.share_code)
        message.success('已清理')
        fetchList()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

const formatTime = (isoString: string) => {
  if (!isoString) return '-'
  return new Date(isoString).toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

const formatSize = (n: number) => {
  if (!n) return '-'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0, v = n
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`
}

const columns: DataTableColumns<AdminQuickShareItem> = [
  {title: '用户', key: 'username', width: 120},
  {title: '文件名', key: 'file_name'},
  {title: '大小', key: 'file_size', width: 100, render: (row) => formatSize(row.file_size)},
  {
    title: '存储方式', key: 'storage_kind', width: 90,
    render(row) {
      return h(NTag, {type: row.storage_kind === 'memory' ? 'info' : 'default', size: 'small'},
          {default: () => (row.storage_kind === 'memory' ? '内存' : '磁盘')})
    },
  },
  {title: '创建时间', key: 'created_at', width: 180, render: (row) => formatTime(row.created_at)},
  {title: '到期时间', key: 'expire_time', width: 180, render: (row) => formatTime(row.expire_time)},
  {
    title: '操作', key: 'actions', width: 100,
    render(row) {
      return h(NButton, {size: 'small', type: 'error', ghost: true, onClick: () => handleRevoke(row)},
          {default: () => '清理'})
    },
  },
]
</script>

<template>
  <div class="cache-manage-container">
    <div class="toolbar">
      <n-space align="center" justify="space-between">
        <div class="title">缓存管理</div>
        <n-space align="center">
          <n-input
              v-model:value="username"
              placeholder="按用户名过滤"
              clearable
              style="width: 200px"
              @keyup.enter="onSearch"
              @clear="onSearch"
          />
          <n-button size="small" @click="onSearch">查询</n-button>
          <n-button size="small" @click="fetchList">刷新</n-button>
        </n-space>
      </n-space>
    </div>

    <div class="table-wrapper">
      <n-data-table
          :columns="columns"
          :data="list"
          :loading="loading"
          :row-key="(row: AdminQuickShareItem) => row.id"
          :bordered="false"
          :striped="true"
          size="small"
      >
        <template #empty>
          <n-empty description="当前没有有效的粘贴快传缓存"/>
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
.cache-manage-container {
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
