<script setup lang="ts">
// 路由管理（仅超级管理员）：决定客户端有哪些页面入口、叫什么、排在哪、哪个级别以上可见。
// 页面集合本身由服务端内置目录决定（页面代码随客户端发布），这里只能改展示与可见性，不能新增 / 删除页面。
import {h, onMounted, ref} from 'vue'
import {
  NButton, NSpace, NTag, NSwitch, NDataTable, NEmpty, NModal, NCard, NForm, NFormItem, NInput,
  NInputNumber, NSelect, NAlert, NText, useMessage,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {adminListRoutes, adminUpdateRoute} from '@/api/system/systemApi'
import type {AdminRoute} from '@/api/system/systemTypes'
import {LEVEL_TEXT} from '@/utils/level'
import {hasComponent} from '@/router/dynamic'

const message = useMessage()
const rows = ref<AdminRoute[]>([])
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
onMounted(fetchAll)

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

// 编辑弹窗
const editing = ref<AdminRoute | null>(null)
const form = ref({title: '', icon: '', sort: 0, min_level: 1})
const levelOptions = [
  {label: '用户（1）', value: 1},
  {label: '管理员（2）', value: 2},
  {label: '超级管理员（3）', value: 3},
]
const openEdit = (r: AdminRoute) => {
  editing.value = r
  form.value = {title: r.title, icon: r.icon, sort: r.sort, min_level: r.min_level}
}
const saveEdit = async () => {
  if (!editing.value) return
  if (!form.value.title.trim()) {
    message.warning('标题不能为空')
    return
  }
  const r = editing.value
  editing.value = null
  await patch(r, {title: form.value.title.trim(), icon: form.value.icon, sort: form.value.sort, min_level: form.value.min_level})
}

const columns: DataTableColumns<AdminRoute> = [
  {
    title: '标题', key: 'title', width: 190,
    render: (r) => h('span', {style: {paddingLeft: r.parent_code ? '20px' : '0', fontWeight: r.component ? 'normal' : '600'}}, r.title),
  },
  {title: '编码', key: 'code', width: 150},
  {title: '路径', key: 'path', width: 170},
  {
    title: '类型', key: 'component', width: 90,
    render(r) {
      if (!r.component) return h(NTag, {size: 'small'}, {default: () => '分组'})
      // 服务端有、但当前客户端没有对应页面组件：老客户端 / 新页面未随客户端发布
      return hasComponent(r.component)
          ? h(NTag, {size: 'small', type: 'success'}, {default: () => '页面'})
          : h(NTag, {size: 'small', type: 'warning'}, {default: () => '客户端缺失'})
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
    title: '操作', key: 'actions', width: 80,
    render: (r) => h(NButton, {size: 'small', onClick: () => openEdit(r)}, {default: () => '编辑'}),
  },
]
</script>

<template>
  <div class="manage-container">
    <n-space align="center" justify="space-between" class="toolbar">
      <div class="title">路由管理</div>
      <n-button size="small" @click="fetchAll">刷新</n-button>
    </n-space>

    <n-alert type="info" :show-icon="false" class="tip">
      客户端只内置登录等基础页面，其余菜单和路由都按这里的配置下发。
      停用分组会让整组页面消失；访客不受「最低级别」约束，只看管理员为他单独授权的页面。
      <n-text depth="3">页面本身不能在线新增，需随客户端发布。</n-text>
    </n-alert>

    <n-data-table :columns="columns" :data="rows" :loading="loading" :row-key="(r: AdminRoute) => r.id"
                  :bordered="false" size="small" :pagination="false">
      <template #empty>
        <n-empty description="没有路由"/>
      </template>
    </n-data-table>

    <n-modal :show="!!editing" @update:show="(v: boolean) => { if (!v) editing = null }">
      <n-card style="width: 420px" :title="`编辑「${editing?.code}」`" closable @close="editing = null">
        <n-form label-placement="left" label-width="80">
          <n-form-item label="标题">
            <n-input v-model:value="form.title"/>
          </n-form-item>
          <n-form-item label="图标">
            <n-input v-model:value="form.icon" placeholder="图标名（可留空）"/>
          </n-form-item>
          <n-form-item label="排序">
            <n-input-number v-model:value="form.sort" style="width: 140px"/>
          </n-form-item>
          <n-form-item label="最低级别">
            <n-select v-model:value="form.min_level" :options="levelOptions" style="width: 180px"/>
          </n-form-item>
        </n-form>
        <template #footer>
          <n-space justify="end">
            <n-button @click="editing = null">取消</n-button>
            <n-button type="primary" @click="saveEdit">保存</n-button>
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
