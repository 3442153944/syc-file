<script setup lang="ts">
// 编辑资料：修改用户名 / 邮箱 / 手机 / 头像
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NForm, NFormItem, NInput, NButton, NAvatar, NSpace, NIcon, useMessage } from 'naive-ui'
import { ArrowLeft, Upload as UploadIcon } from '@vicons/fa'
import { open } from '@tauri-apps/plugin-dialog'
import { isTauri } from '@tauri-apps/api/core'
import { updateProfile } from '@/api/user/userApi'
import type { UserInfo } from '@/api/user/userTypes'

const router = useRouter()
const message = useMessage()
const inTauri = isTauri()

const form = ref({
  username: '',
  email: '',
  phone: '',
  avatarPath: '',
})
const avatarPreview = ref('')
const saving = ref(false)
const orig = ref<Partial<UserInfo>>({})

onMounted(() => {
  const saved = localStorage.getItem('userInfo')
  if (saved) {
    const u = JSON.parse(saved) as UserInfo
    orig.value = u
    form.value.username = u.username || ''
    form.value.email = u.email || ''
    form.value.phone = u.phone || ''
    if (u.avatar) avatarPreview.value = u.avatar
  }
})

async function pickAvatar() {
  if (!inTauri) {
    message.warning('头像上传仅桌面端支持')
    return
  }
  const sel = await open({ filters: [{ name: '图片', extensions: ['png', 'jpg', 'jpeg', 'gif', 'webp'] }], multiple: false })
  if (sel) {
    form.value.avatarPath = sel
    avatarPreview.value = `local://${sel}`
  }
}

async function handleSave() {
  saving.value = true
  try {
    const updates: Record<string, any> = {}
    if (form.value.username && form.value.username !== orig.value.username) updates.username = form.value.username
    if (form.value.email !== (orig.value.email || '')) updates.email = form.value.email
    if (form.value.phone !== (orig.value.phone || '')) updates.phone = form.value.phone
    if (form.value.avatarPath) updates.avatarPath = form.value.avatarPath

    if (Object.keys(updates).length === 0) {
      message.info('没有需要更新的内容')
      return
    }

    await updateProfile(updates)
    message.success('资料已更新')

    // 更新 localStorage 缓存
    const saved = localStorage.getItem('userInfo')
    if (saved) {
      const u = JSON.parse(saved) as UserInfo
      if (updates.username) u.username = updates.username
      if (updates.email !== undefined) u.email = updates.email
      if (updates.phone !== undefined) u.phone = updates.phone
      localStorage.setItem('userInfo', JSON.stringify(u))
    }
    router.push('/person/center')
  } catch (e) {
    message.error(String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="edit-profile">
    <n-button text @click="router.back()" style="margin-bottom:16px">
      <template #icon><n-icon><ArrowLeft /></n-icon></template>
      返回
    </n-button>

    <n-card title="编辑资料" size="small">
      <n-space vertical align="center" style="width:100%;margin-bottom:24px">
        <n-avatar :size="80" round :src="avatarPreview || undefined">
          <template #fallback>
            <n-icon size="48"><UploadIcon /></n-icon>
          </template>
        </n-avatar>
        <n-button v-if="inTauri" size="small" @click="pickAvatar" :disabled="saving">
          更换头像
        </n-button>
      </n-space>

      <n-form label-placement="left" label-width="64px" size="medium">
        <n-form-item label="用户名">
          <n-input v-model:value="form.username" :disabled="saving" placeholder="用户名" />
        </n-form-item>
        <n-form-item label="邮箱">
          <n-input v-model:value="form.email" :disabled="saving" placeholder="邮箱" />
        </n-form-item>
        <n-form-item label="手机">
          <n-input v-model:value="form.phone" :disabled="saving" placeholder="手机号" />
        </n-form-item>
      </n-form>

      <n-button type="primary" :loading="saving" @click="handleSave" block style="margin-top:16px">
        保存修改
      </n-button>
    </n-card>
  </div>
</template>

<style scoped>
.edit-profile {
  max-width: 480px;
  margin: 0 auto;
}
</style>
