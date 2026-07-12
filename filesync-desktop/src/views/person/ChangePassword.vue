<script setup lang="ts">
// 修改密码
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { NCard, NForm, NFormItem, NInput, NButton, NIcon, useMessage } from 'naive-ui'
import { ArrowLeft, Lock, Key } from '@vicons/fa'
import { changePassword } from '@/api/user/userApi'

const router = useRouter()
const message = useMessage()

const form = ref({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})
const loading = ref(false)

async function handleSubmit() {
  if (!form.value.oldPassword || !form.value.newPassword) {
    message.warning('请填写所有字段')
    return
  }
  if (form.value.newPassword.length < 6) {
    message.warning('新密码长度至少 6 位')
    return
  }
  if (form.value.newPassword !== form.value.confirmPassword) {
    message.warning('两次输入的新密码不一致')
    return
  }

  loading.value = true
  try {
    await changePassword(form.value.oldPassword, form.value.newPassword)
    message.success('密码修改成功，请重新登录')
    localStorage.removeItem('token')
    localStorage.removeItem('userInfo')
    setTimeout(() => router.push('/login'), 1500)
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="change-password">
    <n-button text @click="router.back()" style="margin-bottom:16px">
      <template #icon><n-icon><ArrowLeft /></n-icon></template>
      返回
    </n-button>

    <n-card title="修改密码" size="small">
      <n-form label-placement="left" label-width="80px" size="medium">
        <n-form-item label="旧密码">
          <n-input v-model:value="form.oldPassword" type="password" :disabled="loading"
                   placeholder="请输入旧密码" show-password-on="click">
            <template #prefix><n-icon><Lock /></n-icon></template>
          </n-input>
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="form.newPassword" type="password" :disabled="loading"
                   placeholder="至少 6 位" show-password-on="click">
            <template #prefix><n-icon><Key /></n-icon></template>
          </n-input>
        </n-form-item>
        <n-form-item label="确认密码">
          <n-input v-model:value="form.confirmPassword" type="password" :disabled="loading"
                   placeholder="再次输入新密码" show-password-on="click">
            <template #prefix><n-icon><Key /></n-icon></template>
          </n-input>
        </n-form-item>
      </n-form>

      <n-button type="primary" :loading="loading" @click="handleSubmit" block style="margin-top:16px">
        {{ loading ? '提交中...' : '确认修改' }}
      </n-button>
    </n-card>
  </div>
</template>

<style scoped>
.change-password {
  max-width: 480px;
  margin: 0 auto;
}
</style>
