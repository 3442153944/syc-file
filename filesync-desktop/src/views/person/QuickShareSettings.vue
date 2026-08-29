<script setup lang="ts">
// 粘贴快传设置：全局唤起快捷键（录制，不手打）+ 默认分享有效期 + 配额展示
import {ref, onMounted, onBeforeUnmount} from 'vue'
import {useRouter} from 'vue-router'
import {NCard, NForm, NFormItem, NInputNumber, NButton, NIcon, NTag, NDescriptions, NDescriptionsItem, useMessage} from 'naive-ui'
import {ArrowLeft} from '@vicons/fa'
import {isTauri, invoke} from '@tauri-apps/api/core'
import {saveQuickShareSettings, getQuickShareQuota} from '@/api/share/quickShareApi'
import {captureHotkey, encodeHotkeyHex, decodeHotkeyLabel} from '@/utils/hotkeyCodec'
import type {UserInfo} from '@/api/user/userTypes'
import type {QuickShareQuota} from '@/api/file/fileTypes'

const router = useRouter()
const message = useMessage()
const inTauri = isTauri()

// 存库/传给后端的是 hotkeyHex（十六进制编码，跟平台无关，不是 "Ctrl+Shift+V" 这种文本）；
// hotkeyLabel 只是给用户看的展示文本，不落库。
const hotkeyHex = ref('')
const hotkeyLabel = ref('未设置')
const recording = ref(false)
const expireMinutes = ref(60)
const saving = ref(false)
const quota = ref<QuickShareQuota | null>(null)

const formatBytes = (n: number) => {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0, v = n
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`
}

onMounted(async () => {
  const saved = localStorage.getItem('userInfo')
  if (saved) {
    try {
      const u = JSON.parse(saved) as UserInfo
      hotkeyHex.value = u.quick_share_hotkey || ''
      hotkeyLabel.value = decodeHotkeyLabel(hotkeyHex.value)
      expireMinutes.value = u.quick_share_expire_minutes || 60
    } catch { /* ignore */
    }
  }
  try {
    quota.value = await getQuickShareQuota()
  } catch { /* 配额展示失败不影响设置页其它功能 */
  }
})

function onKeydown(e: KeyboardEvent) {
  e.preventDefault()
  e.stopPropagation()
  if (e.code === 'Escape') {
    stopRecording()
    hotkeyLabel.value = decodeHotkeyLabel(hotkeyHex.value)
    return
  }
  const captured = captureHotkey(e)
  if (!captured) return // 还只是修饰键本身，或者暂不支持的键，继续等下一次按键
  hotkeyHex.value = encodeHotkeyHex(captured.code)
  hotkeyLabel.value = captured.label
  stopRecording()
}

function startRecording() {
  recording.value = true
  hotkeyLabel.value = '请按下组合键…（Esc 取消）'
  window.addEventListener('keydown', onKeydown, true)
}

function stopRecording() {
  recording.value = false
  window.removeEventListener('keydown', onKeydown, true)
}

onBeforeUnmount(() => {
  if (recording.value) window.removeEventListener('keydown', onKeydown, true)
})

async function handleSave() {
  if (!hotkeyHex.value) {
    message.warning('请先录制快捷键')
    return
  }
  if (!expireMinutes.value || expireMinutes.value <= 0) {
    message.warning('请填写有效期')
    return
  }
  saving.value = true
  try {
    await saveQuickShareSettings(hotkeyHex.value, expireMinutes.value)

    const saved = localStorage.getItem('userInfo')
    if (saved) {
      const u = JSON.parse(saved) as UserInfo
      u.quick_share_hotkey = hotkeyHex.value
      u.quick_share_expire_minutes = expireMinutes.value
      localStorage.setItem('userInfo', JSON.stringify(u))
    }

    if (inTauri) {
      try {
        await invoke('set_quick_paste_hotkey', {hotkey: hotkeyHex.value})
      } catch (e) {
        message.warning('快捷键已保存，但立即生效失败，重启应用后生效：' + String(e))
      }
    }

    message.success('保存成功')
    router.push('/person/center')
  } catch (e) {
    message.error(String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="quick-share-settings">
    <n-button text @click="router.back()" style="margin-bottom:16px">
      <template #icon>
        <n-icon>
          <ArrowLeft/>
        </n-icon>
      </template>
      返回
    </n-button>

    <n-card title="粘贴快传设置" size="small">
      <n-form label-placement="left" label-width="100px" size="medium">
        <n-form-item label="唤起快捷键">
          <n-button :type="recording ? 'warning' : 'default'" :disabled="saving" @click="startRecording">
            <n-tag v-if="!recording" round>{{ hotkeyLabel }}</n-tag>
            <span v-else>{{ hotkeyLabel }}</span>
          </n-button>
        </n-form-item>
        <n-form-item label="默认有效期">
          <n-input-number v-model:value="expireMinutes" :disabled="saving" :min="1" style="width:100%">
            <template #suffix>分钟</template>
          </n-input-number>
        </n-form-item>
      </n-form>

      <n-descriptions v-if="quota" label-placement="left" :column="1" bordered size="small" style="margin-top:16px">
        <n-descriptions-item label="已用空间">
          {{ formatBytes(quota.used_bytes) }} / {{ formatBytes(quota.max_bytes) }}
        </n-descriptions-item>
      </n-descriptions>

      <div class="hint">
        点击上面的按钮，再按下想用的组合键即可录制（至少带一个 Ctrl/Shift/Alt/Meta）。应用主窗口聚焦时，任意页面粘贴文件都会直接上传；应用在后台时，按这个快捷键唤起悬浮窗后再粘贴。
      </div>

      <n-button type="primary" :loading="saving" @click="handleSave" block style="margin-top:16px">
        保存
      </n-button>
    </n-card>
  </div>
</template>

<style scoped>
.quick-share-settings {
  max-width: 480px;
  margin: 0 auto;
}

.hint {
  margin-top: 12px;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
}
</style>
