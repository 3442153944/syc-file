<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NAlert, NButton, NCard, NDatePicker, NEmpty, NFormItem, NInput, NInputNumber,
  NModal, NSelect, NSpace, NSwitch, NTable, NTag, useMessage,
} from 'naive-ui'
import {
  createStoragePool, listStoragePartitions, listStoragePools, listUploadSpaces,
  saveUploadSpace, setPoolEnabled, myUploadSpace,
  type Partition, type SpaceUser, type StoragePool, type UploadSpace,
} from '@/api/file/storageApi'
import { formatBytes } from '@/utils/speedMeter'
import { levelText } from '@/utils/level'
import UploadSpaceStatus from '@/components/UploadSpaceStatus.vue'

const props = defineProps<{ canConfigurePools: boolean }>()

const message = useMessage()
const pools = ref<StoragePool[]>([])
const partitions = ref<Partition[]>([])
const users = ref<SpaceUser[]>([])
const busy = ref(false)
const poolName = ref('')
const rootPath = ref('')
const selectedPartition = ref<string | null>(null)
const editing = ref<SpaceUser | null>(null)
const editPool = ref<number | null>(null)
const quotaGiB = ref<number | null>(10)
const expiry = ref<number | null>(null)
const enabled = ref(true)
const parentSpace = ref<UploadSpace | null>(null)
const maxQuotaGiB = computed(() => props.canConfigurePools ? 8388607 : Math.max(0, ((parentSpace.value?.quota_bytes ?? 0) - (parentSpace.value?.used_bytes ?? 0) + (editing.value?.space?.quota_bytes ?? 0)) / 1024 ** 3))
const poolOptions = computed(() => pools.value.map(p => ({
  label: `${p.name} · ${p.root_path}${p.enabled ? '' : '（已停用）'}`, value: p.id,
})))
const partitionOptions = computed(() => partitions.value.map(p => ({
  label: `${p.path} · 剩余 ${formatBytes(p.free)} / ${formatBytes(p.total)}`, value: p.path,
})))

async function load() {
  busy.value = true
  try {
    const [p, u, d, mine] = await Promise.all([listStoragePools(), listUploadSpaces(), props.canConfigurePools ? listStoragePartitions() : Promise.resolve([]), myUploadSpace()])
    pools.value = p ?? []; users.value = u ?? []; partitions.value = d ?? []
    parentSpace.value = mine
  } catch (e) { message.error(String(e)) } finally { busy.value = false }
}

function choosePartition(path: string) {
  selectedPartition.value = path
  rootPath.value = `${path.replace(/[\\/]$/, '')}${path.includes('\\') || /^[A-Za-z]:/.test(path) ? '\\' : '/'}filesync-uploads`
}

async function addPool() {
  if (!poolName.value.trim() || !rootPath.value.trim()) { message.warning('请填写名称和服务器目录'); return }
  busy.value = true
  try {
    await createStoragePool(poolName.value.trim(), rootPath.value.trim())
    poolName.value = ''; rootPath.value = ''; selectedPartition.value = null
    message.success('存储位置已创建'); await load()
  } catch (e) { message.error(String(e)) } finally { busy.value = false }
}

async function togglePool(pool: StoragePool) {
  busy.value = true
  try { await setPoolEnabled(pool.id, !pool.enabled); await load() }
  catch (e) { message.error(String(e)) } finally { busy.value = false }
}

function edit(row: SpaceUser) {
  editing.value = row; editPool.value = row.space?.pool_id ?? pools.value.find(p => p.enabled)?.id ?? null
  quotaGiB.value = (row.space?.quota_bytes ?? 10 * 1024 ** 3) / 1024 ** 3
  const defaultExpiry = row.space?.expires_at ?? (!props.canConfigurePools ? parentSpace.value?.expires_at : null)
  expiry.value = defaultExpiry ? new Date(defaultExpiry).getTime() : null
  enabled.value = row.space?.enabled ?? true
}

async function save() {
  if (!editing.value || !editPool.value || !quotaGiB.value) { message.warning('请选择存储位置并填写容量'); return }
  busy.value = true
  try {
    await saveUploadSpace(editing.value.user_id, {
      pool_id: editPool.value, quota_bytes: Math.round(quotaGiB.value * 1024 ** 3),
      expires_at: expiry.value === null ? null : new Date(expiry.value).toISOString(), enabled: enabled.value,
    })
    editing.value = null; message.success('空间配置已保存'); await load()
  } catch (e) { message.error(String(e)) } finally { busy.value = false }
}
onMounted(load)
</script>

<template>
  <n-space vertical :size="16">
    <n-alert type="info">只能向更低权限账号分配空间。下级额度从父空间划减；到期或停用后冻结访问并保留文件，不自动返还额度。</n-alert>
    <upload-space-status v-if="!canConfigurePools && parentSpace" :space="parentSpace" :loading="busy" :show-browse="false" @refresh="load" />
    <n-card v-if="canConfigurePools" title="存储位置" size="small">
      <n-space vertical>
        <n-select :value="selectedPartition" :options="partitionOptions" placeholder="选择服务器磁盘或分区" @update:value="choosePartition" />
        <n-input v-model:value="poolName" placeholder="存储位置名称，例如：数据盘" />
        <n-input v-model:value="rootPath" placeholder="服务器上的独立空目录，例如 /mnt/data/filesync-uploads" />
        <n-space><n-button type="primary" :loading="busy" @click="addPool">添加存储位置</n-button><n-button :disabled="busy" @click="load">刷新</n-button></n-space>
        <n-table v-if="pools.length" :single-line="false">
          <thead><tr><th>名称</th><th>服务器目录</th><th>状态</th><th>操作</th></tr></thead>
          <tbody><tr v-for="pool in pools" :key="pool.id"><td>{{ pool.name }}</td><td class="path">{{ pool.root_path }}</td><td>{{ pool.enabled ? '可用' : '已停用' }}</td><td><n-button size="small" :disabled="busy" @click="togglePool(pool)">{{ pool.enabled ? '停用' : '启用' }}</n-button></td></tr></tbody>
        </n-table>
        <n-empty v-else description="尚未创建存储位置" />
      </n-space>
    </n-card>
    <n-card title="用户空间与有效期" size="small">
      <div class="table-scroll"><n-table :single-line="false">
        <thead><tr><th>账号</th><th>级别</th><th>用量 / 容量</th><th>有效期</th><th>状态</th><th>操作</th></tr></thead>
        <tbody><tr v-for="row in users" :key="row.user_id">
          <td>{{ row.username }}</td><td>{{ levelText(row.level) }}</td>
          <td>{{ row.space ? `${formatBytes(row.space.used_bytes)} / ${formatBytes(row.space.quota_bytes)}` : '—' }}</td>
          <td>{{ row.space?.expires_at ? new Date(row.space.expires_at).toLocaleString() : '不限期' }}</td>
          <td><n-tag :type="row.error || row.space?.frozen ? 'warning' : row.space ? 'success' : 'default'">{{ row.error ? '目录不可访问' : row.space?.frozen ? '已冻结' : row.space ? '可用' : '未分配' }}</n-tag></td>
          <td><n-button size="small" :disabled="busy || !pools.length" @click="edit(row)">{{ row.space ? '配置 / 续期' : '分配空间' }}</n-button></td>
        </tr></tbody>
      </n-table></div>
    </n-card>
    <n-modal :show="!!editing" preset="card" :title="`配置空间 · ${editing?.username ?? ''}`" style="width: min(520px, 94vw)" @update:show="v => { if (!v) editing = null }">
      <n-form-item label="存储位置"><n-select v-model:value="editPool" :options="poolOptions" :disabled="!!editing?.space || !canConfigurePools" /></n-form-item>
      <n-form-item :label="`容量（GiB），本次最多可分配 ${maxQuotaGiB.toFixed(3)}`"><n-input-number v-model:value="quotaGiB" :min="0.001" :max="maxQuotaGiB" :precision="3" /></n-form-item>
      <n-form-item label="有效期（清空即不限期）"><n-date-picker v-model:value="expiry" type="datetime" clearable /></n-form-item>
      <n-form-item label="允许访问"><n-switch v-model:value="enabled" /></n-form-item>
      <n-space justify="end"><n-button :disabled="busy" @click="editing = null">取消</n-button><n-button type="primary" :loading="busy" @click="save">保存</n-button></n-space>
    </n-modal>
  </n-space>
</template>

<style scoped>
.table-scroll { overflow-x: auto; }
.path { overflow-wrap: anywhere; }
</style>
