<script setup lang="ts">
import {ref, onMounted} from "vue"
import {getAvailableDisks} from "@/api/file/fileApi"
import type {DiskInfo} from "@/api/file/fileTypes"
import {formatBytes} from '@/utils/speedMeter'
import {Folder} from '@vicons/fa'
import {
  NSpin, NGrid, NGi, NCard, NProgress,
  NButton, NEmpty, NAlert, NIcon
} from "naive-ui"
import {useRouter} from "vue-router"

const router = useRouter()
const disk_list = ref<DiskInfo[]>([])
const loading = ref(false)
const loadError = ref('')
const isVirtual = (disk: DiskInfo) => disk.path === '/@space'
const percentage = (disk: DiskInfo) => {
  const value = disk.total > 0 ? disk.used / disk.total * 100 : disk.used_percent
  return Number.isFinite(value) ? Math.min(100, Math.max(0, Number(value.toFixed(1)))) : 0
}

async function loadDisks() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await getAvailableDisks()
    disk_list.value = res.all_disks || []
  } catch (error) {
    disk_list.value = []
    loadError.value = String(error)
  } finally {
    loading.value = false
  }
}
onMounted(loadDisks)


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
      <n-button size="small" :loading="loading" @click="loadDisks">刷新</n-button>
    </div>

    <n-spin :show="loading">
      <n-alert v-if="loadError" type="error" title="读取存储空间失败">{{ loadError }}</n-alert>
      <n-empty v-else-if="!loading && disk_list.length === 0" description="暂无可用存储空间，请联系管理员分配"/>

      <n-grid v-else x-gap="16" y-gap="16" cols="1 640:2 960:3 1280:4" responsive="self">
        <n-gi v-for="disk in disk_list" :key="disk.path">

          <n-card
              hoverable
              class="disk-card"
              :class="{ 'is-disabled': !disk.is_allowed }"
              size="small"
          >
            <div class="disk-header">
              <div class="disk-title">
                <n-icon class="disk-icon"><folder /></n-icon>
                <span class="disk-path" :title="isVirtual(disk) ? '管理员分配的独立文件空间' : disk.path">{{ isVirtual(disk) ? '我的文件空间' : `本地磁盘 (${disk.path})` }}</span>
              </div>
              <!-- 挂载点和路径通常是同一个值，重复显示只会挤占标题 -->
              <span v-if="disk.mountpoint && disk.mountpoint !== disk.path" class="disk-mount"
                    :title="disk.mountpoint">{{ disk.mountpoint }}</span>
            </div>

            <div class="disk-progress">
              <n-progress
                  type="line"
                  :percentage="percentage(disk)"
                  :color="getProgressColor(percentage(disk))"
                  :indicator-placement="'inside'"
                  :height="16"
                  border-radius="8px"
              />
            </div>

            <div class="disk-info">
              <span>可用 {{ formatBytes(disk.free) }} / 共 {{ formatBytes(disk.total) }}</span>
            </div>
            <div v-if="isVirtual(disk)" class="disk-info">已用容量包含文件、上传预占和已分配给下级的额度</div>

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
  display: flex;
  justify-content: space-between;
  align-items: center;
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
  gap: 12px;
  margin-bottom: 16px;
}

/* min-width: 0 是关键：flex 子项默认不会收缩到比内容更窄，长路径会把整行撑坏 */
.disk-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.disk-icon {
  font-size: 20px;
  flex-shrink: 0;
}

.disk-path {
  font-size: 15px;
  font-weight: bold;
  color: #333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.disk-mount {
  flex-shrink: 1;
  min-width: 0;
  max-width: 45%;
  font-size: 12px;
  color: #999;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
