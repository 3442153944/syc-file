<script setup lang="ts">
import {ref, onMounted} from "vue"
import {getAvailableDisks} from "@/api/file/fileApi"
import type {DiskInfo} from "@/api/file/fileTypes"
import {
  NSpin, NGrid, NGi, NCard, NProgress,
  NSpace, NButton, NEmpty
} from "naive-ui"
import {useRouter} from "vue-router"

const router = useRouter()
const disk_list = ref<DiskInfo[]>([])
const loading = ref(false)

onMounted(async () => {
  loading.value = true
  try {
    const res = await getAvailableDisks()
    disk_list.value = res.all_disks
  } catch (error) {
    console.error("获取磁盘列表失败", error)
  } finally {
    loading.value = false
  }
})


const handleOpenDisk = (disk: DiskInfo) => {
  router.push({
    path: '/file/catalog',
    query: {path: disk.path}
  })
}

// 2. 动态计算进度条颜色：快满的时候标红警告
const getProgressColor = (percent: number) => {
  if (percent > 90) return "#d03050" // 红色 (极度紧张)
  if (percent > 70) return "#f0a020" // 橙色 (警告)
  return "#18a058"                   // 绿色 (健康)
}
</script>

<template>
  <div class="disk-container">
    <div class="header">
      <h2>存储空间节点</h2>
    </div>

    <n-spin :show="loading">
      <n-empty v-if="!loading && disk_list.length === 0" description="暂无可用磁盘"/>

      <n-grid v-else x-gap="16" y-gap="16" cols="1 s:2 m:3 l:4" responsive="screen">
        <n-gi v-for="disk in disk_list" :key="disk.path">

          <n-card
              hoverable
              class="disk-card"
              :class="{ 'is-disabled': !disk.is_allowed }"
              size="small"
          >
            <div class="disk-header">
              <div class="disk-title">
                <span class="disk-icon">🖴</span>
                <span class="disk-path">本地磁盘 ({{ disk.path }})</span>
              </div>
              <n-space size="small">
                <n-button size="tiny" quaternary>{{ disk.mountpoint }}</n-button>
              </n-space>
            </div>

            <div class="disk-progress">
              <n-progress
                  type="line"
                  :percentage="Number(disk.used_percent.toFixed(1))"
                  :color="getProgressColor(disk.used_percent)"
                  :indicator-placement="'inside'"
                  :height="16"
                  border-radius="8px"
              />
            </div>

            <div class="disk-info">
              <span>可用 {{ disk.free_gb }} / 共 {{ disk.total_gb }}</span>
            </div>

            <template #footer>
              <div class="disk-action">
                <span v-if="!disk.is_allowed" class="status-text error">无访问权限</span>
                <span v-else-if="!disk.is_accessible" class="status-text warning">无法访问</span>
                <span v-else class="status-text success">运行正常</span>

                <n-button
                    type="primary"
                    size="small"
                    :disabled="!disk.is_allowed || !disk.is_accessible"
                    @click="handleOpenDisk(disk)"
                >
                  打开
                </n-button>
              </div>
            </template>
          </n-card>

        </n-gi>
      </n-grid>
    </n-spin>
  </div>
</template>

<style scoped>
.disk-container {
  padding: 10px;
}

.header {
  margin-bottom: 24px;
}

.header h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
  color: #333;
}

/* 卡片样式优化 */
.disk-card {
  border-radius: 12px;
  transition: all 0.3s ease;
}

/* 没有权限的磁盘置灰 */
.disk-card.is-disabled {
  opacity: 0.55;
  filter: grayscale(0.8);
  background-color: #f9f9f9;
}

.disk-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.disk-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.disk-icon {
  font-size: 20px;
}

.disk-path {
  font-size: 15px;
  font-weight: bold;
  color: #333;
}

.disk-progress {
  margin-bottom: 8px;
}

.disk-info {
  font-size: 13px;
  color: #666;
  text-align: right;
  margin-bottom: 4px;
}

/* 底部插槽样式 */
:deep(.n-card__footer) {
  padding: 12px;
  background-color: #fafafa;
  border-top: 1px solid #f0f0f0;
  border-bottom-left-radius: 12px;
  border-bottom-right-radius: 12px;
}

.disk-action {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.status-text {
  font-size: 12px;
  font-weight: 500;
}

.status-text.success {
  color: #18a058;
}

.status-text.error {
  color: #d03050;
}

.status-text.warning {
  color: #f0a020;
}
</style>