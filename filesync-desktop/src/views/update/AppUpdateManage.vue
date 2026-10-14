<script setup lang="ts">
import {ref, computed, onMounted, watch} from 'vue'
import {invoke, isTauri} from '@tauri-apps/api/core'
import {open} from '@tauri-apps/plugin-dialog'
import {useMessage, useDialog} from 'naive-ui'
import {uploadFile} from '../../api/file/fileApi'
import {publishRelease, listReleases, updateRelease, deleteRelease} from '../../api/update/updateApi'
import type {AppRelease} from '../../api/update/updateTypes'
import {desktopVersionCode, inferDesktopArtifact, type ReleasePlatform} from '@/utils/releaseTarget'

const message = useMessage()
const dialog = useDialog()
const inTauri = isTauri()
const platform = ref<ReleasePlatform>('android')
const arch = ref('x86_64')
const bundleType = ref('nsis')
const signature = ref('')
const remoteDir = ref('/mnt/data/file_sync/updates')
const uploading = ref(false)
const publishing = ref(false)
const uploaded = ref<{storage_path: string; file_name: string; file_size: number; file_hash: string} | null>(null)
const webFileInput = ref<HTMLInputElement | null>(null)
const webSigInput = ref<HTMLInputElement | null>(null)
const platformOptions = [{label:'Android', value:'android'}, {label:'Windows', value:'windows'}, {label:'Linux', value:'linux'}]
const archOptions = [{label:'x64 / amd64', value:'x86_64'}, {label:'ARM64', value:'aarch64'}, {label:'x86（32位）', value:'i686'}]
const bundleOptions = computed(() => platform.value === 'windows' ? [{label:'安装程序 (.exe)',value:'nsis'}]
  : [{label:'Debian / Ubuntu (.deb)',value:'deb'}, {label:'AppImage',value:'appimage'}])
const extensions = computed(() => platform.value === 'android' ? ['apk'] : platform.value === 'windows' ? ['exe'] : ['deb','AppImage'])
const busy = computed(() => uploading.value || publishing.value)
const form = ref({version_code:null as number | null, version_name:'', notes:'', mandatory:false, min_version_code:0})
watch(platform, value => {
  uploaded.value = null; signature.value = ''
  bundleType.value = value === 'linux' ? 'deb' : 'nsis'
})
watch(() => form.value.version_name, name => {
  const code = desktopVersionCode(name)
  if (code !== null) form.value.version_code = code
})
async function selectPackage() {
  if (!remoteDir.value.trim()) { message.warning('请填写安装包存放目录'); return }
  if (!inTauri) { webFileInput.value?.click(); return }
  const path = await open({multiple:false, directory:false, filters:[{name:'安装包',extensions:extensions.value}]})
  if (!path || typeof path !== 'string') return
  signature.value = ''
  if (platform.value !== 'android') {
    try { signature.value = await invoke<string>('read_update_signature', {path:path + '.sig'}) }
    catch { message.info('未找到配套签名文件，请选择安装包旁的 .sig 文件') }
  }
  await doUpload(path, path.split(/[\\/]/).pop() || '')
}
async function onWebFile(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  const file = files.find(f => !f.name.endsWith('.sig'))
  const sig = files.find(f => f.name.endsWith('.sig'))
  if (sig && sig.size > 16384) { message.error('签名文件过大，请选择配套 .sig'); input.value = ''; return }
  signature.value = sig ? (await sig.text()).trim() : ''
  if (file) await doUpload(file, file.name)
  input.value = ''
}
async function selectSignature() {
  if (!inTauri) { webSigInput.value?.click(); return }
  const path = await open({multiple:false, filters:[{name:'更新签名',extensions:['sig']}]})
  if (path && typeof path === 'string') {
    try { signature.value = await invoke<string>('read_update_signature', {path}) }
    catch (e) { message.error(String(e)) }
  }
}
async function onWebSignature(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file && file.size <= 16384) signature.value = (await file.text()).trim()
  else message.error('请选择配套的 .sig 签名文件')
  input.value = ''
}
async function doUpload(entry: string | File, name: string) {
  uploading.value = true; uploaded.value = null
  try {
    if (platform.value !== 'android') {
      const inferred = inferDesktopArtifact(name)
      if (!inferred.bundle || (platform.value === 'windows' && inferred.bundle !== 'nsis') || (platform.value === 'linux' && inferred.bundle === 'nsis')) throw new Error('安装包与所选平台不匹配')
      bundleType.value = inferred.bundle
      if (inferred.arch) arch.value = inferred.arch
      if (inferred.version) form.value.version_name = inferred.version
    } else if (!name.toLowerCase().endsWith('.apk')) throw new Error('Android 请选择 APK')
    const res = await uploadFile(entry, remoteDir.value.trim(), () => {}, 'timestamp')
    uploaded.value = {storage_path:res.storage_path, file_name:res.file_name, file_size:res.file_size, file_hash:res.file_hash}
    message.success('安装包已上传')
  } catch (e: any) { message.error(e?.message || String(e)) }
  finally { uploading.value = false }
}
async function doPublish() {
  if (!uploaded.value) { message.warning('请先上传安装包'); return }
  if (!form.value.version_code || !form.value.version_name.trim() || !Number.isInteger(form.value.version_code) || !Number.isInteger(form.value.min_version_code)) {
    message.warning('请填写有效版本名和整数版本号'); return
  }
  if (platform.value !== 'android' && (!signature.value || desktopVersionCode(form.value.version_name) !== form.value.version_code)) {
    message.warning('桌面包需要配套签名，版本名与安装包自带版本一致（x.y.z）'); return
  }
  if (platform.value !== 'android') {
    const artifact = inferDesktopArtifact(uploaded.value.file_name)
    if (artifact.version && artifact.version !== form.value.version_name.trim()) {
      message.warning(`安装包版本为 ${artifact.version}，请使用相同版本发布，或重新构建目标版本的安装包`)
      return
    }
  }
  publishing.value = true
  try {
    await publishRelease({
      platform:platform.value, arch:platform.value === 'android' ? '' : arch.value,
      bundle_type:platform.value === 'android' ? '' : bundleType.value,
      signature:platform.value === 'android' ? '' : signature.value,
      ...form.value, version_code:form.value.version_code!, version_name:form.value.version_name.trim(),
      file_path:uploaded.value.storage_path, file_name:uploaded.value.file_name,
      file_size:uploaded.value.file_size, file_hash:uploaded.value.file_hash, enabled:true,
    })
    message.success('发布成功，已通知在线设备')
    uploaded.value = null; signature.value = ''
    form.value = {version_code:null, version_name:'', notes:'', mandatory:false, min_version_code:0}
    await refresh()
  } catch (e: any) { message.error(e?.message || String(e)) }
  finally { publishing.value = false }
}
const releases = ref<AppRelease[]>([])
const loading = ref(false)
const filter = ref('')
async function refresh() {
  loading.value = true
  try { releases.value = (await listReleases(filter.value)).list || [] }
  catch (e: any) { message.error(e?.message || String(e)) }
  finally { loading.value = false }
}
watch(filter, refresh)
async function toggle(rel: AppRelease, field: 'enabled' | 'mandatory') {
  try { await updateRelease(rel.id, {[field]:!rel[field]}); rel[field] = !rel[field] }
  catch (e: any) { message.error(e?.message || String(e)) }
}
function confirmDelete(rel: AppRelease) {
  dialog.warning({title:'删除版本', content:`删除 ${rel.platform} ${rel.version_name}？服务器上的安装包会保留。`,
    positiveText:'删除', negativeText:'取消', onPositiveClick:async () => {
      try { await deleteRelease(rel.id); await refresh() }
      catch (e: any) { message.error(e?.message || String(e)) }
    }})
}
const fmtSize = (bytes: number) => bytes >= 1048576 ? (bytes / 1048576).toFixed(1) + ' MB' : (bytes / 1024).toFixed(1) + ' KB'
onMounted(refresh)
</script>

<template>
  <div style="padding:16px; display:flex; flex-direction:column; gap:16px">
    <n-card title="发布新版本">
      <n-form label-placement="left" label-width="110">
        <n-form-item label="平台"><n-select v-model:value="platform" :options="platformOptions" :disabled="busy" /></n-form-item>
        <n-form-item v-if="platform !== 'android'" label="目标架构"><n-select v-model:value="arch" :options="archOptions" :disabled="busy" /></n-form-item>
        <n-form-item v-if="platform !== 'android'" label="安装包类型"><n-select v-model:value="bundleType" :options="bundleOptions" :disabled="busy || !!uploaded" /></n-form-item>
        <n-form-item label="存放目录"><n-input v-model:value="remoteDir" placeholder="服务器上已存在且允许访问的目录" :disabled="busy" /></n-form-item>
        <n-form-item label="安装包">
          <n-space vertical>
            <n-button :loading="uploading" :disabled="publishing" @click="selectPackage">选择并上传安装包</n-button>
            <input v-if="!inTauri" ref="webFileInput" type="file" multiple :accept="extensions.map(e => '.'+e).join(',') + ',.sig'" style="display:none" @change="onWebFile" />
            <n-text v-if="uploaded" depth="3">{{ uploaded.file_name }}（{{ fmtSize(uploaded.file_size) }}）</n-text>
          </n-space>
        </n-form-item>
        <n-form-item v-if="platform !== 'android'" label="更新签名">
          <n-space align="center">
            <n-tag :type="signature ? 'success' : 'warning'">{{ signature ? '已读取配套签名' : '未选择签名' }}</n-tag>
            <n-button size="small" :disabled="busy" @click="selectSignature">选择 .sig 文件</n-button>
            <input v-if="!inTauri" ref="webSigInput" type="file" accept=".sig" style="display:none" @change="onWebSignature" />
          </n-space>
        </n-form-item>
        <n-form-item label="版本名"><n-input v-model:value="form.version_name" placeholder="如 0.1.1，与安装包版本一致" :disabled="busy" /></n-form-item>
        <n-form-item label="版本号">
          <n-input-number v-model:value="form.version_code" :min="1" :precision="0" :disabled="busy || platform !== 'android'" />
          <n-text depth="3" style="margin-left:8px">自动生成；Android 应与 APK 内的 versionCode 一致</n-text>
        </n-form-item>
        <n-form-item label="更新说明"><n-input v-model:value="form.notes" type="textarea" :autosize="{minRows:2}" :disabled="busy" /></n-form-item>
        <n-form-item label="强制更新"><n-switch v-model:value="form.mandatory" :disabled="busy" /></n-form-item>
        <n-form-item label="最低版本号"><n-input-number v-model:value="form.min_version_code" :min="0" :precision="0" :disabled="busy" /></n-form-item>
        <n-form-item label=" "><n-button type="primary" :loading="publishing" :disabled="!uploaded || uploading || (platform !== 'android' && !signature)" @click="doPublish">发布</n-button></n-form-item>
      </n-form>
    </n-card>
    <n-card title="已发布版本">
      <n-space vertical>
        <n-space><n-select v-model:value="filter" :options="[{label:'全部平台',value:''}, ...platformOptions]" style="width:180px" /><n-button size="small" :loading="loading" @click="refresh">刷新</n-button></n-space>
        <n-table :bordered="false" size="small">
          <thead><tr><th>平台 / 架构</th><th>包类型</th><th>版本</th><th>大小</th><th>强制</th><th>上架</th><th>说明</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="rel in releases" :key="rel.id">
              <td>{{ rel.platform }} {{ rel.arch }}</td><td>{{ rel.bundle_type || 'APK' }}</td>
              <td>{{ rel.version_name }}（{{ rel.version_code }}）</td><td>{{ fmtSize(rel.file_size) }}</td>
              <td><n-switch size="small" :value="rel.mandatory" @update:value="() => toggle(rel, 'mandatory')" /></td>
              <td><n-switch size="small" :value="rel.enabled" @update:value="() => toggle(rel, 'enabled')" /></td>
              <td style="max-width:220px;white-space:pre-wrap">{{ rel.notes }}</td>
              <td><n-button size="tiny" type="error" @click="confirmDelete(rel)">删除</n-button></td>
            </tr>
            <tr v-if="!releases.length"><td colspan="8" style="text-align:center;color:#999">暂无发布版本</td></tr>
          </tbody>
        </n-table>
      </n-space>
    </n-card>
  </div>
</template>
