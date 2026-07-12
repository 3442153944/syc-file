<script setup lang="ts">
// 服务器地址设置对话框
import {ref, onMounted} from 'vue'
import {invoke, isTauri} from '@tauri-apps/api/core'
import {NModal, NForm, NFormItem, NInput, NButton, NSpace, useMessage} from 'naive-ui'
import {setServerUrl} from '@/api/platform'

defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()

const message = useMessage()
const form = ref({serverUrl: '', wsUrl: ''})
const loading = ref(false)

onMounted(async () => {
  if (!isTauri()) return
  try {
    const cfg = await invoke<any>('get_sync_config')
    form.value.serverUrl = cfg?.server_url || ''
    form.value.wsUrl = cfg?.ws_url || ''
  } catch { /* ignore */
  }
})

async function handleSave() {
  if (!form.value.serverUrl) {
    message.warning('请输入服务器地址')
    return
  }
  loading.value = true
  try {
    let ws = form.value.wsUrl
    if (!ws) {
      ws = form.value.serverUrl
          .replace(/^http/, 'ws')
          .replace(/\/+$/, '') + '/v1/ws/connect'
    }
    await invoke('set_sync_config', {
      serverUrl: form.value.serverUrl.replace(/\/+$/, ''),
      wsUrl: ws,
      token: '',
      uploadWorkers: null,
      downloadWorkers: null,
      debounceMs: null,
      logLevel: null,
    })
    // 同步到 localStorage（头像 URL 拼接用）
    setServerUrl(form.value.serverUrl.replace(/\/+$/, ''))
    message.success('服务器地址已保存')
    emit('close')
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="(v:boolean) => !v && emit('close')" preset="card"
           title="服务器设置" style="width:480px" :mask-closable="false">
    <n-form label-placement="left" label-width="80px" size="medium">
      <n-form-item label="服务器地址">
        <n-input v-model:value="form.serverUrl" placeholder="https://ddns.sunyuanling.cn:8443" :disabled="loading"/>
      </n-form-item>
      <n-form-item label="WebSocket">
        <n-input v-model:value="form.wsUrl" placeholder="留空自动推导" :disabled="loading"/>
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="emit('close')" :disabled="loading">取消</n-button>
        <n-button type="primary" @click="handleSave" :loading="loading">保存</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
