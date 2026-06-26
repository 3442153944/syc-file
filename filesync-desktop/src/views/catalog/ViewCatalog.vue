<script setup lang="ts">
import {onMounted, h} from "vue"
import {useRoute, useRouter} from "vue-router"
import {storeToRefs} from "pinia"
import {useCatalogStore} from "./composeables/useCatalogStore"
import {NDataTable, NButton, NSpace, NEmpty, useMessage} from "naive-ui"
import type {DataTableColumns} from "naive-ui"
import type {FileItem} from "@/api/file/fileTypes"

const route = useRoute()
const router = useRouter()
const catalogStore = useCatalogStore()
const message = useMessage() // 引入 Naive UI 的消息提示

// 使用 storeToRefs 保持响应式解构
const {currentPath, parentPath, items, loading} = storeToRefs(catalogStore)

onMounted(() => {
  const initPath = route.query.path as string
  if (initPath) {
    catalogStore.fetchDirectory(initPath)
  }
})

// 处理表格行的双击/点击事件
const handleRowClick = (row: FileItem) => {
  if (row.is_dir) {
    catalogStore.fetchDirectory(row.path)
    router.push({query: {path: row.path}})
  } else {
    // 单击文件直接触发下载
    handleDownload(row)
  }
}

// 返回上一级
const handleGoUp = () => {
  catalogStore.goParent()
  router.push({query: {path: parentPath.value}})
}

// ✨ 核心单文件下载逻辑
const handleDownload = (row: FileItem) => {
  if (row.is_dir) {
    message.warning("暂时不支持直接下载整个文件夹哦")
    return
  }

  const token = localStorage.getItem("token") || ""

  // 注意：这里的 /api 前缀请根据你实际的代理配置或 config.http 进行调整
  const downloadUrl = `/api/files/get-file?path=${encodeURIComponent(row.path)}&name=${encodeURIComponent(row.name)}&token=${token}`

  // 动态创建 a 标签触发浏览器原生下载，不占用内存
  const a = document.createElement("a")
  a.href = downloadUrl
  a.download = row.name
  a.style.display = "none"
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)

  message.success(`已开始下载: ${row.name}`)
}

const formatTime = (isoString: string) => {
  if (!isoString) return "-"
  const date = new Date(isoString)
  return date.toLocaleString("zh-CN", {
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit"
  })
}

// 定义 Naive UI 表格列
const columns: DataTableColumns<FileItem> = [
  {
    title: "名称",
    key: "name",
    sorter: "default",
    render(row) {
      const icon = row.is_dir ? "📁" : "📄"
      return h(
          "div",
          {
            style: {display: "flex", alignItems: "center", gap: "8px", cursor: "pointer"},
            onClick: () => handleRowClick(row)
          },
          [
            h("span", {style: {fontSize: "18px"}}, icon),
            h("span", {style: {fontWeight: row.is_dir ? "bold" : "normal"}}, row.name)
          ]
      )
    }
  },
  {
    title: "修改时间",
    key: "mod_time",
    render(row) {
      return formatTime(row.mod_time)
    }
  },
  // {
  //   title: "属性",
  //   key: "mode",
  //   render(row) {
  //     const perms = catalogStore.parseMode(row.mode)
  //     return h(
  //         NSpace,
  //         {size: 'small'},
  //         {
  //           default: () => perms.map(p =>
  //               h(
  //                   NTag,
  //                   {size: "small", type: p.type as any, bordered: false},
  //                   {default: () => p.label}
  //               )
  //           )
  //         }
  //     )
  //   }
  // },
  {
    title: "包含项",
    key: "children_count",
    render(row) {
      return row.is_dir ? `${row.children_count} 项` : "-"
    }
  },
  // ✨ 新增：操作列
  {
    title: "操作",
    key: "actions",
    width: 100,
    render(row) {
      return h(
          NButton,
          {
            size: "small",
            type: "primary",
            ghost: true,
            disabled: row.is_dir,
            onClick: (e) => {
              e.stopPropagation() // 阻止冒泡，防止触发整行的点击事件
              handleDownload(row)
            }
          },
          {default: () => (row.is_dir ? "-" : "下载")}
      )
    }
  }
]
</script>

<template>
  <div class="catalog-container">
    <div class="toolbar">
      <n-space align="center">
        <n-button
            v-if="parentPath"
            type="primary"
            ghost
            size="small"
            @click="handleGoUp"
        >
          ⬆ 返回上一级
        </n-button>

        <div class="current-path">
          <span class="path-label">当前路径：</span>
          <span class="path-value">{{ currentPath || "加载中..." }}</span>
        </div>
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
          :max-height="'calc(100vh - 200px)'"
      >
        <template #empty>
          <n-empty description="此文件夹为空"/>
        </template>
      </n-data-table>
    </div>
  </div>
</template>

<style scoped>
/* 样式保持不变 */
.catalog-container {
  padding: 16px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.05);
  height: 100%;
  display: flex;
  flex-direction: column;
}

.toolbar {
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid #f0f0f0;
}

.current-path {
  font-size: 14px;
  padding: 4px 8px;
  background-color: #f5f7fa;
  border-radius: 4px;
}

.path-label {
  color: #909399;
}

.path-value {
  color: #303133;
  font-family: monospace;
  font-weight: bold;
}

.table-wrapper {
  flex: 1;
  overflow: hidden;
}

:deep(.n-data-table-tr:hover) {
  background-color: #f0f7ff !important;
}
</style>