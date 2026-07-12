<script setup lang="ts">
// 个人中心：查看当前资料，提供编辑入口
import {ref, onMounted, computed} from 'vue'
import {useRouter} from 'vue-router'
import {NCard, NButton, NAvatar, NSpace, NDivider, NDescriptions, NDescriptionsItem, NIcon} from 'naive-ui'
import {User, Edit, Lock, ArrowLeft} from '@vicons/fa'
import type {UserInfo} from '@/api/user/userTypes'
import {verify as apiVerify} from '@/api/user/userApi'
import {avatarUrl} from '@/api/platform'

const router = useRouter()
const user = ref<UserInfo | null>(null)
const avatar = computed(() => avatarUrl(user.value?.avatar))

onMounted(async () => {
  const saved = localStorage.getItem('userInfo')
  if (saved) {
    try {
      user.value = JSON.parse(saved)
    } catch { /* ignore */
    }
  }
  // 拉最新资料
  try {
    const res = await apiVerify()
    if ((res as any).user) {
      user.value = (res as any).user
      localStorage.setItem('userInfo', JSON.stringify(user.value))
    }
  } catch { /* 失败用缓存的 */
  }
})
</script>

<template>
  <div class="person-center">
    <n-button text @click="router.back()" style="margin-bottom:16px">
      <template #icon>
        <n-icon>
          <ArrowLeft/>
        </n-icon>
      </template>
      返回
    </n-button>

    <n-card title="个人中心" size="small">
      <n-space vertical align="center" style="width:100%">
        <n-avatar :size="80" round :src="avatar">
          <template #fallback>
            <n-icon size="48">
              <User/>
            </n-icon>
          </template>
        </n-avatar>
        <h2 style="margin:8px 0 0">{{ user?.username || '-' }}</h2>
        <span style="color:#999;font-size:13px">{{ user?.role === 'admin' ? '管理员' : '普通用户' }}</span>
      </n-space>
    </n-card>

    <n-card title="详细信息" size="small">
      <n-descriptions label-placement="left" :column="1" bordered size="small">
        <n-descriptions-item label="用户名">{{ user?.username || '-' }}</n-descriptions-item>
        <n-descriptions-item label="邮箱">{{ user?.email || '未设置' }}</n-descriptions-item>
        <n-descriptions-item label="手机">{{ user?.phone || '未设置' }}</n-descriptions-item>
        <n-descriptions-item label="注册时间">{{ user?.created_at || '-' }}</n-descriptions-item>
      </n-descriptions>
    </n-card>

    <n-divider/>

    <n-space vertical style="width:100%">
      <n-button type="primary" @click="router.push('/person/edit')" block>
        <template #icon>
          <n-icon>
            <Edit/>
          </n-icon>
        </template>
        编辑资料
      </n-button>
      <n-button @click="router.push('/person/password')" block>
        <template #icon>
          <n-icon>
            <Lock/>
          </n-icon>
        </template>
        修改密码
      </n-button>
    </n-space>
  </div>
</template>

<style scoped>
.person-center {
  max-width: 480px;
  margin: 0 auto;
}
</style>
