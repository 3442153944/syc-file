<script setup lang="ts">
// 服务器初始化向导：服务器首次启动没有任何超级管理员时，客户端自动引导到这里。
// 三步：初始化码 → 超级管理员账号 → 基础设置，完成后自动登录。
import {computed, ref} from 'vue'
import {useRouter} from 'vue-router'
import {
  NSteps, NStep, NForm, NFormItem, NInput, NButton, NSwitch, NSpace, NAlert, NText, useMessage,
} from 'naive-ui'
import NodeSwitcher from '@/components/NodeSwitcher.vue'
import {initServer} from '@/api/system/systemApi'
import {useLogin} from '../login/login'
import {pinia} from '@/store/useStore'
import {useRouteStore} from '@/store/useRouteStore'

const router = useRouter()
const message = useMessage()
const rs = useRouteStore(pinia)
const {login} = useLogin()

const step = ref(1)
const submitting = ref(false)
const form = ref({
  setupCode: '',
  username: '',
  password: '',
  confirm: '',
  email: '',
  allowRegister: false,
})

const serverName = computed(() => rs.status?.name || '当前服务器')

// 每一步的前进条件。校验口径与服务端 /system/init 一致（用户名 ≥3，密码 ≥8），服务端仍会再校验一遍。
const stepError = computed(() => {
  if (step.value === 1) {
    return form.value.setupCode.trim() ? '' : '请输入初始化码'
  }
  if (step.value === 2) {
    if (form.value.username.trim().length < 3) return '用户名至少 3 位'
    if (form.value.password.length < 8) return '密码至少 8 位'
    if (form.value.password !== form.value.confirm) return '两次输入的密码不一致'
  }
  return ''
})

const next = () => {
  if (stepError.value) {
    message.warning(stepError.value)
    return
  }
  step.value++
}

const submit = async () => {
  submitting.value = true
  try {
    await initServer({
      setup_code: form.value.setupCode.trim(),
      username: form.value.username.trim(),
      password: form.value.password,
      email: form.value.email.trim() || undefined,
      allow_register: form.value.allowRegister,
    })
  } catch (e) {
    // 初始化码错误等：回到第一步让用户改，其它错误留在当前步骤
    const text = (e instanceof Error ? e.message : String(e)).replace(/^\[\d+]\s*/, '')
    message.error(`初始化失败：${text}`)
    if (text.includes('初始化码')) step.value = 1
    submitting.value = false
    return
  }

  rs.markInitialized()
  message.success('初始化完成，正在登录…')
  try {
    const res = await login({username: form.value.username.trim(), password: form.value.password})
    localStorage.setItem('token', res.token)
    localStorage.setItem('userInfo', JSON.stringify(res.user))
    rs.reset(router)
    await router.replace('/')
  } catch (e) {
    // 初始化已成功，只是自动登录失败：让用户手动登录
    message.warning('初始化已完成，请使用刚创建的账号登录：' + String(e))
    await router.replace('/login')
  } finally {
    submitting.value = false
  }
}

// 用户切换到另一台（可能已初始化的）服务器后，重新检测
const recheck = async () => {
  const s = await rs.ensureStatus(true)
  if (s?.initialized) {
    message.info('该服务器已完成初始化，请直接登录')
    await router.replace('/login')
  } else if (!s) {
    message.error('无法连接服务器，请检查地址')
  } else {
    message.success('服务器尚未初始化，可以继续')
  }
}
</script>

<template>
  <div class="init-page">
    <div class="init-node">
      <NodeSwitcher/>
    </div>
    <div class="init-card">
      <div class="init-header">
        <h1>初始化服务器</h1>
        <p>{{ serverName }} 还没有管理员，先完成初始化</p>
      </div>

      <n-steps :current="step" size="small" class="init-steps">
        <n-step title="初始化码"/>
        <n-step title="超级管理员"/>
        <n-step title="基础设置"/>
      </n-steps>

      <n-form label-placement="top" class="init-form" @submit.prevent>
        <template v-if="step === 1">
          <n-alert type="info" :show-icon="false" class="init-tip">
            初始化码在服务器启动日志里（形如 <n-text code>ABCD-2345</n-text>）。
            Docker 部署用 <n-text code>docker logs 容器名</n-text> 查看；也可以用环境变量
            <n-text code>SYC_SETUP_CODE</n-text> 预先指定。它是一次性的，初始化完成后即失效。
          </n-alert>
          <n-form-item label="初始化码">
            <n-input v-model:value="form.setupCode" placeholder="ABCD-2345" size="large" clearable
                     @keyup.enter="next"/>
          </n-form-item>
          <n-button text type="primary" size="small" @click="recheck">换了服务器？重新检测</n-button>
        </template>

        <template v-else-if="step === 2">
          <n-form-item label="超级管理员用户名">
            <n-input v-model:value="form.username" placeholder="至少 3 位" size="large"/>
          </n-form-item>
          <n-form-item label="密码">
            <n-input v-model:value="form.password" type="password" show-password-on="click"
                     placeholder="至少 8 位" size="large"/>
          </n-form-item>
          <n-form-item label="确认密码">
            <n-input v-model:value="form.confirm" type="password" show-password-on="click" size="large"
                     @keyup.enter="next"/>
          </n-form-item>
          <n-form-item label="邮箱（可选）">
            <n-input v-model:value="form.email" placeholder="用于找回账号，可留空" size="large"/>
          </n-form-item>
        </template>

        <template v-else>
          <n-alert type="warning" :show-icon="false" class="init-tip">
            超级管理员拥有全部权限，并能管理管理员、路由和系统开关。请妥善保管这个账号。
          </n-alert>
          <n-form-item label="开放自助注册">
            <n-space align="center">
              <n-switch v-model:value="form.allowRegister"/>
              <n-text depth="3">{{ form.allowRegister ? '任何人都能自行注册账号' : '关闭：只有管理员能创建账号（推荐，服务暴露在公网时尤其应该关闭）' }}</n-text>
            </n-space>
          </n-form-item>
          <n-text depth="3">以后可以在「系统管理 → 用户管理」里随时修改。</n-text>
        </template>
      </n-form>

      <n-space justify="space-between" class="init-actions">
        <n-button v-if="step > 1" :disabled="submitting" @click="step--">上一步</n-button>
        <span v-else/>
        <n-button v-if="step < 3" type="primary" @click="next">下一步</n-button>
        <n-button v-else type="primary" :loading="submitting" @click="submit">完成初始化并登录</n-button>
      </n-space>
    </div>
  </div>
</template>

<style scoped>
.init-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f5f7fa;
  position: relative;
}

.init-node {
  position: absolute;
  top: 16px;
  right: 20px;
}

.init-card {
  width: 480px;
  max-width: calc(100vw - 32px);
  background: #fff;
  border-radius: 12px;
  padding: 32px 36px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.08);
}

.init-header {
  text-align: center;
  margin-bottom: 20px;
}

.init-header h1 {
  margin: 0 0 6px;
  font-size: 22px;
}

.init-header p {
  margin: 0;
  color: #888;
  font-size: 13px;
}

.init-steps {
  margin-bottom: 24px;
}

.init-form {
  min-height: 190px;
}

.init-tip {
  margin-bottom: 16px;
}

.init-actions {
  margin-top: 8px;
}
</style>
