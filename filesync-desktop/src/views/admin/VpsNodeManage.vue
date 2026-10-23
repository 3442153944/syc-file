<script setup lang="ts">
// VPS 节点面板管理：每条节点自带一个随机入口路径 + 访问秘钥，点「打开」即现签地址进面板。
//
// 面板本身不再对公网裸奔：入口路径只做「校验秘钥 → 种 cookie → 302 到面板真实路径」，
// 节点上的 nginx 片段由本页「网关片段」生成，粘贴即用。
import {h, onMounted, ref} from 'vue'
import {
  NButton, NSpace, NTag, NInput, NInputNumber, NDataTable,
  NModal, NForm, NFormItem, NAlert, NPagination, NSelect, NSwitch,
  NPopconfirm, NText, useMessage, useDialog,
} from 'naive-ui'
import type {DataTableColumns} from 'naive-ui'
import {isTauri} from '@tauri-apps/api/core'
import {
  listVpsNodes, createVpsNode, updateVpsNode, deleteVpsNode,
  rotateVpsNode, checkVpsNode, vpsNodeSnippet, vpsAllSnippets,
} from '@/api/admin/vpsApi'
import type {VpsNode, VpsNodeInput, VpsStatus} from '@/api/admin/vpsApi'

const message = useMessage()
const dialog = useDialog()

const list = ref<VpsNode[]>([])
const loading = ref(false)
const total = ref(0)
const pageNum = ref(1)
const pageSize = ref(20)
const checking = ref<Record<number, boolean>>({})

const showForm = ref(false)
const saving = ref(false)
const editing = ref<VpsNode | null>(null)
const form = ref<VpsNodeInput>(emptyForm())

const showResult = ref(false)
const result = ref<{url: string; key: string; snippet: string} | null>(null)

const showSnippet = ref(false)
const snippetText = ref('')
const snippetLoading = ref(false)

const checkingAll = ref(false)
const showAllSnippets = ref(false)
const allSnippetsText = ref('')
const allSnippetsLoading = ref(false)

function emptyForm(): VpsNodeInput {
  return {
    code: '', title: '', region: '', provider: '',
    host: '', gateway_host: '',
    panel_scheme: 'https', panel_port: 443, panel_path: '/', gateway_port: 443,
    sub_url: '', sub_port: 2096, remark: '', sort: 0, enabled: true,
  }
}

const STATUS_TEXT: Record<VpsStatus, string> = {ok: '正常', fail: '不通', unknown: '未检测'}
const STATUS_TYPE: Record<VpsStatus, 'success' | 'error' | 'default'> = {
  ok: 'success', fail: 'error', unknown: 'default',
}

const fmtTime = (s?: string | null) =>
  s ? new Date(s).toLocaleString('zh-CN', {hour12: false}) : '—'

function fmtBytes(n?: number | null): string {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

async function openExternal(url: string) {
  if (!url) return
  if (isTauri()) {
    const {openUrl} = await import('@tauri-apps/plugin-opener')
    await openUrl(url)
  } else {
    window.open(url, '_blank', 'noopener,noreferrer')
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制')
  } catch {
    message.warning('复制失败，请手动选中复制')
  }
}

async function fetchList() {
  loading.value = true
  try {
    const data = await listVpsNodes({page: pageNum.value, page_size: pageSize.value})
    list.value = data.list || []
    total.value = data.total || 0
  } catch (e) {
    message.error(String(e))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.value = emptyForm()
  form.value.sort = (list.value.length ? Math.max(...list.value.map((n) => n.sort)) : 0) + 1
  showForm.value = true
}

function openEdit(row: VpsNode) {
  editing.value = row
  form.value = {
    code: row.code, title: row.title, region: row.region, provider: row.provider,
    host: row.host, gateway_host: row.gateway_host,
    panel_scheme: row.panel_scheme, panel_port: row.panel_port,
    panel_path: row.panel_path, gateway_port: row.gateway_port,
    sub_url: row.sub_url, sub_port: row.sub_port, remark: row.remark, sort: row.sort, enabled: row.enabled,
  }
  showForm.value = true
}

async function submitForm() {
  if (saving.value) return
  if (!form.value.title?.trim()) {
    message.warning('请填写节点名称')
    return
  }
  if (!form.value.host?.trim()) {
    message.warning('请填写节点域名或 IP')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateVpsNode(editing.value.id, form.value)
      message.success('已保存')
      showForm.value = false
    } else {
      const created = await createVpsNode(form.value)
      const snip = await vpsNodeSnippet(created.node.id).catch(() => null)
      result.value = {
        url: created.node.entry_url,
        key: created.access_key,
        snippet: snip?.snippet || '',
      }
      showForm.value = false
      showResult.value = true
    }
    await fetchList()
  } catch (e) {
    message.error(String(e))
  } finally {
    saving.value = false
  }
}

function confirmDelete(row: VpsNode) {
  dialog.warning({
    title: '删除节点',
    content: `确定删除「${row.title}」吗？该节点的入口地址与秘钥会一并作废。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteVpsNode(row.id)
        message.success('已删除')
        await fetchList()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

function confirmRotate(row: VpsNode) {
  dialog.warning({
    title: '轮换入口与秘钥',
    content: `将重新生成「${row.title}」的入口路径和访问秘钥，旧入口立即失效。轮换后要把新的 nginx 片段同步到该节点，否则面板打不开。`,
    positiveText: '轮换',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        const res = await rotateVpsNode(row.id)
        result.value = {url: res.node.entry_url, key: res.access_key, snippet: res.snippet || ''}
        showResult.value = true
        await fetchList()
      } catch (e) {
        message.error(String(e))
      }
    },
  })
}

async function doCheck(row: VpsNode) {
  checking.value[row.id] = true
  try {
    const node = await checkVpsNode(row.id)
    const idx = list.value.findIndex((n) => n.id === row.id)
    if (idx >= 0) list.value[idx] = node
    if (node.last_status === 'ok') message.success(`${row.title} 入口正常（${node.last_latency_ms}ms）`)
    else message.error(`${row.title} 不通：${node.last_error || '未知错误'}`)
  } catch (e) {
    message.error(String(e))
  } finally {
    checking.value[row.id] = false
  }
}

async function openSnippet(row: VpsNode) {
  snippetLoading.value = true
  showSnippet.value = true
  snippetText.value = ''
  try {
    const res = await vpsNodeSnippet(row.id)
    snippetText.value = res.snippet
  } catch (e) {
    message.error(String(e))
    showSnippet.value = false
  } finally {
    snippetLoading.value = false
  }
}

/** 逐台探活：串行执行，避免同时打一圈把节点打花。 */
async function checkAll() {
  if (checkingAll.value) return
  const targets = list.value.filter((n) => n.enabled)
  if (!targets.length) {
    message.info('没有启用的节点')
    return
  }
  checkingAll.value = true
  let ok = 0
  let fail = 0
  try {
    for (const row of targets) {
      try {
        const node = await checkVpsNode(row.id)
        const idx = list.value.findIndex((n) => n.id === row.id)
        if (idx >= 0) list.value[idx] = node
        if (node.last_status === 'ok') ok++
        else fail++
      } catch {
        fail++
      }
    }
    if (fail === 0) message.success(`${ok} 个节点全部正常`)
    else message.warning(`正常 ${ok} 个，异常 ${fail} 个`)
  } finally {
    checkingAll.value = false
  }
}

async function openAllSnippets() {
  allSnippetsLoading.value = true
  showAllSnippets.value = true
  allSnippetsText.value = ''
  try {
    const res = await vpsAllSnippets()
    allSnippetsText.value = res.text
  } catch (e) {
    message.error(String(e))
    showAllSnippets.value = false
  } finally {
    allSnippetsLoading.value = false
  }
}

const schemeOptions = [
  {label: 'https', value: 'https'},
  {label: 'http', value: 'http'},
]

const columns: DataTableColumns<VpsNode> = [
  {
    title: '节点', key: 'title', minWidth: 170,
    render: (r) => h('div', {style: 'line-height:1.5'}, [
      h('div', {style: 'font-weight:600'}, r.title),
      h('div', {style: 'font-size:12px;color:#888'}, [
        h(NTag, {size: 'tiny', bordered: false}, () => r.code),
        h('span', {style: 'margin-left:6px'},
          [r.provider, r.region].filter(Boolean).join(' · ') || '—'),
      ]),
    ]),
  },
  {
    title: '面板', key: 'panel', minWidth: 200,
    render: (r) => h('div', {style: 'line-height:1.5;font-size:12px'}, [
      h('div', {style: 'font-family:ui-monospace,monospace'},
        `${r.panel_scheme}://${r.host}:${r.panel_port}`),
      h('div', {style: 'color:#888;font-family:ui-monospace,monospace'}, r.panel_path),
    ]),
  },
  {
    title: '入口', key: 'entry', width: 210,
    render: (r) => h(NSpace, {size: 4, align: 'center'}, () => [
      h(NText, {depth: 3, style: 'font-size:12px;font-family:ui-monospace,monospace'}, () => r.access_path),
      h(NButton, {
        size: 'tiny', type: 'primary', secondary: true, disabled: !r.enabled,
        onClick: () => openExternal(r.entry_url),
      }, () => '打开'),
    ]),
  },
  {
    title: '状态', key: 'status', width: 150,
    render: (r) => h('div', {style: 'line-height:1.5;font-size:12px'}, [
      h(NSpace, {size: 4, align: 'center'}, () => [
        h(NTag, {size: 'small', type: STATUS_TYPE[r.last_status] || 'default'},
          () => STATUS_TEXT[r.last_status] || r.last_status),
        r.last_status === 'ok'
          ? h('span', {style: 'color:#888'}, `${r.last_latency_ms}ms`)
          : null,
      ]),
      h('div', {style: 'color:#aaa'}, fmtTime(r.last_checked_at)),
      r.last_status === 'fail' && r.last_error
        ? h('div', {style: 'color:#d03050', wordBreak: 'break-all'}, r.last_error)
        : null,
    ]),
  },
  {
    title: '流量', key: 'traffic', width: 170,
    render: (r) => {
      const used = (r.traffic_up || 0) + (r.traffic_down || 0)
      if (!used) return h('span', {style: 'color:#bbb'}, '—')
      return h('div', {style: 'line-height:1.5;font-size:12px'}, [
        h('div', `↑ ${fmtBytes(r.traffic_up)}  ↓ ${fmtBytes(r.traffic_down)}`),
        h('div', {style: 'color:#888'},
          r.traffic_total > 0 ? `共 ${fmtBytes(r.traffic_total)}` : '不限量'),
        r.traffic_expire
          ? h('div', {style: 'color:#888'}, `到期 ${fmtTime(r.traffic_expire)}`)
          : null,
      ])
    },
  },
  {
    title: '操作', key: 'actions', width: 250,
    render: (r) => h(NSpace, {size: 4}, () => [
      h(NButton, {
        size: 'tiny', quaternary: true, loading: !!checking.value[r.id],
        onClick: () => doCheck(r),
      }, () => '探活'),
      h(NButton, {size: 'tiny', quaternary: true, onClick: () => openEdit(r)}, () => '编辑'),
      h(NButton, {size: 'tiny', quaternary: true, onClick: () => openSnippet(r)}, () => '片段'),
      h(NButton, {size: 'tiny', quaternary: true, type: 'warning', onClick: () => confirmRotate(r)}, () => '轮换'),
      h(NPopconfirm, {onPositiveClick: () => confirmDelete(r)}, {
        trigger: () => h(NButton, {size: 'tiny', quaternary: true, type: 'error'}, () => '删除'),
      }),
    ]),
  },
]

onMounted(fetchList)
</script>

<template>
  <div class="vps-page">
    <div class="page-head">
      <div>
        <h2>节点面板</h2>
        <p class="hint">
          每个节点有一条随机入口路径和一把访问秘钥：点「打开」带上秘钥进面板，节点上的 nginx 校验后跳转到面板真实路径。
          面板本身保持只监听回环，不再对公网裸奔。
        </p>
      </div>
      <n-space>
        <n-button type="primary" @click="openCreate">新增节点</n-button>
        <n-button :loading="checkingAll" @click="checkAll">全部探活</n-button>
        <n-button @click="openAllSnippets">导出全部片段</n-button>
        <n-button :loading="loading" @click="fetchList">刷新</n-button>
      </n-space>
    </div>

    <n-data-table
      :columns="columns"
      :data="list"
      :loading="loading"
      :row-key="(r: VpsNode) => r.id"
      size="small"
      :bordered="false"
    >
      <template #empty>
        <div class="empty-slot">
          <div class="empty-title">还没有登记节点</div>
          <div class="empty-hint">点右上角「新增节点」，填入面板地址后会自动生成入口路径和访问秘钥。</div>
        </div>
      </template>
    </n-data-table>

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

    <!-- 新建 / 编辑 -->
    <n-modal v-model:show="showForm" preset="card" :title="editing ? '编辑节点' : '新增节点'" style="width: 640px">
      <n-form label-placement="left" label-width="104">
        <div class="grid2">
          <n-form-item label="节点标识">
            <n-input v-model:value="form.code" :disabled="!!editing" placeholder="jp / ny / hk" />
          </n-form-item>
          <n-form-item label="节点名称">
            <n-input v-model:value="form.title" placeholder="东京-1" />
          </n-form-item>
          <n-form-item label="服务商">
            <n-input v-model:value="form.provider" placeholder="aliyun / aws / vultr" />
          </n-form-item>
          <n-form-item label="地区">
            <n-input v-model:value="form.region" placeholder="东京 / 纽约" />
          </n-form-item>
          <n-form-item label="域名或 IP">
            <n-input v-model:value="form.host" placeholder="jp.sunyuanling.cn" />
          </n-form-item>
          <n-form-item label="入口域名">
            <n-input v-model:value="form.gateway_host" placeholder="留空则同上" />
          </n-form-item>
        </div>

        <n-form-item label="面板地址">
          <n-space :size="8" align="center">
            <n-select v-model:value="form.panel_scheme" :options="schemeOptions" style="width: 100px" />
            <n-input v-model:value="form.host" disabled style="width: 180px" />
            <n-input-number v-model:value="form.panel_port" :min="1" :max="65535" style="width: 110px" />
            <n-input v-model:value="form.panel_path" placeholder="/1fa597d456cc34fc/" style="width: 220px" />
          </n-space>
        </n-form-item>
        <n-form-item label="入口端口">
          <n-input-number v-model:value="form.gateway_port" :min="1" :max="65535" style="width: 120px" />
          <span class="inline-hint">面板路径为 / 时用它做独立 server（如 ny 用 9443）</span>
        </n-form-item>

        <n-form-item label="订阅">
          <n-space :size="8" align="center" style="width: 100%">
            <n-input
              v-model:value="form.sub_url"
              placeholder="/sub/xxxx/ 走网关（推荐）；也可填完整 http(s) 地址"
              style="flex: 1"
            />
            <n-input-number v-model:value="form.sub_port" :min="1" :max="65535" style="width: 110px">
              <template #prefix>端口</template>
            </n-input-number>
          </n-space>
        </n-form-item>
        <p class="field-hint">
          填相对路径（以 / 开头）时，网关片段会额外生成一段订阅反代，后端凭同一个 cookie 去读流量，
          订阅端口无需对公网开放。
        </p>
        <n-form-item label="备注">
          <n-input v-model:value="form.remark" type="textarea" :rows="2" maxlength="200" />
        </n-form-item>
        <n-form-item label="排序 / 启用">
          <n-space :size="10" align="center">
            <n-input-number v-model:value="form.sort" :min="0" style="width: 100px" />
            <n-switch v-model:value="form.enabled" />
          </n-space>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showForm = false">取消</n-button>
          <n-button type="primary" :loading="saving" @click="submitForm">
            {{ editing ? '保存' : '创建并生成秘钥' }}
          </n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 新建 / 轮换后的结果 -->
    <n-modal v-model:show="showResult" preset="card" title="入口已生成" style="width: 720px">
      <n-alert type="warning" :show-icon="true" style="margin-bottom: 12px">
        访问秘钥只在这里完整展示；入口地址等同于面板钥匙，别贴到公开的地方。
      </n-alert>
      <div v-if="result">
        <div class="label">入口地址</div>
        <div class="link-row">
          <n-input :value="result.url" readonly size="small" />
          <n-button size="small" @click="copy(result.url)">复制</n-button>
          <n-button size="small" type="primary" secondary @click="openExternal(result.url)">打开</n-button>
        </div>
        <div class="label" style="margin-top: 12px">访问秘钥</div>
        <div class="link-row">
          <n-input :value="result.key" readonly size="small" />
          <n-button size="small" @click="copy(result.key)">复制</n-button>
        </div>
        <template v-if="result.snippet">
          <div class="label" style="margin-top: 12px">nginx 片段（粘到该节点的 server 块里）</div>
          <div class="link-row">
            <n-input :value="result.snippet" type="textarea" :rows="8" readonly size="small" />
            <n-button size="small" @click="copy(result.snippet)">复制</n-button>
          </div>
        </template>
      </div>
    </n-modal>

    <!-- 片段查看 -->
    <n-modal v-model:show="showSnippet" preset="card" title="网关片段" style="width: 760px">
      <n-spin :show="snippetLoading">
        <n-input :value="snippetText" type="textarea" :rows="18" readonly size="small" />
        <template #description>
          <span class="inline-hint">把 CERT_FULLCHAIN / CERT_PRIVKEY 换成该机实际证书路径后再 reload。</span>
        </template>
      </n-spin>
      <template #footer>
        <n-space justify="end">
          <n-button size="small" @click="copy(snippetText)">复制</n-button>
          <n-button size="small" @click="showSnippet = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 全部片段 -->
    <n-modal v-model:show="showAllSnippets" preset="card" title="全部节点网关片段" style="width: 900px">
      <n-spin :show="allSnippetsLoading">
        <n-input :value="allSnippetsText" type="textarea" :rows="26" readonly size="small" />
      </n-spin>
      <template #footer>
        <n-space justify="end">
          <n-button size="small" @click="copy(allSnippetsText)">复制全部</n-button>
          <n-button size="small" @click="showAllSnippets = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<style scoped>
.vps-page { padding: 4px 2px; }
.page-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 12px; }
.page-head h2 { margin: 0 0 4px; font-size: 18px; }
.hint { color: #888; font-size: 12px; margin: 0; line-height: 1.6; max-width: 720px; }
.pager { display: flex; justify-content: flex-end; margin-top: 12px; }
.label { font-size: 12px; color: #888; margin-bottom: 6px; }
.link-row { display: flex; gap: 8px; margin-bottom: 8px; align-items: flex-start; }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 12px; }
.inline-hint { color: #999; font-size: 12px; margin-left: 8px; }
.field-hint { color: #999; font-size: 12px; margin: -8px 0 12px 104px; line-height: 1.6; }
.empty-slot { padding: 48px 0; text-align: center; }
.empty-title { font-size: 14px; color: #666; }
.empty-hint { font-size: 12px; color: #999; margin-top: 6px; }
</style>
