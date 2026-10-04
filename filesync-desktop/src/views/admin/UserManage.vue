<script setup lang="ts">
// 用户管理：查看 / 启停用户、调整级别（仅超级管理员）、重置密码、删除；超级管理员可在此开关自助注册。
import {h, onMounted, ref} from 'vue'
import {
  NButton, NSpace, NTag, NInput, NSelect, NSwitch, NPagination, NDataTable, NEmpty, NText,
  NModal, NCard, NForm, NFormItem, NAlert, useDialog, useMessage,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {listUsers, updateUser, resetUserPassword, deleteUser} from '@/api/admin/adminApi'
import type {AdminUser} from '@/api/admin/adminTypes'
import {getSettings, setRegisterOpen, setUserLevel, createUser} from '@/api/system/systemApi'
import {currentLevel, currentUserId, LEVEL_TEXT} from '@/utils/level'

const message = useMessage()
const dialog = useDialog()

const myLevel = currentLevel()
const myId = currentUserId()
const isSuper = myLevel >= 3

const list = ref<AdminUser[]>([])
const loading = ref(false)
const total = ref(0)
const pageNum = ref(1)
const pageSize = ref(20)
const keyword = ref('')

const allowRegister = ref(false)
const registerLoading = ref(false)

const fetchList = async () => {
  loading.value = true
  try {
    const data = await listUsers({keyword: keyword.value.trim(), page: pageNum.value, page_size: pageSize.value})
    list.value = data.list || []
    total.value = data.total || 0
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

const fetchSettings = async () => {
  try {
    allowRegister.value = (await getSettings()).allow_register
  } catch { /* 读不到不影响用户列表 */ }
}

onMounted(() => {
  fetchList()
  fetchSettings()
})

const onSearch = () => {
  pageNum.value = 1
  fetchList()
}
const onPageChange = (p: number) => {
  pageNum.value = p
  fetchList()
}

const toggleRegister = async (v: boolean) => {
  registerLoading.value = true
  try {
    const r = await setRegisterOpen(v)
    allowRegister.value = r.allow_register
    message.success(v ? '已开放自助注册' : '已关闭自助注册')
  } catch (e) {
    message.error(String(e))
  } finally {
    registerLoading.value = false
  }
}

/** 管理员(2)不能动管理员及以上；超级管理员能动所有人；自己永远可以（用于改自己的资料类操作） */
const canManage = (u: AdminUser) => u.id === myId || isSuper || u.level < 2
/** 访客只在「访客管理」里处理，这里不提供级别 / 密码 / 删除类操作 */
const isGuest = (u: AdminUser) => u.level === 0

const toggleStatus = async (u: AdminUser, v: boolean) => {
  try {
    await updateUser(u.id, {status: v ? 1 : 0})
    message.success(v ? '已启用' : '已禁用')
  } catch (e) {
    message.error(String(e))
  } finally {
    fetchList()
  }
}

const changeLevel = async (u: AdminUser, level: number) => {
  try {
    await setUserLevel(u.id, level)
    message.success(`已将 ${u.username} 设为${LEVEL_TEXT[level]}（对方重新登录后生效）`)
  } catch (e) {
    message.error(String(e))
  } finally {
    fetchList()
  }
}

const resetTarget = ref<AdminUser | null>(null)
const newPassword = ref('')
const resetLoading = ref(false)
const openReset = (u: AdminUser) => {
  resetTarget.value = u
  newPassword.value = ''
}
const doReset = async () => {
  if (!resetTarget.value) return
  if (newPassword.value.length < 6) {
    message.warning('新密码至少 6 位')
    return
  }
  resetLoading.value = true
  try {
    await resetUserPassword(resetTarget.value.id, newPassword.value)
    message.success('密码已重置，对方已被强制下线')
    resetTarget.value = null
  } catch (e) {
    message.error(String(e))
  } finally {
    resetLoading.value = false
  }
}

const handleDelete = (u: AdminUser) => {
  dialog.warning({
    title: '删除用户',
    content: `确认删除「${u.username}」？只删除账号，不会动他的文件和同步记录。此操作不可撤销。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteUser(u.id)
        message.success('已删除')
        fetchList()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

// 超级管理员全系统只有一个（初始化时产生），不能再设置，所以这里没有它
const levelOptions = [
  {label: '用户', value: 1},
  {label: '管理员', value: 2},
]

// ── 新建账号 ───────────────────────────────────────────────
// 自助注册关闭后，这是新增用户的唯一途径。管理员只能建用户；超级管理员还能建管理员。
const showCreate = ref(false)
const creating = ref(false)
const createForm = ref({username: '', password: '', email: '', level: 1})
const createdAccount = ref<{ username: string; password: string } | null>(null)
const createLevelOptions = isSuper ? levelOptions : levelOptions.filter((o) => o.value === 1)

const openCreate = () => {
  createForm.value = {username: '', password: '', email: '', level: 1}
  showCreate.value = true
}
const doCreate = async () => {
  const f = createForm.value
  if (f.username.trim().length < 3) {
    message.warning('用户名至少 3 位')
    return
  }
  if (f.password && f.password.length < 8) {
    message.warning('密码至少 8 位（留空则自动生成）')
    return
  }
  creating.value = true
  try {
    const r = await createUser({
      username: f.username.trim(), password: f.password || undefined,
      email: f.email.trim() || undefined, level: f.level,
    })
    showCreate.value = false
    // 自动生成的密码只返回这一次；自己指定的不回显
    createdAccount.value = r.password ? {username: r.username, password: r.password} : null
    message.success(`已创建${LEVEL_TEXT[r.level]}「${r.username}」`)
    fetchList()
  } catch (e) {
    message.error(String(e))
  } finally {
    creating.value = false
  }
}
const copyText = async (text: string) => {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.warning('复制失败，请手动选中复制')
  }
}
const tagType = (level: number) => (['default', 'info', 'warning', 'error'] as const)[level] ?? 'default'

const columns: DataTableColumns<AdminUser> = [
  {title: 'ID', key: 'id', width: 60},
  {title: '用户名', key: 'username', width: 140},
  {title: '邮箱', key: 'email', render: (u) => u.email || '-'},
  {
    title: '级别', key: 'level', width: 150,
    render(u) {
      // 超级管理员可改非访客、非自己的级别；其余人只读
      if (isSuper && !isGuest(u) && u.id !== myId) {
        return h(NSelect, {
          size: 'small', value: u.level, options: levelOptions,
          'onUpdate:value': (v: number) => changeLevel(u, v),
        })
      }
      return h(NTag, {size: 'small', type: tagType(u.level)}, {default: () => LEVEL_TEXT[u.level] ?? '用户'})
    },
  },
  {
    title: '状态', key: 'status', width: 80,
    render(u) {
      return h(NSwitch, {
        size: 'small', value: u.status === 1,
        disabled: u.id === myId || !canManage(u),
        'onUpdate:value': (v: boolean) => toggleStatus(u, v),
      })
    },
  },
  {
    title: '在线', key: 'online', width: 70,
    render: (u) => h(NTag, {size: 'small', type: u.online ? 'success' : 'default'}, {default: () => (u.online ? '在线' : '离线')}),
  },
  {title: '最近登录', key: 'last_login', width: 170, render: (u) => u.last_login || '-'},
  {
    title: '操作', key: 'actions', width: 160,
    render(u) {
      if (isGuest(u)) return h(NText, {depth: 3}, {default: () => '在访客管理中处理'})
      if (!canManage(u)) return h(NText, {depth: 3}, {default: () => '无权操作'})
      return h(NSpace, {size: 6}, {
        default: () => [
          h(NButton, {size: 'small', onClick: () => openReset(u)}, {default: () => '重置密码'}),
          u.id === myId ? null : h(NButton, {size: 'small', type: 'error', ghost: true, onClick: () => handleDelete(u)}, {default: () => '删除'}),
        ],
      })
    },
  },
]
</script>

<template>
  <div class="manage-container">
    <n-space align="center" justify="space-between" class="toolbar">
      <div class="title">用户管理</div>
      <n-space align="center">
        <n-space v-if="isSuper" align="center" :size="8">
          <n-text>开放自助注册</n-text>
          <n-switch :value="allowRegister" :loading="registerLoading" @update:value="toggleRegister"/>
        </n-space>
        <n-input v-model:value="keyword" placeholder="按用户名 / 邮箱 / 手机号搜索" clearable style="width: 240px"
                 @keyup.enter="onSearch" @clear="onSearch"/>
        <n-button size="small" @click="onSearch">查询</n-button>
        <n-button size="small" @click="fetchList">刷新</n-button>
        <n-button size="small" type="primary" @click="openCreate">新建账号</n-button>
      </n-space>
    </n-space>

    <n-data-table :columns="columns" :data="list" :loading="loading" :row-key="(r: AdminUser) => r.id"
                  :bordered="false" striped size="small">
      <template #empty>
        <n-empty description="没有用户"/>
      </template>
    </n-data-table>

    <div class="pager">
      <n-pagination :page="pageNum" :page-size="pageSize" :item-count="total" @update:page="onPageChange"/>
    </div>

    <!-- 新建账号 -->
    <n-modal v-model:show="showCreate">
      <n-card style="width: 440px" title="新建账号" closable @close="showCreate = false">
        <n-form label-placement="left" label-width="70">
          <n-form-item label="用户名">
            <n-input v-model:value="createForm.username" placeholder="至少 3 位"/>
          </n-form-item>
          <n-form-item label="密码">
            <n-input v-model:value="createForm.password" type="password" show-password-on="click"
                     placeholder="留空则自动生成（至少 8 位）"/>
          </n-form-item>
          <n-form-item label="邮箱">
            <n-input v-model:value="createForm.email" placeholder="可选"/>
          </n-form-item>
          <n-form-item label="级别">
            <n-select v-model:value="createForm.level" :options="createLevelOptions" style="width: 160px"/>
            <n-text v-if="!isSuper" depth="3" style="margin-left: 10px">管理员只能创建用户</n-text>
          </n-form-item>
        </n-form>
        <template #footer>
          <n-space justify="end">
            <n-button @click="showCreate = false">取消</n-button>
            <n-button type="primary" :loading="creating" @click="doCreate">创建</n-button>
          </n-space>
        </template>
      </n-card>
    </n-modal>

    <!-- 自动生成的密码（只显示这一次） -->
    <n-modal :show="!!createdAccount" @update:show="(v: boolean) => { if (!v) createdAccount = null }">
      <n-card style="width: 420px" title="账号已创建" closable @close="createdAccount = null">
        <n-alert type="warning" :show-icon="false" style="margin-bottom: 12px">
          密码只显示这一次，关闭后无法再查看（忘记了只能重置）。请转交给对方，并建议其登录后尽快修改。
        </n-alert>
        <n-space vertical>
          <n-input readonly :value="createdAccount?.username">
            <template #prefix><n-text depth="3">账号</n-text></template>
          </n-input>
          <n-input readonly :value="createdAccount?.password">
            <template #prefix><n-text depth="3">密码</n-text></template>
          </n-input>
        </n-space>
        <template #footer>
          <n-space justify="end">
            <n-button @click="copyText(`账号：${createdAccount?.username}\n密码：${createdAccount?.password}`)">复制账号和密码</n-button>
            <n-button type="primary" @click="createdAccount = null">我已保存</n-button>
          </n-space>
        </template>
      </n-card>
    </n-modal>

    <n-modal :show="!!resetTarget" @update:show="(v: boolean) => { if (!v) resetTarget = null }">
      <n-card style="width: 380px" :title="`重置「${resetTarget?.username}」的密码`" closable
              @close="resetTarget = null">
        <n-input v-model:value="newPassword" type="password" show-password-on="click" placeholder="新密码（至少 6 位）"
                 @keyup.enter="doReset"/>
        <template #footer>
          <n-space justify="end">
            <n-button @click="resetTarget = null">取消</n-button>
            <n-button type="primary" :loading="resetLoading" @click="doReset">确认重置</n-button>
          </n-space>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>

<style scoped>
.manage-container {
  padding: 20px;
}

.toolbar {
  margin-bottom: 16px;
}

.title {
  font-size: 18px;
  font-weight: 600;
}

.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
