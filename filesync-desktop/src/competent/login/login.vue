<!-- login.vue -->
<script setup lang="ts">
import {ref} from 'vue'
import {useRouter} from 'vue-router'
import {useLogin} from './login.ts'
import {isTauri, invoke} from '@tauri-apps/api/core'
import {saveWebPassword, loadWebPassword, clearWebPassword} from '@/utils/credentialStore'
import {
  useMessage,
  NForm,
  NFormItem,
  NInput,
  NButton,
  NCheckbox,
  NDivider,
  NIcon
} from 'naive-ui'

const router = useRouter()
const message = useMessage()
const {login} = useLogin()

const form = ref({
  username: '',
  password: '',
})

const loading = ref(false)
const rememberMe = ref(false)

// 记住密码：账号本身不敏感，继续放 localStorage；密码 Tauri 下存 OS 原生凭据管理器
// （Windows Credential Manager / macOS Keychain / Linux Secret Service），Web 模式下
// 用 Web Crypto 本地加密（见 utils/credentialStore.ts 顶部注释，说明了这条路径的局限）。
const loadRememberedAccount = async () => {
  const remembered = localStorage.getItem('rememberedAccount')
  if (!remembered) return
  try {
    const account = JSON.parse(remembered)
    form.value.username = account.username
    rememberMe.value = true
    if (isTauri()) {
      try {
        const pwd = await invoke<string | null>('get_remembered_credential', {username: account.username})
        if (pwd) form.value.password = pwd
      } catch {
        // 取不到就当没记住，用户照常手动输入密码
      }
    } else {
      const pwd = await loadWebPassword()
      if (pwd) form.value.password = pwd
    }
  } catch {
    // rememberedAccount 数据损坏，忽略即可，不影响正常登录
  }
}

loadRememberedAccount()

const handleLogin = async () => {
  if (!form.value.username || !form.value.password) {
    message.warning('请输入用户名和密码')
    return
  }

  loading.value = true
  try {
    const res = await login(form.value)

    localStorage.setItem('token', res.token)
    localStorage.setItem('userInfo', JSON.stringify(res.user))

    if (rememberMe.value) {
      localStorage.setItem('rememberedAccount', JSON.stringify({username: form.value.username}))
      if (isTauri()) {
        try {
          await invoke('save_remembered_credential', {username: form.value.username, password: form.value.password})
        } catch (e) {
          message.warning('账号已记住，但密码保存到系统凭据管理器失败：' + String(e))
        }
      } else {
        try {
          await saveWebPassword(form.value.password)
        } catch {
          // 加密保存失败不影响本次登录，只是下次要重新手动输入密码
        }
      }
    } else {
      localStorage.removeItem('rememberedAccount')
      if (isTauri()) {
        try {
          await invoke('clear_remembered_credential', {username: form.value.username})
        } catch {
          // 忽略：本来就没记住过也会走到这里
        }
      } else {
        clearWebPassword()
      }
    }

    message.success('登录成功')

    // 登录成功后自动启动同步引擎
    try {
      const { invoke } = await import('@tauri-apps/api/core')
      await invoke('start_sync')
    } catch { /* 未配置同步文件夹等正常情况 */ }

    await router.push({name: 'Home'})
  } catch (error) {
    console.error('登录失败', error)
  } finally {
    loading.value = false
  }
}

const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Enter') handleLogin()
}

//跳转注册页面
const handleRegister = () => {
  router.push('/register')
}

//跳转忘记密码页面
const handleResetPassword = () => {
  router.push('/reset')
}

</script>

<template>
  <div class="login" @keydown="handleKeydown">
    <div class="login-container">
      <div class="login-header">
        <h1>私有云系统</h1>
        <p>File Sync Platform</p>
      </div>

      <div class="login-form">
        <n-form :model="form" label-placement="top">
          <n-form-item label="用户名">
            <n-input
                v-model:value="form.username"
                placeholder="请输入用户名"
                size="large"
                clearable
            >
              <template #prefix>
                <n-icon><i class="icon-user"/></n-icon>
              </template>
            </n-input>
          </n-form-item>

          <n-form-item label="密码">
            <n-input
                v-model:value="form.password"
                type="password"
                placeholder="请输入密码"
                size="large"
                show-password-on="click"
            />
          </n-form-item>

          <n-form-item>
            <div class="login-options">
              <n-checkbox v-model:checked="rememberMe">记住账号密码</n-checkbox>
              <n-button text type="primary" size="small" @click="handleResetPassword">忘记密码?</n-button>
            </div>
          </n-form-item>

          <n-button
              type="primary"
              size="large"
              :loading="loading"
              block
              @click="handleLogin"
          >
            {{ loading ? '登录中...' : '登录' }}
          </n-button>
        </n-form>
      </div>

      <div class="login-divider">
        <n-divider>还没有账号?</n-divider>
        <n-button block @click="handleRegister">
          立即注册
        </n-button>
      </div>

      <div class="login-footer">
        <p>© 2025 私有云系统 - 多端文件同步平台</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login {
  width: 100%;
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  position: relative;
  overflow: hidden;
}

.login::before {
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

.login-container {
  width: 420px;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(10px);
  border-radius: 16px;
  padding: 40px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
  position: relative;
  z-index: 1;
}

.login-header {
  text-align: center;
  margin-bottom: 36px;
}

.login-header h1 {
  font-size: 28px;
  font-weight: 600;
  color: #333;
  margin: 0 0 8px 0;
}

.login-header p {
  font-size: 14px;
  color: #666;
  margin: 0;
}

.login-form {
  margin-bottom: 8px;
}

.login-options {
  width: 100%;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.login-divider {
  margin-top: 8px;
}

.login-footer {
  text-align: center;
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid #eee;
}

.login-footer p {
  font-size: 12px;
  color: #999;
  margin: 0;
}

:deep(.n-input) {
  border-radius: 8px !important;
}
</style>