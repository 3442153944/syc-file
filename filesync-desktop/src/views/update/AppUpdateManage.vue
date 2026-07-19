<script setup lang="ts">
// 应用更新管理（Windows/Web）：上传 APK → 填版本信息 → 发布；下方管理已发布版本。
// APK 上传复用 fileApi.uploadFile（Tauri 传本地路径 / Web 传 File），返回 storage_path+hash+size 供发布登记。
import { ref, onMounted } from 'vue'
import { isTauri } from '@tauri-apps/api/core'
import { open } from '@tauri-apps/plugin-dialog'
import { useMessage, useDialog } from 'naive-ui'
import { uploadFile } from '../../api/file/fileApi'
import { publishRelease, listReleases, updateRelease, deleteRelease } from '../../api/update/updateApi'
import type { AppRelease } from '../../api/update/updateTypes'

const message = useMessage()
const dialog = useDialog()
const inTauri = isTauri()

// ── APK 上传 ──────────────────────────────────────────────
const remoteDir = ref('E:\\FileSync\\updates') // APK 存放目录（须在允许盘内且已存在）
const uploading = ref(false)
const uploaded = ref<{ storage_path: string; file_name: string; file_size: number; file_hash: string } | null>(null)
const webFileInput = ref<HTMLInputElement | null>(null)

async function selectApk() {
  if (!remoteDir.value.trim()) {
    message.warning('请先填写 APK 存放目录')
    return
  }
  if (inTauri) {
    const sel = await open({ multiple: false, directory: false, filters: [{ name: 'APK', extensions: ['apk'] }] })
    if (!sel) return
    await doUpload(sel as string)
  } else {
    webFileInput.value?.click()
  }
}

async function onWebFile(e: Event) {
  const files = (e.target as HTMLInputElement).files
  if (!files || files.length === 0) return
  await doUpload(files[0])
  ;(e.target as HTMLInputElement).value = ''
}

async function doUpload(entry: string | File) {
  uploading.value = true
  uploaded.value = null
  try {
    const res = await uploadFile(entry, remoteDir.value.trim())
    uploaded.value = {
      storage_path: res.storage_path,
      file_name: res.file_name,
      file_size: res.file_size,
      file_hash: res.file_hash,
    }
    message.success(`APK 上传完成：${res.file_name}`)
  } catch (err: any) {
    message.error(`上传失败：${err?.message || err}`)
  } finally {
    uploading.value = false
  }
}

// ── 发布 ──────────────────────────────────────────────────
const form = ref({
  version_code: null as number | null,
  version_name: '',
  notes: '',
  mandatory: false,
  min_version_code: 0,
})
const publishing = ref(false)

async function doPublish() {
  if (!uploaded.value) {
    message.warning('请先上传 APK')
    return
  }
  if (!form.value.version_code || !form.value.version_name.trim()) {
    message.warning('请填写版本号与版本名')
    return
  }
  publishing.value = true
  try {
    await publishRelease({
      platform: 'android',
      version_code: form.value.version_code,
      version_name: form.value.version_name.trim(),
      notes: form.value.notes.trim(),
      file_path: uploaded.value.storage_path,
      file_name: uploaded.value.file_name,
      file_size: uploaded.value.file_size,
      file_hash: uploaded.value.file_hash,
      mandatory: form.value.mandatory,
      min_version_code: form.value.min_version_code || 0,
      enabled: true,
    })
    message.success('发布成功，已推送在线设备')
    uploaded.value = null
    form.value = { version_code: null, version_name: '', notes: '', mandatory: false, min_version_code: 0 }
    await refresh()
  } catch (err: any) {
    message.error(`发布失败：${err?.message || err}`)
  } finally {
    publishing.value = false
  }
}

// ── 版本列表管理 ──────────────────────────────────────────
const releases = ref<AppRelease[]>([])
const loading = ref(false)

async function refresh() {
  loading.value = true
  try {
    const data = await listReleases('android')
    releases.value = data.list || []
  } catch (err: any) {
    message.error(`加载失败：${err?.message || err}`)
  } finally {
    loading.value = false
  }
}

async function toggleEnabled(rel: AppRelease) {
  try {
    await updateRelease(rel.id, { enabled: !rel.enabled })
    rel.enabled = !rel.enabled
    message.success(rel.enabled ? '已上架' : '已下架')
  } catch (err: any) {
    message.error(`操作失败：${err?.message || err}`)
  }
}

async function toggleMandatory(rel: AppRelease) {
  try {
    await updateRelease(rel.id, { mandatory: !rel.mandatory })
    rel.mandatory = !rel.mandatory
  } catch (err: any) {
    message.error(`操作失败：${err?.message || err}`)
  }
}

function confirmDelete(rel: AppRelease) {
  dialog.warning({
    title: '删除版本',
    content: `确定删除 ${rel.version_name} (${rel.version_code})？磁盘上的 APK 不会被删除。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteRelease(rel.id)
        message.success('已删除')
        await refresh()
      } catch (err: any) {
        message.error(`删除失败：${err?.message || err}`)
      }
    },
  })
}

function fmtSize(n: number): string {
  if (!n) return '-'
  const u = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${u[i]}`
}

onMounted(refresh)
</script>

<template>
  <div style="padding: 16px; display: flex; flex-direction: column; gap: 16px;">
    <n-card title="发布新版本 (Android)">
      <n-form label-placement="left" label-width="110">
        <n-form-item label="APK 存放目录">
          <n-input v-model:value="remoteDir" placeholder="须在允许盘内且已存在，如 E:\FileSync\updates" />
        </n-form-item>
        <n-form-item label="APK 文件">
          <n-space vertical style="width:100%">
            <n-space>
              <n-button :loading="uploading" @click="selectApk">
                {{ uploading ? '上传中…' : '选择并上传 APK' }}
              </n-button>
              <input v-if="!inTauri" ref="webFileInput" type="file" accept=".apk" style="display:none" @change="onWebFile" />
            </n-space>
            <n-text v-if="uploaded" depth="3">
              已上传：{{ uploaded.file_name }}（{{ fmtSize(uploaded.file_size) }}）
            </n-text>
          </n-space>
        </n-form-item>
        <n-form-item label="版本号 (code)">
          <n-input-number v-model:value="form.version_code" :min="1" placeholder="整数，比较用，如 2" style="width:200px" />
        </n-form-item>
        <n-form-item label="版本名">
          <n-input v-model:value="form.version_name" placeholder="如 1.1.0" style="width:200px" />
        </n-form-item>
        <n-form-item label="更新说明">
          <n-input v-model:value="form.notes" type="textarea" placeholder="本次更新内容" :autosize="{ minRows: 2 }" />
        </n-form-item>
        <n-form-item label="强制更新">
          <n-switch v-model:value="form.mandatory" />
        </n-form-item>
        <n-form-item label="最低版本号">
          <n-input-number v-model:value="form.min_version_code" :min="0" style="width:200px" />
          <n-text depth="3" style="margin-left:8px">低于此版本号强制更新（0=不启用）</n-text>
        </n-form-item>
        <n-form-item label=" ">
          <n-button type="primary" :loading="publishing" :disabled="!uploaded" @click="doPublish">
            发布
          </n-button>
        </n-form-item>
      </n-form>
    </n-card>

    <n-card title="已发布版本">
      <n-space vertical>
        <n-button size="small" @click="refresh" :loading="loading">刷新</n-button>
        <n-table :bordered="false" size="small">
          <thead>
            <tr>
              <th>版本名</th><th>版本号</th><th>大小</th><th>强制</th><th>上架</th><th>说明</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="rel in releases" :key="rel.id">
              <td>{{ rel.version_name }}</td>
              <td>{{ rel.version_code }}</td>
              <td>{{ fmtSize(rel.file_size) }}</td>
              <td><n-switch size="small" :value="rel.mandatory" @update:value="() => toggleMandatory(rel)" /></td>
              <td><n-switch size="small" :value="rel.enabled" @update:value="() => toggleEnabled(rel)" /></td>
              <td style="max-width:220px; white-space:pre-wrap;">{{ rel.notes }}</td>
              <td><n-button size="tiny" type="error" @click="confirmDelete(rel)">删除</n-button></td>
            </tr>
            <tr v-if="releases.length === 0">
              <td colspan="7" style="text-align:center; color:#999;">暂无发布版本</td>
            </tr>
          </tbody>
        </n-table>
      </n-space>
    </n-card>
  </div>
</template>
