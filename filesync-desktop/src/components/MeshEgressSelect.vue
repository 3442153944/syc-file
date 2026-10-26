<script setup lang="ts">
// 组网出口网卡：只有直出物理网卡的握手才被判定为可信直连。选择后桌面端会把隧道
// 外层流量（对端/中继服务器端点）的 /32 路由固定到该网卡，避免被系统 VPN 抢走默认路由。
import {onMounted, ref} from 'vue'
import {NSelect, NSpace, NText, useMessage} from 'naive-ui'
import {
  fetchPhysicalInterfaces, setMeshEgress, storedEgress, type PhysicalInterface,
} from '@/api/vpn'

const AUTO = -1
const message = useMessage()
const ifIndex = ref<number>(storedEgress() ?? AUTO)
const options = ref<{label: string; value: number}[]>([
  {label: '自动（跟随系统路由）', value: AUTO},
])
const busy = ref(false)

async function load() {
  const interfaces: PhysicalInterface[] = await fetchPhysicalInterfaces()
  options.value = [
    {label: '自动（跟随系统路由）', value: AUTO},
    ...interfaces.map(it => ({
      label: `${it.name}${it.addresses.length ? ' · ' + it.addresses[0] : ''}`,
      value: it.if_index,
    })),
  ]
  // 之前选中的网卡已不存在（拔掉/禁用）时回到自动，避免用户误以为仍固定着。
  if (ifIndex.value !== AUTO && !interfaces.some(it => it.if_index === ifIndex.value)) {
    ifIndex.value = AUTO
  }
}

async function apply(value: number) {
  busy.value = true
  try {
    await setMeshEgress(value === AUTO ? null : value)
    ifIndex.value = value
    message.success(value === AUTO ? '已恢复跟随系统路由' : '组网外层流量已固定到所选物理网卡')
  } catch (e) {
    message.error(String(e))
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <NSpace align="center" :size="8">
    <NText depth="3" style="font-size: 12px">组网出口网卡</NText>
    <NSelect :value="ifIndex" :options="options" :loading="busy" size="small" style="width: 260px"
             @update:value="apply"/>
    <NText depth="3" style="font-size: 12px">
      只有直出物理网卡的握手才算可信直连；选择后隧道外层流量（含中继服务器）固定走该网卡
    </NText>
  </NSpace>
</template>
