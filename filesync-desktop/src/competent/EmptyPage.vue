<script setup lang="ts">
// 路由表加载失败 / 当前账号没有任何可访问页面时的落地页。静态注册在 Home 下，保证总有地方可去。
import {computed, ref} from 'vue'
import {useRouter} from 'vue-router'
import {NResult, NButton, NSpace} from 'naive-ui'
import {pinia} from '@/store/useStore'
import {useRouteStore} from '@/store/useRouteStore'

const router = useRouter()
const rs = useRouteStore(pinia)
const retrying = ref(false)

// 网络类错误才提示重试；其余（拉取成功但没有页面）说明是权限问题
const networkError = computed(() => !!rs.loadError && rs.loadError !== 'unauthorized')

const retry = async () => {
  retrying.value = true
  try {
    rs.reset(router)
    await router.replace('/')
  } finally {
    retrying.value = false
  }
}

const logout = () => {
  localStorage.removeItem('token')
  localStorage.removeItem('userInfo')
  rs.reset(router)
  router.push('/login')
}
</script>

<template>
  <div class="empty-page">
    <n-result
        :status="networkError ? 'error' : 'info'"
        :title="networkError ? '无法获取菜单' : '没有可访问的页面'"
        :description="networkError ? `服务器暂时不可达：${rs.loadError}` : '当前账号尚未被授权访问任何页面，请联系管理员。'"
    >
      <template #footer>
        <n-space justify="center">
          <n-button v-if="networkError" key="retry" type="primary" :loading="retrying" @click="retry">重试</n-button>
          <n-button key="logout" @click="logout">退出登录</n-button>
        </n-space>
      </template>
    </n-result>
  </div>
</template>

<style scoped>
.empty-page {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  min-height: 360px;
}
</style>
