/**
 * net.ts — 前端统一网络出口（节点注册表 + 健康探测 + 灾备切换）
 *
 * 和 Rust 的 net.rs 是同一套模型的两端：
 *
 *   Tauri 模式：节点表、探测、自动灾备全在 Rust（HTTP/WS 也都在 Rust 发），这里只是
 *              一层壳 —— 通过 invoke 读写，通过事件 `network-status` / `network-node-changed`
 *              被动接收状态。前端自己发出去的少量请求（http.ts 的 fetch、下载直链、
 *              头像 URL）也从这里取 baseUrl，保证和 Rust 用的是同一个节点。
 *
 *   Web  模式：没有 Rust，节点表存 localStorage，探测/切换由这里自己做。默认节点是
 *              「当前站点」（相对路径 /file，走 nginx 反代），另外两个绝对地址的节点
 *              属于跨域访问，用户显式选了才会用。
 *
 * 切换节点时会 abort 掉所有在途 fetch（见 registerAbort）——这就是「切节点不用等
 * 所有请求跑完」的那一半；另一半（Rust 侧的 HTTP/WS）由 net.rs 的世代号负责。
 */

import {isTauri} from '@tauri-apps/api/core'
import {reactive} from 'vue'

// ── 类型（字段名与 Rust net.rs 的序列化结果一一对应） ──────────────────────────

/** 一个服务器入口。注意 Rust 侧 ServerNode 用的是 snake_case */
export interface ServerNode {
    id: string
    name: string
    server_url: string
    ws_url: string
    builtin: boolean
}

/** 单个节点最近一次探测结果 */
export interface NodeHealth {
    id: string
    name: string
    serverUrl: string
    reachable: boolean
    latencyMs: number
    checkedAt: number
    message: string
    serverNode: string
    version: string
    clockSkewMs: number
    wsConns: number
    uptime: number
}

export interface NetStatus {
    activeId: string
    activeName: string
    serverUrl: string
    wsUrl: string
    autoFailover: boolean
    intervalMinutes: number
    nodes: ServerNode[]
    health: NodeHealth[]
    offline: boolean
    epoch: number
}

/** ping 接口返回的简要信息 */
export interface PingData {
    status?: string
    node?: string
    version?: string
    server_time?: number
    uptime?: number
    ws_conns?: number
}

// ── localStorage 键（Web 模式用） ─────────────────────────────────────────────

const KEY_NODES = 'filesync_nodes'
const KEY_ACTIVE = 'filesync_active_node'
const KEY_FAILOVER = 'filesync_failover'
/** 旧版只存一个地址，升级上来时要迁移 */
const KEY_LEGACY_SERVER = 'filesync_server_url'

/** 默认巡检间隔：15 分钟确认一次链路存活 */
export const DEFAULT_INTERVAL_MINUTES = 15

/** 两个内置节点，与 Rust config.rs 的 default_nodes() 保持一致 */
function builtinNodes(): ServerNode[] {
    return [
        {
            id: 'ddns',
            name: '主节点 · ddns',
            server_url: 'https://ddns.sunyuanling.cn/file',
            ws_url: 'wss://ddns.sunyuanling.cn/file',
            builtin: true,
        },
        {
            id: 'jp',
            name: '备用节点 · 东京',
            // 东京这条隧道在 VPS 上占的是 8443（frpc_akile.toml: local 443 → remote 8443）
            server_url: 'https://jp.sunyuanling.cn:8443/file',
            ws_url: 'wss://jp.sunyuanling.cn:8443/file',
            builtin: true,
        },
    ]
}

/** Web 模式的首选节点：当前站点，相对路径交给 nginx 反代，天然同源无跨域问题 */
function siteNode(): ServerNode {
    return {
        id: 'site',
        name: '当前站点',
        server_url: '/file',
        ws_url: (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/file',
        builtin: true,
    }
}

// ── 全局状态 ─────────────────────────────────────────────────────────────────

const empty: NetStatus = {
    activeId: '',
    activeName: '',
    serverUrl: '',
    wsUrl: '',
    autoFailover: true,
    intervalMinutes: DEFAULT_INTERVAL_MINUTES,
    nodes: [],
    health: [],
    offline: false,
    epoch: 0,
}

/** 响应式网络状态，菜单栏指示灯 / 网络面板直接绑它 */
export const netState = reactive<NetStatus>({...empty})

let initialized = false
let webTimer: number | undefined

// ── 在途请求登记：切节点时统一 abort ───────────────────────────────────────────

const inflight = new Set<AbortController>()

/**
 * 登记一个在途请求的 AbortController。节点一换就把它们全 abort 掉 ——
 * 否则这些请求还挂在旧地址上，最坏要等 TCP 超时才失败，用户看到的就是「切完还在转圈」。
 *
 * 返回注销函数，请求结束（无论成败）时必须调，避免集合无限增长。
 */
export function registerAbort(ctrl: AbortController): () => void {
    inflight.add(ctrl)
    return () => inflight.delete(ctrl)
}

function abortAllInflight(reason: string) {
    for (const c of inflight) {
        try {
            c.abort(reason)
        } catch { /* 已完成的 controller abort 会静默失败，忽略 */
        }
    }
    inflight.clear()
}

// ── 对外：当前地址 ───────────────────────────────────────────────────────────

/** 当前激活节点的 HTTP 基地址（不含 /v1，不带尾斜杠） */
export function baseUrl(): string {
    if (netState.serverUrl) return netState.serverUrl.replace(/\/+$/, '')
    // 还没初始化完（比如 App 刚起、第一条请求就发了）：用同步能拿到的兜底值
    return fallbackBaseUrl()
}

/** 当前激活节点的 WS 基地址（不含 /v1/ws/connect） */
export function wsBaseUrl(): string {
    if (netState.wsUrl) return netState.wsUrl.replace(/\/+$/, '')
    const b = baseUrl()
    if (b.startsWith('https://')) return 'wss://' + b.slice(8)
    if (b.startsWith('http://')) return 'ws://' + b.slice(7)
    return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + b
}

/** 初始化完成前的兜底地址：读 localStorage，再不行用默认节点 */
function fallbackBaseUrl(): string {
    const legacy = localStorage.getItem(KEY_LEGACY_SERVER)
    if (legacy) return legacy.replace(/\/+$/, '')
    const nodes = loadWebNodes()
    const activeId = localStorage.getItem(KEY_ACTIVE) || ''
    const node = nodes.find(n => n.id === activeId) || nodes[0]
    return node ? node.server_url.replace(/\/+$/, '') : ''
}

// ── 初始化 ───────────────────────────────────────────────────────────────────

/** 由 main.ts 调一次。重复调用无副作用。 */
export async function initNet(): Promise<void> {
    if (initialized) return
    initialized = true
    if (isTauri()) {
        await initTauri()
    } else {
        initWeb()
    }
}

async function initTauri() {
    const {invoke} = await import('@tauri-apps/api/core')
    const {listen} = await import('@tauri-apps/api/event')
    try {
        applyStatus(await invoke<NetStatus>('get_network_status'))
    } catch { /* Rust 还没起来也不要紧，事件会补上 */
    }
    await listen<NetStatus>('network-status', e => applyStatus(e.payload))
    await listen('network-node-changed', () => {
        // Rust 已经掐掉了它自己的在途连接，前端这边的 fetch 也一并放弃
        abortAllInflight('节点已切换')
    })
}

function initWeb() {
    const nodes = loadWebNodes()
    const activeId = localStorage.getItem(KEY_ACTIVE) || nodes[0]?.id || ''
    const active = nodes.find(n => n.id === activeId) || nodes[0]
    Object.assign(netState, {
        activeId: active?.id || '',
        activeName: active?.name || '',
        serverUrl: active?.server_url || '',
        wsUrl: active?.ws_url || '',
        autoFailover: loadWebFailover().auto,
        intervalMinutes: loadWebFailover().minutes,
        nodes,
        health: [],
        offline: false,
        epoch: 0,
    })
    scheduleWebProbe()
}

function applyStatus(s: NetStatus) {
    Object.assign(netState, s)
    // 头像 URL、下载直链这些地方还在用 platform.ts 的 localStorage 值，同步一份过去
    if (s.serverUrl) localStorage.setItem(KEY_LEGACY_SERVER, s.serverUrl.replace(/\/+$/, ''))
}

// ── 探测 ─────────────────────────────────────────────────────────────────────

/** 探测超时：和 Rust 侧一致，6 秒不回话就当它不可用 */
const PROBE_TIMEOUT_MS = 6000

/**
 * 直接 ping 一个地址，返回 (耗时, 简要信息)。Web 模式巡检和「立即检测」用。
 *
 * 和 Rust 侧 net.rs 的 ping_url 同样做两处兼容：GET 撞 404/405 就退回 POST
 * （旧后端只注册了 POST /v1/ping），响应没有 code 字段也算通。
 */
export async function pingUrl(serverUrl: string): Promise<{ latencyMs: number; data: PingData }> {
    const url = `${serverUrl.replace(/\/+$/, '')}/v1/ping`
    const ctrl = new AbortController()
    const timer = window.setTimeout(() => ctrl.abort('探测超时'), PROBE_TIMEOUT_MS)
    const started = performance.now()
    try {
        let res = await fetch(url, {method: 'GET', signal: ctrl.signal})
        if (res.status === 404 || res.status === 405) {
            res = await fetch(url, {method: 'POST', signal: ctrl.signal})
        }
        const latencyMs = Math.round(performance.now() - started)
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const text = await res.text()
        try {
            const json = JSON.parse(text)
            // 新版信封 {code,message,data}；老版只有 {"message":"pong"}
            if (typeof json.code === 'number' && json.code !== 200) {
                throw new Error(`[${json.code}] ${json.message || '探测失败'}`)
            }
            return {latencyMs, data: (json.data || {}) as PingData}
        } catch (e: any) {
            // 不是 JSON：认 "pong" 而不是「只要 200 就算通」——有些中转会对任意路径
            // 回一个 200 的 HTML，那种节点切过去只会更糟
            if (e instanceof SyntaxError) {
                if (text.includes('pong')) return {latencyMs, data: {}}
                throw new Error('响应不是本服务的 ping 接口')
            }
            throw e
        }
    } finally {
        window.clearTimeout(timer)
    }
}

/** 探测全部节点。Tauri 交给 Rust（它才是真正发请求的那一端），Web 自己探。 */
export async function probeAll(): Promise<NetStatus> {
    if (isTauri()) {
        const {invoke} = await import('@tauri-apps/api/core')
        applyStatus(await invoke<NetStatus>('probe_server_nodes'))
        return netState
    }
    const results = await Promise.all(netState.nodes.map(webProbe))
    netState.health = results
    netState.offline = results.length > 0 && results.every(h => !h.reachable)
    if (netState.offline) {
        // Web 模式没得切：跨域节点未必可用，这里只提示，不擅自换站点
        console.warn('[net] 所有节点均不可达')
    }
    return netState
}

async function webProbe(node: ServerNode): Promise<NodeHealth> {
    const base: NodeHealth = {
        id: node.id,
        name: node.name,
        serverUrl: node.server_url,
        reachable: false,
        latencyMs: PROBE_TIMEOUT_MS,
        checkedAt: Date.now(),
        message: '',
        serverNode: '',
        version: '',
        clockSkewMs: 0,
        wsConns: 0,
        uptime: 0,
    }
    try {
        const {latencyMs, data} = await pingUrl(node.server_url)
        return {
            ...base,
            reachable: true,
            latencyMs,
            message: '',
            serverNode: data.node || '',
            version: data.version || '',
            clockSkewMs: data.server_time ? Date.now() - data.server_time : 0,
            wsConns: data.ws_conns || 0,
            uptime: data.uptime || 0,
        }
    } catch (e: any) {
        return {...base, message: e?.message || String(e)}
    }
}

/** Web 模式的低频巡检定时器 */
function scheduleWebProbe() {
    if (webTimer) window.clearInterval(webTimer)
    const ms = Math.max(1, netState.intervalMinutes) * 60_000
    webTimer = window.setInterval(() => {
        probeAll().catch(() => { /* 探测失败本身就是结果，已写进 health */
        })
    }, ms)
    // 首探延后几秒，别和首屏的登录校验抢
    window.setTimeout(() => probeAll().catch(() => {}), 3000)
}

// ── 节点管理 ─────────────────────────────────────────────────────────────────

/** 切换到指定节点。会立刻放弃所有在途请求。 */
export async function switchNode(nodeId: string): Promise<NetStatus> {
    if (isTauri()) {
        const {invoke} = await import('@tauri-apps/api/core')
        applyStatus(await invoke<NetStatus>('switch_server_node', {nodeId}))
        abortAllInflight('节点已切换')
        return netState
    }
    const node = netState.nodes.find(n => n.id === nodeId)
    if (!node) throw new Error('节点不存在')
    netState.activeId = node.id
    netState.activeName = node.name
    netState.serverUrl = node.server_url
    netState.wsUrl = node.ws_url
    netState.epoch += 1
    saveWebNodes(netState.nodes, node.id)
    localStorage.setItem(KEY_LEGACY_SERVER, node.server_url.replace(/\/+$/, ''))
    abortAllInflight('节点已切换')
    return netState
}

/** 新增（id 传 null）或修改节点；activate=true 时保存后立即切过去。 */
export async function saveNode(input: {
    id?: string | null
    name: string
    serverUrl: string
    wsUrl?: string | null
    activate?: boolean
}): Promise<NetStatus> {
    const serverUrl = input.serverUrl.trim().replace(/\/+$/, '')
    if (!serverUrl) throw new Error('服务器地址不能为空')
    if (isTauri()) {
        const {invoke} = await import('@tauri-apps/api/core')
        applyStatus(await invoke<NetStatus>('save_server_node', {
            id: input.id ?? null,
            name: input.name,
            serverUrl,
            wsUrl: input.wsUrl ?? null,
            activate: input.activate ?? false,
        }))
        return netState
    }
    const wsUrl = (input.wsUrl || '').trim() || deriveWsUrl(serverUrl)
    const name = input.name.trim() || serverUrl
    const nodes = [...netState.nodes]
    let id = input.id || ''
    if (id) {
        const idx = nodes.findIndex(n => n.id === id)
        if (idx < 0) throw new Error('节点不存在')
        nodes[idx] = {...nodes[idx], name, server_url: serverUrl, ws_url: wsUrl}
    } else {
        id = 'custom-' + (crypto.randomUUID?.() || Date.now().toString(36))
        nodes.push({id, name, server_url: serverUrl, ws_url: wsUrl, builtin: false})
    }
    netState.nodes = nodes
    saveWebNodes(nodes, netState.activeId)
    if (input.activate || netState.activeId === id) await switchNode(id)
    return netState
}

/** 删除自定义节点（内置节点不可删）。 */
export async function removeNode(nodeId: string): Promise<NetStatus> {
    if (isTauri()) {
        const {invoke} = await import('@tauri-apps/api/core')
        applyStatus(await invoke<NetStatus>('remove_server_node', {nodeId}))
        return netState
    }
    const node = netState.nodes.find(n => n.id === nodeId)
    if (!node) throw new Error('节点不存在')
    if (node.builtin) throw new Error('内置节点不能删除，可以修改它的地址')
    if (netState.nodes.length <= 1) throw new Error('至少要保留一个节点')
    const nodes = netState.nodes.filter(n => n.id !== nodeId)
    netState.nodes = nodes
    saveWebNodes(nodes, netState.activeId)
    if (netState.activeId === nodeId) await switchNode(nodes[0].id)
    return netState
}

/** 灾备开关 / 巡检间隔（分钟）。 */
export async function setFailover(opts: { autoFailover?: boolean; intervalMinutes?: number }): Promise<NetStatus> {
    if (isTauri()) {
        const {invoke} = await import('@tauri-apps/api/core')
        applyStatus(await invoke<NetStatus>('set_failover_settings', {
            autoFailover: opts.autoFailover ?? null,
            intervalMinutes: opts.intervalMinutes ?? null,
        }))
        return netState
    }
    if (opts.autoFailover !== undefined) netState.autoFailover = opts.autoFailover
    if (opts.intervalMinutes !== undefined) {
        netState.intervalMinutes = Math.min(Math.max(1, opts.intervalMinutes), 24 * 60)
    }
    localStorage.setItem(KEY_FAILOVER, JSON.stringify({
        auto: netState.autoFailover,
        minutes: netState.intervalMinutes,
    }))
    scheduleWebProbe()
    return netState
}

/** 取某个节点最近一次探测结果 */
export function healthOf(nodeId: string): NodeHealth | undefined {
    return netState.health.find(h => h.id === nodeId)
}

export function deriveWsUrl(serverUrl: string): string {
    const s = serverUrl.replace(/\/+$/, '')
    if (s.startsWith('https://')) return 'wss://' + s.slice(8)
    if (s.startsWith('http://')) return 'ws://' + s.slice(7)
    return s
}

// ── Web 模式的本地存储 ───────────────────────────────────────────────────────

function loadWebNodes(): ServerNode[] {
    try {
        const raw = localStorage.getItem(KEY_NODES)
        if (raw) {
            const list = JSON.parse(raw) as ServerNode[]
            if (Array.isArray(list) && list.length > 0) return list
        }
    } catch { /* 存储损坏就当没有，下面重建默认表 */
    }
    return [siteNode(), ...builtinNodes()]
}

function saveWebNodes(nodes: ServerNode[], activeId: string) {
    localStorage.setItem(KEY_NODES, JSON.stringify(nodes))
    localStorage.setItem(KEY_ACTIVE, activeId)
}

function loadWebFailover(): { auto: boolean; minutes: number } {
    try {
        const raw = localStorage.getItem(KEY_FAILOVER)
        if (raw) {
            const v = JSON.parse(raw)
            return {
                auto: v.auto !== false,
                minutes: Number(v.minutes) > 0 ? Number(v.minutes) : DEFAULT_INTERVAL_MINUTES,
            }
        }
    } catch { /* 同上 */
    }
    return {auto: true, minutes: DEFAULT_INTERVAL_MINUTES}
}
