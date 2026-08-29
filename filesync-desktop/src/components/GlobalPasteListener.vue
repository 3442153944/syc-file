<script setup lang="ts">
// 挂在主窗口根组件里的无渲染监听器：应用窗口只要处于焦点，随便在哪个页面粘贴文件都
// 直接上传（不是文件就完全不干预，不影响正常粘贴文本到输入框）。悬浮窗/日志窗口不挂这个，
// 各自有自己的处理（悬浮窗用 QuickPaste.vue 自己的可见粘贴区域）。
import {onMounted, onUnmounted} from 'vue'
import {useMessage} from 'naive-ui'
import {useQuickPaste} from '@/composables/useQuickPaste'

const message = useMessage()

const {handlePasteEvent} = useQuickPaste((result, err) => {
  if (result) {
    message.success('已上传，链接已复制到剪贴板')
  } else {
    message.error(err instanceof Error ? err.message : String(err))
  }
})

onMounted(() => document.addEventListener('paste', handlePasteEvent))
onUnmounted(() => document.removeEventListener('paste', handlePasteEvent))
</script>

<template></template>
