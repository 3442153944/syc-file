<script setup lang="ts">
// DSH 访问令牌：管理员一键生成一次性短链 / 长期 token，供 Web、PC、移动端登录 DSH。
//
// 令牌明文只在创建时返回一次；兑换入口由 nginx 把 /dsh/auth 反代到后端。
import {h, onMounted, ref, watch} from 'vue'
import {
  NButton, NSpace, NTag, NInput, NInputNumber, NDataTable, NEmpty,
  NModal, NForm, NFormItem, NAlert, NPagination, NRadioGroup, NRadio,
  useMessage, useDialog,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {listDshTokens, createDshToken, revokeDshToken} from '@/api/admin/dshApi'
import type {DshToken, DshTokenKind, CreateDshTokenResult} from '@/api/admin/dshApi'

const message = useMessage()
const dialog = useDialog()

const list = ref<DshToken[]>([])
const loading = ref(false)
const total = ref(0)
const pageNum = ref(1)
const pageSize = ref(20)

const showCreate = ref(false)
const creating = ref(false)
const form = ref<{kind: DshTokenKind; label: string; hours: number}>({
  kind: 'one_time', label: '', hours: 24,
})

const result = ref<CreateDshTokenResult | null>(null)
const showResult = ref(false)

watch(() => form.value.kind, (k) => {
  form.value.hours = k === 'one_time' ? 24 : 24 * 30
})

const KIND_TEXT: Record<string, string> = {
  one_time: '一次性短链',
  long_lived: '长期 token',
}
const STATUS_TEXT: Record<string, string> = {
  active: '有效',
  used: '已使用',
  expired: '已过期',
  revoked: '已吊销',
}
const STATUS_TYPE: Record<string, 'success' | 'default' | 'warning' | 'error'> = {
  active: 'success',
  used: 'default',
  expired: 'warning',
  revoked: 'error',
}

const fmt = (s?: string | null) => s ? new Date(s).toLocaleString('zh-CN', {hour12: false}) : '—'

async function fetchList() {
  loading.value = true
  try {
    const data = await listDshTokens({page: pageNum.value, page_size: pageSize.value})
    list.value = data.list || []
    total.value = data.total || 0
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

async function submitCreate() {
  if (creating.value) return
  creating.value = true
  try {
    const data = await createDshToken({
      kind: form.value.kind,
      label: form.value.label.trim(),
      expires_hours: form.value.hours,
    })
    result.value = data
    showCreate.value = false
    showResult.value = true
    form.value = {kind: 'one_time', label: '', hours: 24}
    await fetchList()
  } catch (e) {
    message.error(String(e))
  } finally {
    creating.value = false
  }
}

function confirmRevoke(row: DshToken) {
  dialog.warning({
    title: '吊销令牌',
    content: `确定吊销「${row.label || KIND_TEXT[row.kind]}」吗？已签发的会话不会立即失效，但之后不能再兑换。`,
    positiveText: '吊销',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await revokeDshToken(row.id)
        message.success('已吊销')
        await fetchList()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.warning('复制失败，请手动选中复制')
  }
}

const columns: DataTableColumns<DshToken> = [
  {
    title: '备注', key: 'label', minWidth: 140,
    render: (r) => r.label || h('span', {style: 'color:#999'}, '未填写'),
  },
  {
    title: '类型', key: 'kind', width: 110,
    render: (r) => h(NTag, {size: 'small', type: r.kind === 'one_time' ? 'info' : 'default'}, () => KIND_TEXT[r.kind]),
  },
  {
    title: '状态', key: 'status', width: 90,
    render: (r) => h(NTag, {size: 'small', type: STATUS_TYPE[r.status] || 'default'}, () => STATUS_TEXT[r.status] || r.status),
  },
  {title: '创建时间', key: 'created_at', width: 165, render: (r) => fmt(r.created_at)},
  {title: '到期时间', key: 'expires_at', width: 165, render: (r) => fmt(r.expires_at)},
  {title: '最近使用', key: 'last_used_at', width: 165, render: (r) => fmt(r.last_used_at)},
  {
    title: '操作', key: 'actions', width: 90,
    render: (r) => r.status === 'active' || r.status === 'used'
      ? h(NButton, {size: 'tiny', quaternary: true, type: 'error', onClick: () => confirmRevoke(r)}, () => '吊销')
      : h('span', {style: 'color:#bbb'}, '—'),
  },
]

onMounted(fetchList)
</script>

<template>
  <div class="dsh-token-page">
    <div class="page-head">
      <div>
        <h2>DSH 访问令牌</h2>
        <p class="hint">
          生成后把链接发给使用人，打开即登录 DSH；一次性短链用过即失效，长期 token 可重复使用，直到过期或吊销。
        </p>
      </div>
      <n-space>
        <n-button type="primary" @click="showCreate = true">生成令牌</n-button>
        <n-button :loading="loading" @click="fetchList">刷新</n-button>
      </n-space>
    </div>

    <n-data-table
      :columns="columns"
      :data="list"
      :loading="loading"
      :row-key="(r: DshToken) => r.id"
      size="small"
    />
    <n-empty v-if="!loading && !list.length" description="还没有令牌" style="padding: 40px 0" />

    <div class="pager">
      <n-pagination
        v-model:page="pageNum"
        v-model:page-size="pageSize"
        :item-count="total"
        :page-sizes="[20, 50]"
        show-size-picker
        @update:page="fetchList"
        @update:page-size="() => { pageNum = 1; fetchList() }"
      />
    </div>

    <n-modal v-model:show="showCreate" preset="card" title="生成 DSH 令牌" style="width: 520px">
      <n-form label-placement="left" label-width="90">
        <n-form-item label="类型">
          <n-radio-group v-model:value="form.kind">
            <n-space vertical>
              <n-radio value="one_time">一次性短链（打开一次即失效）</n-radio>
              <n-radio value="long_lived">长期 token（可反复打开）</n-radio>
            </n-space>
          </n-radio-group>
        </n-form-item>
        <n-form-item label="备注">
          <n-input v-model:value="form.label" placeholder="给谁用，例如：张三的手机" maxlength="60" />
        </n-form-item>
        <n-form-item label="有效期">
          <n-input-number v-model:value="form.hours" :min="1" :max="8760" style="width: 160px">
            <template #suffix>小时</template>
          </n-input-number>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="submitCreate">生成</n-button>
        </n-space>
      </template>
    </n-modal>

    <n-modal v-model:show="showResult" preset="card" title="令牌已生成" style="width: 620px">
      <n-alert type="warning" :show-icon="true" style="margin-bottom: 12px">
        令牌明文只显示这一次，关闭后无法再次查看；如丢失请吊销后重新生成。
      </n-alert>
      <div v-if="result">
        <div class="label">访问链接</div>
        <div v-for="l in result.links" :key="l.base" class="link-row">
          <n-input :value="l.url" readonly size="small" />
          <n-button size="small" @click="copy(l.url)">复制</n-button>
        </div>
        <div class="label" style="margin-top: 14px">原始 token</div>
        <div class="link-row">
          <n-input :value="result.token" readonly size="small" />
          <n-button size="small" @click="copy(result.token)">复制</n-button>
        </div>
        <p class="hint" style="margin-top: 10px">
          打开链接即完成认证并直接进入 DSH，手机、Web、桌面浏览器都一样；链接等同于登录凭证，请只发给信任的人。
          首次登录时由 DSH 引导完成账号与模型配置。
        </p>
      </div>
    </n-modal>
  </div>
</template>

<style scoped>
.dsh-token-page { padding: 4px 2px; }
.page-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 12px; }
.page-head h2 { margin: 0 0 4px; font-size: 18px; }
.hint { color: #888; font-size: 12px; margin: 0; line-height: 1.6; }
.pager { display: flex; justify-content: flex-end; margin-top: 12px; }
.label { font-size: 12px; color: #888; margin-bottom: 6px; }
.link-row { display: flex; gap: 8px; margin-bottom: 8px; }
</style>
