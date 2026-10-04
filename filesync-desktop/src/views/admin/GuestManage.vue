<script setup lang="ts">
// 访客管理：管理员下发临时账号，指定有效期和可访问的页面。访客只能进被勾选的页面，接口也只放行这些页面用到的。
import {computed, h, onMounted, ref} from 'vue'
import {
  NButton, NSpace, NTag, NDataTable, NEmpty, NModal, NCard, NForm, NFormItem, NInputNumber,
  NCheckbox, NCheckboxGroup, NAlert, NInput, NText, useDialog, useMessage,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {
  listGuests, listGuestRoutes, createGuest, updateGuest, deleteGuest,
} from '@/api/system/systemApi'
import type {GuestRow, GuestCreated, RouteItem} from '@/api/system/systemTypes'

const message = useMessage()
const dialog = useDialog()

const guests = ref<GuestRow[]>([])
const pages = ref<RouteItem[]>([])
const loading = ref(false)

const fetchAll = async () => {
  loading.value = true
  try {
    guests.value = await listGuests()
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  fetchAll()
  try {
    pages.value = await listGuestRoutes()
  } catch (e) {
    message.error('获取可授权页面失败：' + String(e))
  }
})

/** 可授权页面按所属分组展示：分组标题取自父级路由（目录不在 pages 里，用 parent_code 兜底成文案） */
const titleOf = (code: string) => pages.value.find((p) => p.code === code)?.title ?? code
const groups = computed(() => {
  const m = new Map<string, RouteItem[]>()
  for (const p of pages.value) {
    const g = p.parent_code || '_'
    if (!m.has(g)) m.set(g, [])
    m.get(g)!.push(p)
  }
  return [...m.entries()].map(([code, items]) => ({code, items}))
})
const groupLabel: Record<string, string> = {
  _: '通用', file: '文件管理', sync: '文件同步', monitor: '系统监控', admin: '系统管理', update: '应用更新',
}

// ── 新建 / 编辑授权 ────────────────────────────────────────
const showForm = ref(false)
const editing = ref<GuestRow | null>(null) // null = 新建
const form = ref({hours: 24, codes: [] as string[]})
const saving = ref(false)

const openCreate = () => {
  editing.value = null
  form.value = {hours: 24, codes: []}
  showForm.value = true
}
const openEdit = (g: GuestRow) => {
  editing.value = g
  form.value = {hours: 24, codes: [...g.route_codes]}
  showForm.value = true
}

const created = ref<GuestCreated | null>(null)

const save = async () => {
  if (form.value.codes.length === 0) {
    message.warning('至少勾选一个可访问的页面')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateGuest(editing.value.id, {route_codes: form.value.codes})
      message.success('已更新授权页面')
    } else {
      created.value = await createGuest({expire_hours: form.value.hours, route_codes: form.value.codes})
    }
    showForm.value = false
    fetchAll()
  } catch (e) {
    message.error(String(e))
  } finally {
    saving.value = false
  }
}

const copy = async (text: string) => {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.warning('复制失败，请手动选中复制')
  }
}

// ── 续期 / 启停 / 重置密码 / 删除 ──────────────────────────
const renewTarget = ref<GuestRow | null>(null)
const renewHours = ref(24)
const doRenew = async () => {
  if (!renewTarget.value) return
  try {
    await updateGuest(renewTarget.value.id, {expire_hours: renewHours.value})
    message.success(`已续期，从现在起 ${renewHours.value} 小时有效`)
    renewTarget.value = null
    fetchAll()
  } catch (e) {
    message.error(String(e))
  }
}

const toggle = async (g: GuestRow) => {
  try {
    await updateGuest(g.id, {status: g.status === 1 ? 0 : 1})
    message.success(g.status === 1 ? '已禁用，对方立即失效' : '已启用')
    fetchAll()
  } catch (e) {
    message.error(String(e))
  }
}

const resetPw = async (g: GuestRow) => {
  try {
    const r = await updateGuest(g.id, {reset_password: true})
    created.value = {id: g.id, username: g.username, password: r.password || '', expires_at: g.expires_at || '', route_codes: g.route_codes}
  } catch (e) {
    message.error(String(e))
  }
}

const remove = (g: GuestRow) => {
  dialog.warning({
    title: '删除访客',
    content: `确认删除访客「${g.username}」？对方会立即下线，此操作不可撤销。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteGuest(g.id)
        message.success('已删除')
        fetchAll()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

const fmt = (s: string | null) => (s ? new Date(s).toLocaleString('zh-CN', {hour12: false}) : '-')

const columns: DataTableColumns<GuestRow> = [
  {title: '账号', key: 'username', width: 130},
  {
    title: '状态', key: 'status', width: 90,
    render(g) {
      if (g.expired) return h(NTag, {size: 'small', type: 'error'}, {default: () => '已过期'})
      if (g.status !== 1) return h(NTag, {size: 'small', type: 'warning'}, {default: () => '已禁用'})
      return h(NTag, {size: 'small', type: 'success'}, {default: () => '有效'})
    },
  },
  {title: '到期时间', key: 'expires_at', width: 170, render: (g) => fmt(g.expires_at)},
  {
    title: '可访问页面', key: 'route_codes',
    render: (g) => h(NSpace, {size: 4}, {
      default: () => g.route_codes.map((c) => h(NTag, {size: 'small'}, {default: () => titleOf(c)})),
    }),
  },
  {title: '最近登录', key: 'last_login', width: 170, render: (g) => fmt(g.last_login)},
  {
    title: '操作', key: 'actions', width: 330,
    render: (g) => h(NSpace, {size: 6}, {
      default: () => [
        h(NButton, {size: 'small', onClick: () => { renewTarget.value = g; renewHours.value = 24 }}, {default: () => '续期'}),
        h(NButton, {size: 'small', onClick: () => openEdit(g)}, {default: () => '改授权'}),
        h(NButton, {size: 'small', onClick: () => toggle(g)}, {default: () => (g.status === 1 ? '禁用' : '启用')}),
        h(NButton, {size: 'small', onClick: () => resetPw(g)}, {default: () => '重置密码'}),
        h(NButton, {size: 'small', type: 'error', ghost: true, onClick: () => remove(g)}, {default: () => '删除'}),
      ],
    }),
  },
]
</script>

<template>
  <div class="manage-container">
    <n-space align="center" justify="space-between" class="toolbar">
      <div class="title">访客管理</div>
      <n-space>
        <n-button size="small" @click="fetchAll">刷新</n-button>
        <n-button size="small" type="primary" @click="openCreate">新建访客</n-button>
      </n-space>
    </n-space>

    <n-alert type="info" :show-icon="false" class="tip">
      访客是临时账号：只能进入你勾选的页面，接口也只放行这些页面用到的；到期或被禁用后立即失效。
      管理员只能管理自己下发的访客，超级管理员可管理全部。
    </n-alert>

    <n-data-table :columns="columns" :data="guests" :loading="loading" :row-key="(r: GuestRow) => r.id"
                  :bordered="false" striped size="small">
      <template #empty>
        <n-empty description="还没有下发过访客账号"/>
      </template>
    </n-data-table>

    <!-- 新建 / 改授权 -->
    <n-modal v-model:show="showForm">
      <n-card style="width: 520px" :title="editing ? `修改「${editing.username}」的授权页面` : '新建访客'" closable
              @close="showForm = false">
        <n-form label-placement="top">
          <n-form-item v-if="!editing" label="有效期（小时，最长 720）">
            <n-input-number v-model:value="form.hours" :min="1" :max="720" style="width: 160px"/>
          </n-form-item>
          <n-form-item label="可访问的页面">
            <n-checkbox-group v-model:value="form.codes" style="width: 100%">
              <div v-for="g in groups" :key="g.code" class="page-group">
                <div class="page-group-title">{{ groupLabel[g.code] ?? g.code }}</div>
                <n-space>
                  <n-checkbox v-for="p in g.items" :key="p.code" :value="p.code" :label="p.title"/>
                </n-space>
              </div>
            </n-checkbox-group>
          </n-form-item>
        </n-form>
        <template #footer>
          <n-space justify="end">
            <n-button @click="showForm = false">取消</n-button>
            <n-button type="primary" :loading="saving" @click="save">{{ editing ? '保存' : '创建' }}</n-button>
          </n-space>
        </template>
      </n-card>
    </n-modal>

    <!-- 续期 -->
    <n-modal :show="!!renewTarget" @update:show="(v: boolean) => { if (!v) renewTarget = null }">
      <n-card style="width: 360px" :title="`续期「${renewTarget?.username}」`" closable @close="renewTarget = null">
        <n-form-item label="从现在起有效（小时）">
          <n-input-number v-model:value="renewHours" :min="1" :max="720" style="width: 160px"/>
        </n-form-item>
        <template #footer>
          <n-space justify="end">
            <n-button @click="renewTarget = null">取消</n-button>
            <n-button type="primary" @click="doRenew">确认</n-button>
          </n-space>
        </template>
      </n-card>
    </n-modal>

    <!-- 账号 / 密码（只显示这一次） -->
    <n-modal :show="!!created" @update:show="(v: boolean) => { if (!v) created = null }">
      <n-card style="width: 420px" title="访客账号" closable @close="created = null">
        <n-alert type="warning" :show-icon="false" style="margin-bottom: 12px">
          密码只显示这一次，关闭后无法再查看（忘记了只能重置）。请把账号和密码转交给访客。
        </n-alert>
        <n-space vertical>
          <n-input readonly :value="created?.username">
            <template #prefix><n-text depth="3">账号</n-text></template>
          </n-input>
          <n-input readonly :value="created?.password">
            <template #prefix><n-text depth="3">密码</n-text></template>
          </n-input>
          <n-text v-if="created?.expires_at" depth="3">有效期至 {{ fmt(created.expires_at) }}</n-text>
        </n-space>
        <template #footer>
          <n-space justify="end">
            <n-button @click="copy(`账号：${created?.username}\n密码：${created?.password}`)">复制账号和密码</n-button>
            <n-button type="primary" @click="created = null">我已保存</n-button>
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
  margin-bottom: 12px;
}

.title {
  font-size: 18px;
  font-weight: 600;
}

.tip {
  margin-bottom: 16px;
}

.page-group {
  margin-bottom: 10px;
}

.page-group-title {
  font-size: 12px;
  color: #999;
  margin-bottom: 4px;
}
</style>
