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
  // 进程/端口明细：服务端按 config.monitor 的间隔（默认 30s）独立采集，这里
  // 只是把"最近一次采到的结果"捎带在每帧监控推送里，不是每帧都重新采一次；
  // 采集还没跑过一轮时（刚启动等）不带这三个字段。
  processes?: ProcessInfo[]
  listening_ports?: ListeningPort[]
  port_connections?: PortConnCount[]
}

/** 历史采样点（画图用），字段比实时快照精简。见后端 internal/monitor/history.go。 */
export interface HistoryPoint {
  t: number // unix 秒
  cpu: number
  mem: number
  mem_used: number
  send_rate: number
  recv_rate: number
  online_devices: number
  active_connections: number
}

/**
 * 拉最近 days 天的历史采样点。7 天内服务端直接查 Redis，更早的从 MySQL 长期
 * 归档表补（服务端每天批量归档一次），对这里透明；超出范围会被服务端夹住。
 * 按时间升序返回。
 */
export function fetchMonitorHistory(days = 1): Promise<HistoryPoint[]> {
  return httpGet<HistoryPoint[]>('/monitor/history', { days: String(days) })
}

/** 一次采集里进入 Top-N 的一个进程。见后端 internal/monitor/sys_detail.go。 */
export interface ProcessInfo {
  pid: number
  name: string
  cpu_percent: number
  mem_bytes: number
  mem_percent: number
  disk_read_bytes: number
  disk_write_bytes: number
  connections: number
  score: number
}
export interface ProcessFrame {
  t: number
  processes: ProcessInfo[]
  /** 本帧归一化 cpu_percent 用的逻辑核数，诊断用——和任务管理器的核数对不上说明有 bug */
  num_cpus?: number
}

/**
 * 一条资源告警。见后端 internal/monitor/resource_alert.go。
 * 触发条件看的是单核占用（trigger_core/trigger_cpu_percent），不是单个进程——
 * top_processes 只是触发那一刻顺手存的 Top-N 进程快照，供排查参考，不是触发原因。
 */
export interface ResourceAlert {
  id: number
  triggered_at: number // unix 秒
  trigger_core: number // 触发告警的核心序号（0-based）
  trigger_cpu_percent: number // 该核心当时的占用率
  top_processes: ProcessInfo[]
  status: 'active' | 'resolved'
  resolved_at: number // 0 表示未恢复
}

/** 资源告警分页结果。active_count 是整个时间范围内「进行中」的总数，不是当前页的。 */
export interface ResourceAlertPage {
  list: ResourceAlert[]
  total: number
  page: number
  page_size: number
  active_count: number
}

/** 分页拉最近 days 天的资源告警历史（服务端分页），按触发时间倒序。status 不传则不过滤。 */
export function fetchResourceAlerts(
  days = 7,
  page = 1,
  pageSize = 20,
  status?: 'active' | 'resolved',
): Promise<ResourceAlertPage> {
  const params: Record<string, string> = {
    days: String(days),
    page: String(page),
    page_size: String(pageSize),
  }
  if (status) params.status = status
  return httpGet<ResourceAlertPage>('/monitor/alerts', params)
}

/** 一个正在监听的端口。 */
export interface ListeningPort {
  port: number
  protocol: 'tcp' | 'udp'
  pid: number
  process_name: string
}
/** 某端口/协议当前的连接数（聚合，不含每条连接明细）。 */
export interface PortConnCount {
  port: number
  protocol: 'tcp' | 'udp'
  connections: number
}
export interface PortFrame {
  t: number
  listening_ports: ListeningPort[]
  port_connections: PortConnCount[]
}

/**
 * 进程 Top-N 采集历史。不传 name：概览首次加载用，days 给小值即可；传 name：
 * 详情用，只回这一个进程名的数据，days 可以给大——PID 会在进程重启后变，
 * 跨时间范围认同一个进程只能按名字，不能按 PID。
 */
export function fetchProcessHistory(days = 1, name?: string): Promise<ProcessFrame[]> {
  const params: Record<string, string> = { days: String(days) }
  if (name) params.name = name
  return httpGet<ProcessFrame[]>('/monitor/processes', params)
}

/** 概览轮询刷新用：只要最新一帧，O(1)，不用把一整天的明细重新拉一遍。 */
export async function fetchLatestProcesses(): Promise<ProcessFrame | null> {
  const frames = await httpGet<ProcessFrame[]>('/monitor/processes', { latest: '1' })
  return frames[0] ?? null
}

/** 监听端口 + 端口连接数历史。传 port：只回该端口（跨 tcp/udp）的数据。 */
export function fetchPortHistory(days = 1, port?: number): Promise<PortFrame[]> {
  const params: Record<string, string> = { days: String(days) }
  if (port) params.port = String(port)
  return httpGet<PortFrame[]>('/monitor/ports', params)
}

/** 概览轮询刷新用：只要最新一帧，O(1)。 */
export async function fetchLatestPorts(): Promise<PortFrame | null> {
  const frames = await httpGet<PortFrame[]>('/monitor/ports', { latest: '1' })
  return frames[0] ?? null
}

/**
 * 订阅监控数据。返回响应式的 system / network / processes / listeningPorts /
 * portConnections / connected。
 *
 * 进程/端口明细复用同一条监控推送（Tauri 下是 WS，见 MonitorFrame 的注释），
 * 不另开 HTTP 轮询——那样等于给同一份数据建第二条连接，白多一份连接建立开销。
 * Web 端没有常驻 WS，退化成轮询时把这两个接口也带上，语义保持一致。
 *
 * @param intervalSec 期望推送间隔（秒），服务端夹到 [1,10]；也是 Web 兜底轮询的间隔。
 */
export function useMonitor(intervalSec = 2) {
  const system = ref<SystemMetrics | null>(null)
  const network = ref<NetworkMetrics | null>(null)
  const processes = ref<ProcessInfo[] | null>(null)
  const listeningPorts = ref<ListeningPort[] | null>(null)
  const portConnections = ref<PortConnCount[] | null>(null)
  const connected = ref(false)

  let unlisten: UnlistenFn | null = null
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function startTauri() {
    unlisten = await listen<MonitorFrame>('monitor-metrics', (e) => {
      if (e.payload.system) system.value = e.payload.system
      if (e.payload.network) network.value = e.payload.network
      if (e.payload.processes) processes.value = e.payload.processes
      if (e.payload.listening_ports) listeningPorts.value = e.payload.listening_ports
      if (e.payload.port_connections) portConnections.value = e.payload.port_connections
      connected.value = true
    })
    await invoke('subscribe_monitor', { interval: intervalSec })
  }

  async function pollOnce() {
    try {
      const [sys, net, latestProcs, latestPorts] = await Promise.all([
        httpGet<SystemMetrics>('/monitor/system'),
        httpGet<NetworkMetrics>('/monitor/network'),
        fetchLatestProcesses().catch(() => null),
        fetchLatestPorts().catch(() => null),
      ])
      system.value = sys
      network.value = net
      if (latestProcs) processes.value = latestProcs.processes
      if (latestPorts) {
        listeningPorts.value = latestPorts.listening_ports
        portConnections.value = latestPorts.port_connections
      }
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

  return { system, network, processes, listeningPorts, portConnections, connected }
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
