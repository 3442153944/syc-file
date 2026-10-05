<script setup lang="ts">
// 文件列表里的「缩略图 / 类型图标」。
// 图片 → 缩略图；视频 → 封面（文件自带，没有就是服务端取的一帧）；有内嵌封面的音频 → 专辑封面。
// 非这几类、加载失败（如音频没有封面，服务端返回 415）→ 显示默认插槽（调用方给的图标），
// 所以任何文件列表都可以无脑套用，不需要先判断类型。
import { ref, watch } from 'vue'
import { buildThumbnailUrl } from '@/api/file/fileApi'
import { hasThumbnail } from '@/utils/fileKind'

const props = withDefaults(
  defineProps<{
    /** 服务端路径（目录或完整路径均可，与下载接口的 path 同义） */
    path: string
    name: string
    /** 文件大小（字节），同时作为缩略图的缓存版本号 */
    size?: number
    /** 方块边长（px） */
    boxSize?: number
  }>(),
  { size: 0, boxSize: 32 },
)

const url = ref('')
const failed = ref(false)

// 固定用 256 档：服务端上传/浏览时预生成的就是这一档，命中即取，不用当场生成
const WIDTH = 256

watch(
  () => [props.path, props.name, props.size] as const,
  async ([path, name, size]) => {
    failed.value = false
    url.value = ''
    if (!path || !hasThumbnail(name)) return
    try {
      url.value = await buildThumbnailUrl(path, name, WIDTH, size)
    } catch {
      failed.value = true
    }
  },
  { immediate: true },
)
</script>

<template>
  <span class="file-thumb" :style="{ width: boxSize + 'px', height: boxSize + 'px' }">
    <img v-if="url && !failed" :src="url" :alt="name" loading="lazy" decoding="async" @error="failed = true" />
    <slot v-else />
  </span>
</template>

<style scoped>
.file-thumb {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  overflow: hidden;
  border-radius: 6px;
}
.file-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
</style>
