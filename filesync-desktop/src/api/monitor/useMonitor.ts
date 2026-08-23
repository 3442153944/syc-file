// api/monitor/useMonitor.ts
// 系统监控数据源。
//
// ── 为什么不是 HTTP 轮询 ──────────────────────────────────
// 监控是「服务端定时产出、多端订阅」，用轮询是错配：每次请求都重新鉴权+建响应，
// 且实时性被轮询间隔卡死，服务端还要为每个轮询各采样一次（CPU 采样阻塞 300ms）。
// 桌面端（Tauri）改走已建的 WS：进页面 subscribe_monitor → Rust 收到 monitor 帧 →
// emit `monitor-metrics` 事件 → 这里接收。离开页面 unsubscribe，服务端随即停止采样。
//
// Web 端没有常驻 WS（那条连接在 Rust 里），退化成 HTTP 轮询兜底。
import { ref, onMounted, onUnmounted } from 'vue'
import { invoke, isTauri } from '@tauri-apps/api/core'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import { httpGet } from '../http'

export interface CpuInfo {
  used_percent: number
  cores: number
  model_name: string
  per_core: number[]
  load1: number
  load5: number
  load15: number
}
export interface MemInfo {
  total: number
  used: number
  free: number
  used_percent: number
  swap_total: number
  swap_used: number
}
export interface HostInfo {
  hostname: string
  os: string
  platform: string
  platform_version: string
  kernel_arch: string
  uptime_seconds: number
  procs: number
  go_version: string
  server_time: number
}
export interface DiskItem {
  path: string
  fstype: string
  total: number
  used: number
  free: number
  used_percent: number
}
export interface SystemMetrics {
  cpu: CpuInfo
  memory: MemInfo
  host: HostInfo
  disks: DiskItem[]
}
export interface NetworkMetrics {
  bytes_sent: number
  bytes_recv: number
  send_rate: number
  recv_rate: number
  interfaces: Array<Record<string, unknown>>
  online_devices: number
  online_users: number
  active_connections: number
  server_time: number
}
export interface MonitorFrame {
  system: SystemMetrics
  network: NetworkMetrics
}

/**
 * 订阅监控数据。返回响应式的 system / network / connected。
 * @param intervalSec 期望推送间隔（秒），服务端夹到 [1,10]；也是 Web 兜底轮询的间隔。
 */
export function useMonitor(intervalSec = 2) {
  const system = ref<SystemMetrics | null>(null)
  const network = ref<NetworkMetrics | null>(null)
  const connected = ref(false)

  let unlisten: UnlistenFn | null = null
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function startTauri() {
    unlisten = await listen<MonitorFrame>('monitor-metrics', (e) => {
      if (e.payload.system) system.value = e.payload.system
      if (e.payload.network) network.value = e.payload.network
      connected.value = true
    })
    await invoke('subscribe_monitor', { interval: intervalSec })
  }

  async function pollOnce() {
    try {
      const [sys, net] = await Promise.all([
        httpGet<SystemMetrics>('/monitor/system'),
        httpGet<NetworkMetrics>('/monitor/network'),
      ])
      system.value = sys
      network.value = net
      connected.value = true
    } catch {
      connected.value = false
    }
  }

  onMounted(() => {
    if (isTauri()) {
      startTauri()
    } else {
      pollOnce()
      pollTimer = setInterval(pollOnce, intervalSec * 1000)
    }
  })

  onUnmounted(() => {
    if (unlisten) unlisten()
    if (pollTimer) clearInterval(pollTimer)
    if (isTauri()) invoke('unsubscribe_monitor').catch(() => {})
  })

  return { system, network, connected }
}

// ── 格式化小工具 ──────────────────────────────────────────
export function fmtBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

export function fmtRate(bytesPerSec: number): string {
  return `${fmtBytes(bytesPerSec)}/s`
}

export function fmtUptime(sec: number): string {
  if (!sec) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d} 天 ${h} 小时`
  if (h > 0) return `${h} 小时 ${m} 分`
  return `${m} 分`
}
