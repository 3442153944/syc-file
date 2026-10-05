<script setup lang="ts">
import {onMounted, onBeforeUnmount, ref, computed} from "vue"
import {useRouter, useRoute} from "vue-router"
import {useLogin} from "./login/login.ts"
import {pinia} from "@/store/useStore"
import {useRouteStore} from "@/store/useRouteStore"
import type {MenuNode} from "@/router/dynamic"
import {NMenu, NBadge, NDropdown, NAvatar, NIcon, NTag} from "naive-ui"
import type {MenuOption, DropdownOption} from "naive-ui"
import {logo} from "@syl/icon"
import {useTransferStore} from "@/store/useTransferStore"
import {avatarUrl} from "@/api/platform"
import {setServerUrl} from "@/api/platform"
import {isTauri} from "@tauri-apps/api/core"
import {
  User as UserIcon,
  Cog as CogIcon,
  CircleNotch as SyncingIcon,
  Upload as UploadIcon,
  Download as DownloadIcon,
  Copy as FilesIcon,
} from '@vicons/fa'
import NodeSwitcher from '@/components/NodeSwitcher.vue'

const router = useRouter()
const route = useRoute() // 引入 route 用于菜单高亮
const {verify} = useLogin()
const rs = useRouteStore(pinia)
const transferStore = useTransferStore()

const userInfo = ref<any>(null)
const userAvatar = computed(() => avatarUrl(userInfo.value?.avatar))
// 节点面板挂在 NodeSwitcher 内部（登录页也要用），齿轮图标通过 ref 复用它
const nodeSwitcher = ref<InstanceType<typeof NodeSwitcher> | null>(null)

// 2. 菜单来自服务器下发的路由表（store/useRouteStore → router/dynamic.buildMenu），按级别 / 授权已过滤
const mapMenus = (nodes: MenuNode[]): MenuOption[] => {
  return nodes.map(n => {
    const option: MenuOption = {label: n.title, key: n.key}
    if (n.children && n.children.length > 0) option.children = mapMenus(n.children)
    return option
  })
}

const menuOptions = computed(() => mapMenus(rs.menu))

// 自动匹配当前路由高亮菜单
const activeKey = computed(() => route.path)

onMounted(async () => {
  try {
    await verify()
    const saved = localStorage.getItem("userInfo")
    if (saved) userInfo.value = JSON.parse(saved)

    // 同步服务器地址（头像 URL 拼接用）
    if (isTauri()) {
      try {
        const {invoke} = await import('@tauri-apps/api/core')
        const cfg = await invoke<any>('get_sync_config')
        if (cfg?.server_url) setServerUrl(cfg.server_url)
      } catch { /* ignore */
      }
    }
  } catch {
    localStorage.removeItem("token")
    await router.push("/login")
    return
  }
  transferStore.init()
  // 管理员改了路由表后，在线客户端几分钟内自动跟上（版本号没变则什么都不做）
  refreshTimer = setInterval(async () => {
    await rs.refresh(router)
    // 访客到期 / 被禁用 / token 失效：回登录页
    if (!rs.loaded && rs.loadError === 'unauthorized') {
      localStorage.removeItem("token")
      localStorage.removeItem("userInfo")
      rs.reset(router)
      router.push("/login")
    }
  }, 5 * 60 * 1000)
})
let refreshTimer: ReturnType<typeof setInterval> | null = null
onBeforeUnmount(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  transferStore.dispose()
})

const ind = computed(() => transferStore.indicator)
const goTransfers = () => router.push("/transfers")
const goPerson = () => router.push("/person/center")

const userDropdownOptions: DropdownOption[] = [
  {label: '个人中心', key: 'center'},
  // 桌面端：日志窗口。Linux 不挂原生顶部菜单栏（GNOME + Wayland 下会让 GTK 栈溢出崩溃），入口放在这里
  ...(isTauri() ? [{label: '日志窗口', key: 'logs'}] : []),
  {label: '退出登录', key: 'logout'},
]

const openLogWindow = async () => {
  const {invoke} = await import('@tauri-apps/api/core')
  await invoke('open_log_window_cmd')
}

const handleUserDropdown = (key: string) => {
  if (key === 'center') goPerson()
  else if (key === 'logs') openLogWindow()
  else if (key === 'logout') handleLogout()
}

const handleLogout = () => {
  localStorage.removeItem("token")
  localStorage.removeItem("userInfo")
  rs.reset(router)
  router.push("/login")
}
const handleHome = () => {
  router.push("/")
}

// 3. 菜单点击事件：Naive UI 会直接传入对应的 key (即 item.path)
const handleMenuClick = (key: string) => {
  if (key.startsWith("dir:")) return // 菜单分组本身不跳转
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

        <!-- responsive：放不下的菜单项自动收进「…」，不再把右侧区域挤出窗口 -->
        <n-menu
            class="header-menu"
            mode="horizontal"
            responsive
            :value="activeKey"
            :options="menuOptions"
            @update:value="handleMenuClick"
        />
      </div>

      <div class="header-right">
        <!-- 网络节点指示器：小圆点显示当前节点通不通，点开可直接切换 -->
        <NodeSwitcher ref="nodeSwitcher"/>
        <div class="transfer-indicator" @click="goTransfers" title="传输列表">
          <n-badge :value="ind.count" :max="99" :show="ind.count > 0" type="error">
            <!-- 同步中：转圈 -->
            <n-icon v-if="ind.type === 'sync'" :size="18" class="spin ind-sync">
              <SyncingIcon/>
            </n-icon>
            <!-- 上传中：箭头向上 -->
            <n-icon v-else-if="ind.type === 'upload'" :size="18" class="ind-upload">
              <UploadIcon/>
            </n-icon>
            <!-- 下载中：箭头向下 -->
            <n-icon v-else-if="ind.type === 'download'" :size="18" class="ind-download">
              <DownloadIcon/>
            </n-icon>
            <!-- 空闲 -->
            <n-icon v-else :size="18" class="ind-idle">
              <FilesIcon/>
            </n-icon>
          </n-badge>
        </div>
        <n-dropdown trigger="click" :options="userDropdownOptions" @select="handleUserDropdown">
          <div class="user-area">
            <n-avatar :size="28" round :src="userAvatar" v-if="userInfo">
              <template #fallback>
                <n-icon :size="16">
                  <UserIcon/>
                </n-icon>
              </template>
            </n-avatar>
            <span v-if="userInfo" class="username">{{ userInfo.username }}</span>
            <n-tag v-if="userInfo && userInfo.level === 0" size="small" type="warning">访客</n-tag>
          </div>
        </n-dropdown>
        <div class="settings-icon" v-if="isTauri()" @click="nodeSwitcher?.open()" title="网络节点设置">
          <n-icon :size="18" color="#666" style="cursor:pointer">
            <CogIcon/>
          </n-icon>
        </div>
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

/* 左侧占满剩余宽度并允许收缩（min-width: 0），菜单才有机会触发 responsive 折叠 */
.header-left {
  display: flex;
  align-items: center;
  gap: 20px;
  flex: 1;
  min-width: 0;
}

.header-menu {
  flex: 1;
  min-width: 0;
}

.logo {
  flex-shrink: 0;
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

/* 右侧（节点 / 传输 / 用户 / 设置）永远完整显示，不被菜单挤压 */
.header-right {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
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

.ind-idle {
  color: #909399;
}

.ind-upload {
  color: #e6a23c;
}

.ind-download {
  color: #409eff;
}

.ind-sync {
  color: #67c23a;
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

.spin {
  animation: spin 1s linear infinite;
}

.username {
  color: #666;
  font-size: 14px;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 窗口变窄时先收起文字，只留图标 / 头像 */
@media (max-width: 960px) {
  .username {
    display: none;
  }
}

@media (max-width: 820px) {
  .header {
    padding: 0 12px;
  }

  .header-right :deep(.node-name) {
    display: none;
  }
}

.user-area {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 6px;
  transition: background-color 0.2s;
}

.user-area:hover {
  background-color: #f0f2f5;
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