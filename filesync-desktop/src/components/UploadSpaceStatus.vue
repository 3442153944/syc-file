<script setup lang="ts">
import { computed } from 'vue'
import { NAlert, NButton, NCard, NProgress, NSpace, NTag } from 'naive-ui'
import type { UploadSpace } from '@/api/file/storageApi'
import { formatBytes } from '@/utils/speedMeter'

const props = withDefaults(defineProps<{ space: UploadSpace; loading?: boolean; showBrowse?: boolean }>(), { loading: false, showBrowse: true })
defineEmits<{ refresh: []; browse: [] }>()
const percent = computed(() => Math.min(100, Math.round(props.space.used_bytes / props.space.quota_bytes * 100)))
</script>

<template>
  <n-card :title="space.pool_name" size="small">
    <n-space vertical>
      <n-space justify="space-between"><span>{{ formatBytes(space.used_bytes) }} / {{ formatBytes(space.quota_bytes) }}</span><n-tag :type="space.frozen ? 'warning' : 'success'">{{ space.frozen ? '已冻结' : '可用' }}</n-tag></n-space>
      <n-progress type="line" :percentage="percent" :status="percent >= 100 ? 'warning' : 'success'" />
      <span>自身文件及未完成上传：{{ formatBytes(space.own_used_bytes) }} · 已划给下级：{{ formatBytes(space.allocated_bytes) }}</span>
      <span>可继续使用或分配：{{ formatBytes(Math.max(0, space.quota_bytes - space.used_bytes)) }} · {{ space.file_count }} 个文件</span>
      <span>有效期：{{ space.expires_at ? new Date(space.expires_at).toLocaleString() : '不限期' }}</span>
      <n-alert v-if="space.frozen" type="warning">当前或上级空间已停用、到期，文件仍保留。请联系上级管理员续期或启用。</n-alert>
      <n-space><n-button v-if="showBrowse" :disabled="space.frozen || loading" @click="$emit('browse')">浏览我的文件</n-button><n-button :loading="loading" @click="$emit('refresh')">刷新用量</n-button></n-space>
    </n-space>
  </n-card>
</template>
