<script setup lang="ts">
import {onMounted, onBeforeUnmount, ref, computed} from "vue"
import {useRouter, useRoute} from "vue-router"
import {useLogin} from "./login/login.ts"
import {useMenuConfig} from "./composeables/menu-config.ts"
import {NMenu, NButton, NBadge} from "naive-ui"
import type {MenuOption} from "naive-ui"
import {logo} from "@syl/icon"
import {useTransferStore} from "@/store/useTransferStore"

const router = useRouter()
const route = useRoute() // 引入 route 用于菜单高亮
const {verify} = useLogin()
const {menuConfig} = useMenuConfig()
const transferStore = useTransferStore()

const userInfo = ref<any>(null)

// 2. 数据转换：将你的 menuConfig 转为 Naive UI 要求的 MenuOption 格式
const mapMenus = (menus: any[]): MenuOption[] => {
  return menus.map(item => {
    const option: MenuOption = {
      label: item.name,
      key: item.path, // 使用 path 作为唯一 key
    }
    // 如果有子节点，递归处理
    if (item.children && item.children.length > 0) {
      option.children = mapMenus(item.children)
    }
    return option
  })
}

// 使用 computed 确保数据响应式
const menuOptions = computed(() => mapMenus(menuConfig || []))

// 自动匹配当前路由高亮菜单
const activeKey = computed(() => route.path)

onMounted(async () => {
  try {
    await verify()
    const saved = localStorage.getItem("userInfo")
    if (saved) userInfo.value = JSON.parse(saved)
  } catch {
    localStorage.removeItem("token")
    await router.push("/login")
    return
  }
  // 初始化传输状态监听（Tauri 模式起事件监听 + 引擎状态轮询）
  transferStore.init()
})
onBeforeUnmount(() => {
  transferStore.dispose()
})

const ind = computed(() => transferStore.indicator)
const goTransfers = () => router.push("/transfers")

const handleLogout = () => {
  localStorage.removeItem("token")
  localStorage.removeItem("userInfo")
  router.push("/login")
}
const handleHome = () => {
  router.push("/")
}

// 3. 菜单点击事件：Naive UI 会直接传入对应的 key (即 item.path)
const handleMenuClick = (key: string) => {
  router.push(key)
}
</script>

<template>
  <div class="layout">
    <div class="header">
      <div class="header-left">
        <div class="logo" @click="handleHome">
          <img :src="logo" alt="logo"/>
        </div>

        <n-menu
            mode="horizontal"
            :value="activeKey"
            :options="menuOptions"
            @update:value="handleMenuClick"
        />
      </div>

      <div class="header-right">
        <div class="transfer-indicator" @click="goTransfers" title="传输列表">
          <n-badge :value="ind.count" :max="99" :show="ind.count > 0" type="error">
            <!-- 同步中：转圈 -->
            <el-icon v-if="ind.type === 'sync'" class="spin ind-sync"><Loading /></el-icon>
            <!-- 上传中：箭头向上 -->
            <el-icon v-else-if="ind.type === 'upload'" class="ind-upload"><Upload /></el-icon>
            <!-- 下载中：箭头向下 -->
            <el-icon v-else-if="ind.type === 'download'" class="ind-download"><Download /></el-icon>
            <!-- 空闲 -->
            <el-icon v-else class="ind-idle"><Files /></el-icon>
          </n-badge>
        </div>
        <span v-if="userInfo" class="username">{{ userInfo.username }}</span>
        <n-button type="primary" size="small" @click="handleLogout">
          退出登录
        </n-button>
      </div>
    </div>

    <div class="content">
      <router-view/>
    </div>
  </div>
</template>

<style scoped>
.layout {
  width: 100%;
  height: 100vh;
  display: flex;
  flex-direction: column;
}

.header {
  height: 60px;
  background: white;
  border-bottom: 1px solid #e8e8e8;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
}

.header-left {
  display: flex;
  align-items: center;
  gap: 32px;
}

.logo {
  font-size: 20px;
  font-weight: bold;
  color: #1890ff; /* 后续你可以用 Naive UI 的主题变量替换这里的硬编码颜色 */
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;

  img {
    width: 50px;
    height: 50px;
    object-fit: contain;
  }
}

.header-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.transfer-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  cursor: pointer;
  transition: background-color 0.2s;
}
.transfer-indicator:hover {
  background-color: #f0f2f5;
}
.transfer-indicator .el-icon {
  font-size: 18px;
}
.ind-idle { color: #909399; }
.ind-upload { color: #e6a23c; }
.ind-download { color: #409eff; }
.ind-sync { color: #67c23a; }

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
.spin {
  animation: spin 1s linear infinite;
}

.username {
  color: #666;
  font-size: 14px;
}

.content {
  flex: 1;
  background: #f0f2f5;
  padding: 20px;
  overflow: auto;
}

/* 所有关于 dropdown、hover、arrow 动画的恶心 CSS 都可以删掉了！
  Naive UI 内部已经处理好了绝佳的过渡动画和阴影。
*/
</style>