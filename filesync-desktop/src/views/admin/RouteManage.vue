<script setup lang="ts">
// 路由管理（仅超级管理员）：决定客户端有哪些菜单入口、叫什么、排在哪、哪个级别以上可见，并能新增 / 删除自定义入口。
//
// 两类路由：
//   - 内置：来自服务端代码里的内置目录，只能停用或调整展示（标题 / 图标 / 排序 / 最低级别），不能删，不能改路径和组件；
//   - 自定义：在这里新增的，可以随意编辑和删除。
// 页面代码在客户端里：「页面」类型的路由必须从客户端现有的页面组件里选（列表自动取自 src/views/**.vue）。
// 要做一个全新的页面：先在客户端 src/views 下新增 .vue 并发布客户端，再回到这里「新增路由」选择它。
import {computed, h, onMounted, ref} from 'vue'
import {
  NButton, NSpace, NTag, NSwitch, NDataTable, NEmpty, NModal, NCard, NForm, NFormItem, NInput,
  NInputNumber, NSelect, NAlert, NText, NRadioGroup, NRadioButton, useDialog, useMessage,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {
  adminListRoutes, adminUpdateRoute, adminCreateRoute, adminDeleteRoute, adminRoutePerms,
} from '@/api/system/systemApi'
import type {AdminRoute, RoutePerm} from '@/api/system/systemTypes'
import {LEVEL_TEXT} from '@/utils/level'
import {hasComponent, componentKeys} from '@/router/dynamic'

const message = useMessage()
const dialog = useDialog()

const rows = ref<AdminRoute[]>([])
const perms = ref<RoutePerm[]>([])
const loading = ref(false)

const fetchAll = async () => {
  loading.value = true
  try {
    rows.value = await adminListRoutes()
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  fetchAll()
  try {
    perms.value = await adminRoutePerms()
  } catch { /* 取不到只是下拉里没有选项，不影响其它操作 */ }
})

const patch = async (r: AdminRoute, p: Parameters<typeof adminUpdateRoute>[1]) => {
  try {
    await adminUpdateRoute(r.id, p)
    message.success('已保存（在线客户端几分钟内自动刷新菜单）')
  } catch (e) {
    message.error(String(e))
  } finally {
    fetchAll()
  }
}

// ── 新增 / 编辑 ────────────────────────────────────────────
interface RouteForm {
  type: 'page' | 'group'
  code: string
  title: string
  path: string
  name: string
  component: string
  parent_code: string
  icon: string
  sort: number
  min_level: number
  showInMenu: boolean
  perm: string
  enabled: boolean
}

const emptyForm = (): RouteForm => ({
  type: 'page', code: '', title: '', path: '', name: '', component: '', parent_code: '',
  icon: '', sort: 100, min_level: 1, showInMenu: true, perm: '', enabled: true,
})

const showForm = ref(false)
const editing = ref<AdminRoute | null>(null) // null = 新增
const form = ref<RouteForm>(emptyForm())
const saving = ref(false)

const isEdit = computed(() => editing.value !== null)
/** 内置路由：只能改展示类字段 */
const lockStructure = computed(() => !!editing.value?.builtin)
/** 编辑时页面 / 分组类型不可互转 */
const lockType = computed(() => isEdit.value)

const openCreate = () => {
  editing.value = null
  form.value = emptyForm()
  showForm.value = true
}
const openEdit = (r: AdminRoute) => {
  editing.value = r
  form.value = {
    type: r.component ? 'page' : 'group', code: r.code, title: r.title, path: r.path, name: r.name,
    component: r.component, parent_code: r.parent_code, icon: r.icon, sort: r.sort,
    min_level: r.min_level, showInMenu: !r.hidden, perm: r.perm, enabled: r.enabled,
  }
  showForm.value = true
}

const levelOptions = [
  {label: '用户（1）', value: 1},
  {label: '管理员（2）', value: 2},
  {label: '超级管理员（3）', value: 3},
]

const componentOptions = computed(() => componentKeys().map((k) => ({label: k, value: k})))

/** 可选的父级：只能是菜单分组；编辑时不能选自己（服务端还会再防环） */
const parentOptions = computed(() => [
  {label: '（无，顶层菜单）', value: ''},
  ...rows.value
      .filter((r) => !r.component && r.code !== editing.value?.code)
      .map((r) => ({label: `${r.title}（${r.code}）`, value: r.code})),
])

const permOptions = computed(() => [
  {label: '不放行任何接口（游客用不了这个页面）', value: ''},
  ...perms.value.map((p) => ({label: `${p.label}（${p.key}）`, value: p.key})),
])

/** views/admin/FooBar.vue → /admin/fooBar；只在用户还没填 path 时作为默认建议 */
const suggestPath = (component: string) =>
    '/' + component.replace(/^views\//, '').replace(/\.vue$/, '')
        .split('/').map((s) => s.charAt(0).toLowerCase() + s.slice(1)).join('/')

/** /admin/fooBar → admin.foobar */
const suggestCode = (path: string) =>
    path.replace(/^\//, '').toLowerCase().replace(/[^a-z0-9]+/g, '.').replace(/^\.+|\.+$/g, '')

const onPickComponent = (c: string) => {
  form.value.component = c
  if (!isEdit.value) {
    if (!form.value.path) form.value.path = suggestPath(c)
    if (!form.value.code) form.value.code = suggestCode(form.value.path)
  }
}

const save = async () => {
  const f = form.value
  if (!f.title.trim()) {
    message.warning('标题不能为空')
    return
  }
  saving.value = true
  try {
    if (!editing.value) {
      if (!f.code.trim()) {
        message.warning('请填写编码')
        saving.value = false
        return
      }
      if (f.type === 'page' && (!f.component || !f.path.trim())) {
        message.warning('页面必须选择页面组件并填写路径')
        saving.value = false
        return
      }
      await adminCreateRoute({
        code: f.code.trim(), title: f.title.trim(), path: f.path.trim() || undefined,
        name: f.name.trim() || undefined, component: f.type === 'page' ? f.component : '',
        parent_code: f.parent_code, icon: f.icon, sort: f.sort, hidden: !f.showInMenu,
        min_level: f.min_level, perm: f.type === 'page' ? f.perm : '', enabled: f.enabled,
      })
      message.success('已新增（在线客户端几分钟内自动出现该菜单）')
    } else {
      const r = editing.value
      const body: Parameters<typeof adminUpdateRoute>[1] = {
        title: f.title.trim(), icon: f.icon, sort: f.sort, min_level: f.min_level,
      }
      // 结构性字段只发有变化的，且仅自定义路由
      if (!r.builtin) {
        if (f.path.trim() !== r.path) body.path = f.path.trim()
        if (f.parent_code !== r.parent_code) body.parent_code = f.parent_code
        if (r.component) {
          if (f.component !== r.component) body.component = f.component
          if (f.name.trim() !== r.name) body.name = f.name.trim()
          if (f.perm !== r.perm) body.perm = f.perm
        }
      }
      await adminUpdateRoute(r.id, body)
      message.success('已保存（在线客户端几分钟内自动刷新菜单）')
    }
    showForm.value = false
    fetchAll()
  } catch (e) {
    message.error(String(e))
  } finally {
    saving.value = false
  }
}

const remove = (r: AdminRoute) => {
  dialog.warning({
    title: '删除路由',
    content: `确认删除「${r.title}」（${r.code}）？它被授权给访客的记录也会一并清除，此操作不可撤销。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await adminDeleteRoute(r.id)
        message.success('已删除')
        fetchAll()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

const columns: DataTableColumns<AdminRoute> = [
  {
    title: '标题', key: 'title', width: 190,
    render: (r) => h('span', {style: {paddingLeft: r.parent_code ? '20px' : '0', fontWeight: r.component ? 'normal' : '600'}}, r.title),
  },
  {title: '编码', key: 'code', width: 150},
  {title: '路径', key: 'path', width: 160},
  {
    title: '类型', key: 'component', width: 150,
    render(r) {
      const tags = [
        h(NTag, {size: 'small', type: r.builtin ? 'default' : 'info'}, {default: () => (r.builtin ? '内置' : '自定义')}),
      ]
      if (!r.component) tags.push(h(NTag, {size: 'small'}, {default: () => '分组'}))
      // 服务端有、但当前客户端没有对应页面组件：老客户端 / 新页面未随客户端发布 / 组件名填错
      else if (!hasComponent(r.component)) tags.push(h(NTag, {size: 'small', type: 'warning'}, {default: () => '客户端缺失'}))
      return h(NSpace, {size: 4}, {default: () => tags})
    },
  },
  {title: '最低级别', key: 'min_level', width: 100, render: (r) => LEVEL_TEXT[r.min_level] ?? r.min_level},
  {title: '排序', key: 'sort', width: 70},
  {
    title: '菜单显示', key: 'hidden', width: 90,
    render: (r) => r.component ? h(NSwitch, {
      size: 'small', value: !r.hidden, 'onUpdate:value': (v: boolean) => patch(r, {hidden: !v}),
    }) : '-',
  },
  {
    title: '启用', key: 'enabled', width: 80,
    render: (r) => h(NSwitch, {
      size: 'small', value: r.enabled, 'onUpdate:value': (v: boolean) => patch(r, {enabled: v}),
    }),
  },
  {
    title: '操作', key: 'actions', width: 130,
    render: (r) => h(NSpace, {size: 6}, {
      default: () => [
        h(NButton, {size: 'small', onClick: () => openEdit(r)}, {default: () => '编辑'}),
        r.builtin ? null : h(NButton, {size: 'small', type: 'error', ghost: true, onClick: () => remove(r)}, {default: () => '删除'}),
      ],
    }),
  },
]
</script>

<template>
  <div class="manage-container">
    <n-space align="center" justify="space-between" class="toolbar">
      <div class="title">路由管理</div>
      <n-space>
        <n-button size="small" @click="fetchAll">刷新</n-button>
        <n-button size="small" type="primary" @click="openCreate">新增路由</n-button>
      </n-space>
    </n-space>

    <n-alert type="info" :show-icon="false" class="tip">
      客户端只内置登录等基础页面，其余菜单和路由都按这里的配置下发。内置路由只能停用或调整展示；
      「新增路由」可以把客户端已有的页面登记成新的菜单入口，或建一个菜单分组。
      停用分组会让整组页面消失；访客不受「最低级别」约束，只看管理员为他单独授权的页面。
      <n-text depth="3">
        做全新的页面：先在客户端 src/views 下新增 .vue 并发布客户端，它就会出现在「新增路由」的页面组件列表里。
      </n-text>
    </n-alert>

    <n-data-table :columns="columns" :data="rows" :loading="loading" :row-key="(r: AdminRoute) => r.id"
                  :bordered="false" size="small" :pagination="false">
      <template #empty>
        <n-empty description="没有路由"/>
      </template>
    </n-data-table>

    <n-modal v-model:show="showForm">
      <n-card style="width: 560px; max-height: 90vh; overflow: auto"
              :title="isEdit ? `编辑「${editing?.code}」` : '新增路由'" closable @close="showForm = false">
        <n-alert v-if="lockStructure" type="warning" :show-icon="false" style="margin-bottom: 12px">
          内置路由的路径、组件、父级由客户端依赖，不能修改；可以改标题、图标、排序和最低级别，或在列表里停用它。
        </n-alert>
        <n-form label-placement="left" label-width="96">
          <n-form-item label="类型">
            <n-radio-group v-model:value="form.type" :disabled="lockType">
              <n-radio-button value="page">页面</n-radio-button>
              <n-radio-button value="group">菜单分组</n-radio-button>
            </n-radio-group>
          </n-form-item>
          <n-form-item v-if="!isEdit" label="编码">
            <n-input v-model:value="form.code" placeholder="如 admin.report（小写字母 / 数字 / . _ -，唯一）"/>
          </n-form-item>
          <n-form-item label="标题">
            <n-input v-model:value="form.title" placeholder="菜单上显示的名字"/>
          </n-form-item>
          <n-form-item v-if="form.type === 'page'" label="页面组件">
            <n-select v-model:value="form.component" :options="componentOptions" filterable
                      :disabled="lockStructure" placeholder="选择客户端已有的页面" @update:value="onPickComponent"/>
          </n-form-item>
          <n-form-item :label="form.type === 'page' ? '路径' : '路径标识'">
            <n-input v-model:value="form.path" :disabled="lockStructure"
                     :placeholder="form.type === 'page' ? '如 /admin/report' : '分组可留空，默认 /编码'"/>
          </n-form-item>
          <n-form-item v-if="form.type === 'page'" label="路由名">
            <n-input v-model:value="form.name" :disabled="lockStructure" placeholder="留空自动生成"/>
          </n-form-item>
          <n-form-item label="父级分组">
            <n-select v-model:value="form.parent_code" :options="parentOptions" :disabled="lockStructure"/>
          </n-form-item>
          <n-form-item label="图标">
            <n-input v-model:value="form.icon" placeholder="图标名（可留空）"/>
          </n-form-item>
          <n-form-item label="排序">
            <n-input-number v-model:value="form.sort" style="width: 140px"/>
          </n-form-item>
          <n-form-item label="最低级别">
            <n-select v-model:value="form.min_level" :options="levelOptions" style="width: 200px"/>
          </n-form-item>
          <n-form-item v-if="form.type === 'page'" label="游客接口">
            <n-select v-model:value="form.perm" :options="permOptions" :disabled="lockStructure"/>
          </n-form-item>
          <template v-if="!isEdit">
            <n-form-item label="菜单显示">
              <n-switch v-model:value="form.showInMenu"/>
            </n-form-item>
            <n-form-item label="启用">
              <n-switch v-model:value="form.enabled"/>
            </n-form-item>
          </template>
        </n-form>
        <template #footer>
          <n-space justify="end">
            <n-button @click="showForm = false">取消</n-button>
            <n-button type="primary" :loading="saving" @click="save">{{ isEdit ? '保存' : '新增' }}</n-button>
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
</style>
