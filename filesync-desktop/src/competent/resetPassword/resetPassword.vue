<script setup lang="ts">
import {ref} from 'vue'
import {useRouter} from 'vue-router'
import {useMessage} from 'naive-ui'
import {useResetPassword} from './resetPassword.ts'

const router = useRouter()
const message = useMessage()
const {loading, resetPassword} = useResetPassword()

const form = ref({
  username: '',
  email: '',
  phone: '',
  new_password: '',
  confirm_password: '',
})

const getCondCount = () => {
  let count = 0
  if (form.value.username) count++
  if (form.value.email) count++
  if (form.value.phone) count++
  return count
}

const handleReset = async () => {
  if (getCondCount() < 2) {
    message.warning('请至少填写用户名、邮箱、手机号中的两项')
    return
  }
  if (!form.value.new_password) {
    message.warning('请输入新密码')
    return
  }
  if (form.value.new_password.length < 6) {
    message.warning('新密码至少6位')
    return
  }
  if (form.value.new_password !== form.value.confirm_password) {
    message.warning('两次密码不一致')
    return
  }

  await resetPassword({
    old_password: "",
    username: form.value.username || undefined,
    email: form.value.email || undefined,
    phone: form.value.phone || undefined,
    new_password: form.value.new_password
  })

  message.success('密码重置成功')
  await router.push({name: 'Login'})
}

const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Enter') handleReset()
}
</script>

<template>
  <div class="reset" @keydown="handleKeydown">
    <div class="reset-container">
      <div class="reset-header">
        <h1>重置密码</h1>
        <p>请填写以下信息中的至少两项进行验证</p>
      </div>

      <div class="reset-form">
        <n-form :model="form" label-placement="top">
          <n-form-item label="用户名">
            <n-input v-model:value="form.username" placeholder="请输入用户名" size="large" clearable/>
          </n-form-item>

          <n-form-item label="邮箱">
            <n-input v-model:value="form.email" placeholder="请输入邮箱" size="large" clearable/>
          </n-form-item>

          <n-form-item label="手机号">
            <n-input v-model:value="form.phone" placeholder="请输入手机号" size="large" clearable/>
          </n-form-item>

          <n-divider/>

          <n-form-item label="新密码">
            <n-input
                v-model:value="form.new_password"
                type="password"
                placeholder="至少6位"
                size="large"
                show-password-on="click"
            />
          </n-form-item>

          <n-form-item label="确认新密码">
            <n-input
                v-model:value="form.confirm_password"
                type="password"
                placeholder="再次输入新密码"
                size="large"
                show-password-on="click"
            />
          </n-form-item>

          <!-- 条件满足提示 -->
          <div class="condition-tip">
            <n-tag :type="getCondCount() >= 2 ? 'success' : 'warning'" size="small">
              已填写验证信息 {{ getCondCount() }}/3，至少需要 2 项
            </n-tag>
          </div>

          <n-button
              type="primary"
              size="large"
              block
              :loading="loading"
              :disabled="getCondCount() < 2"
              style="margin-top: 16px"
              @click="handleReset"
          >
            {{ loading ? '重置中...' : '重置密码' }}
          </n-button>
        </n-form>
      </div>

      <div class="reset-footer-nav">
        <n-button text type="primary" @click="router.push({ name: 'Login' })">
          返回登录
        </n-button>
      </div>

      <div class="reset-footer">
        <p>© 2025 私有云系统 - 多端文件同步平台</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.reset {
  width: 100%;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  position: relative;
  overflow: hidden;
  padding: 40px 0;
}

.reset::before {
  content: '';
  position: absolute;
  width: 200%;
  height: 200%;
  background-image: radial-gradient(circle, rgba(255, 255, 255, 0.1) 1px, transparent 1px);
  background-size: 50px 50px;
  animation: moveBackground 20s linear infinite;
}

@keyframes moveBackground {
  0% {
    transform: translate(0, 0);
  }
  100% {
    transform: translate(50px, 50px);
  }
}

.reset-container {
  width: 420px;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(10px);
  border-radius: 16px;
  padding: 40px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
  position: relative;
  z-index: 1;
}

.reset-header {
  text-align: center;
  margin-bottom: 32px;
}

.reset-header h1 {
  font-size: 28px;
  font-weight: 600;
  color: #333;
  margin: 0 0 8px 0;
}

.reset-header p {
  font-size: 13px;
  color: #888;
  margin: 0;
}

.reset-form {
  margin-bottom: 8px;
}

.condition-tip {
  margin-top: 4px;
  text-align: center;
}

.reset-footer-nav {
  text-align: center;
  margin-top: 16px;
}

.reset-footer {
  text-align: center;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid #eee;
}

.reset-footer p {
  font-size: 12px;
  color: #999;
  margin: 0;
}

:deep(.n-button--primary-type) {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%) !important;
  border: none !important;
  height: 48px;
  font-size: 16px;
  font-weight: 500;
  border-radius: 8px !important;
}

:deep(.n-input) {
  border-radius: 8px !important;
}
</style>