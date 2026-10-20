/**
 * 桌面端组网自动登记。
 *
 * 登录成功后调用一次：用当前登录设备号（Device-Id）作为组网设备名，
 * 后端 /v1/vpn/auto-enroll 会选用户第一个已加入的组并完成登记。
 * 结果写进 localStorage.syc_vpn_device，NodeSwitcher / 主动组网测试都读它。
 *
 * 没加入任何组时后端返回 409，这里静默失败——等管理端把用户加进组后，
 * 下次登录或点「主动组网测试」会自动补上。
 */
import {httpDelete, httpGet, httpGetFrom, httpPost, httpPostFrom} from './http'
import {baseUrl, meshCandidates, meshBaseUrl, netState, pinMeshNode, refreshMeshHealth, resetMeshNode} from './net'

export interface VpnLinkPeer {
    device_id: string
    endpoint: string
    mode: 'direct' | 'relay'
    role?: string
}

export interface VpnLinkResult {
    mode: 'direct' | 'relay' | 'idle' | 'unknown'
    self: { device_id: string; address: string }
    peers: VpnLinkPeer[]
}

// 统一走 http.ts：Tauri 模式由 Rust 侧带 token，Web 模式走 fetch，
// 页面里不要再直接 fetch，否则 Tauri 下会因为没有 localStorage.token 被判未登录。

/** 确保本机有组网设备；返回设备名，失败抛错。 */
export async function ensureVpnDevice(): Promise<string> {
    const cached = localStorage.getItem('syc_vpn_device')
    if (cached) {
        // 在线状态跟隧道无关：只要设备存在就持续上报，避免「连着服务器却显示离线」
        startVpnHeartbeat(cached)
        return cached
    }

    const data = await httpPostFrom<{ device_id: string }>(meshBaseUrl(), '/vpn/auto-enroll')
    const device = data?.device_id
    if (!device) throw new Error('自动入组响应缺少 device_id')
    localStorage.setItem('syc_vpn_device', device)
    startVpnHeartbeat(device)
    return device
}

/** 读当前设备的直连/中继状态。 */
export async function fetchVpnLink(device: string): Promise<VpnLinkResult> {
    const link = await httpGetFrom<VpnLinkResult>(meshBaseUrl(), '/vpn/link', {device_id: device})
    const server = link.peers.find(p => p.role === 'server' || p.device_id === 'syc-server')
    // 以「本机隧道实际配置的服务器端点」为准，而不是后端现在广告的端点：
    // 升级后旧隧道可能还钉在 ddns:51820，直接看 /link 会误报中继。
    // 直连的 WG 服务端口是 51820；服务器 peer 落在别的端口（生产 ali:52000）
    // 说明中间是 frp/中转入口，后端目前只看 endpoint 是否相等，会误报 direct。
    const activeEp = localStorage.getItem(KEY_ACTIVE_SERVER_EP) || server?.endpoint || ''
    const port = activeEp ? endpointPort(activeEp) : null
    if (port && port !== DIRECT_WG_PORT) link.mode = 'relay'
    return link
}

/** WG 默认服务端口；非该端口视为中转入口 */
const DIRECT_WG_PORT = 51820

/** 本机隧道实际配置的服务器端点（用于判断直连还是中转） */
const KEY_ACTIVE_SERVER_EP = 'syc_vpn_server_endpoint'

function endpointPort(endpoint: string): number | null {
    try {
        const u = new URL(`udp://${endpoint}`)
        return u.port ? Number(u.port) : null
    } catch {
        return null
    }
}

/** 是否是 RFC1918 私网 IPv4 */
function isPrivateIPv4(host: string): boolean {
    if (/^10\./.test(host)) return true
    if (/^192\.168\./.test(host)) return true
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(host)) return true
    return false
}

/**
 * 同一局域网内优先直连 VM 的 WireGuard，而不是绕阿里 52000 中继。
 * 节点表里如果有私网地址（例如「内网」http://192.168.31.120/file）且探测可达，
 * 就直接用 <私网IP>:51820；外网/不可达时返回 null，仍走后端的 ali:52000。
 */
function lanServerEndpoint(): string | null {
    for (const node of netState.nodes) {
        let host = ''
        try {
            host = new URL(node.server_url).hostname
        } catch {
            continue
        }
        if (!isPrivateIPv4(host)) continue
        const health = netState.health.find(h => h.id === node.id)
        if (health?.reachable) return `${host}:51820`
    }
    return null
}

export interface MeshDeviceRow {
    id: number
    device_id: string
    username: string
    group: string
    org: string
    address: string
    status: string
    online: boolean
    endpoint: string
    last_seen: string | null
}

export interface MeshOverview {
    devices: MeshDeviceRow[]
    server_address: string
}

export async function fetchMeshOverview(): Promise<MeshOverview> {
    return httpGet<MeshOverview>('/vpn/admin/overview')
}

export async function vpnKick(id: number): Promise<void> {
    await httpPost(`/vpn/admin/devices/${id}/kick`)
}

export async function vpnSuspend(id: number, minutes: number): Promise<void> {
    await httpPost(`/vpn/admin/devices/${id}/suspend`, {minutes})
}

export async function vpnBan(id: number, banned: boolean): Promise<void> {
    await httpPost(`/vpn/admin/devices/${id}/ban`, {banned})
}

export async function vpnReleaseDevice(id: number): Promise<void> {
    await httpDelete(`/vpn/admin/devices/${id}`)
}

export interface VpnPeer {
    device_id?: string
    role?: string
    pubkey: string
    allowed_ips: string[]
    endpoint?: string
}

export interface VpnPeersResult {
    self: { address: string; subnet: string; keepalive: number; mtu: number }
    peers: VpnPeer[]
}

interface VpnIdentity {
    device_id: string
    address: string
    subnet: string
    pubkey: string
    private_key: string
}

interface VpnPeerConfig {
    pubkey: string
    allowed_ips: string[]
    endpoint: string | null
}

/** 服务器 peer 用组网入口（局域网 51820 / 外网 ali:52000），其它 peer 用后端下发的实时端点 */
function buildPeerConfigs(data: VpnPeersResult, lanEp: string | null): VpnPeerConfig[] {
    return data.peers.map(p => {
        const isServer = p.role === 'server' || p.device_id === 'syc-server'
        return {
            pubkey: p.pubkey,
            allowed_ips: p.allowed_ips,
            endpoint: isServer && lanEp ? lanEp : (p.endpoint || null),
        }
    })
}

// 隧道运行期状态：用于隧道启动后重新拉取对端实时端点（打洞）。
// 私钥只在内存里，停止隧道即丢弃。
interface ActiveTunnelState {
    device: string
    base: string
    privateKey: string
    address: string
    subnet: string
    keepalive: number
    lanEp: string | null
    endpoints: Map<string, string>
}

let activeTunnel: ActiveTunnelState | null = null
let peerRefreshTimer: number | undefined
let peerRefreshOneShot: number | undefined

/**
 * Windows：用内嵌/已安装的 WireGuard 启动隧道（Linux 请用 syc up）。
 * 私钥不落盘：每次用当前 JWT 向 /vpn/identity 换取，身份跟着会话走。
 */
export async function startEmbeddedTunnel(device: string): Promise<string> {
    // 组网协调节点按延迟重新选一次（不跟随当前文件节点），否则会拿到别的入口
    // 推导出的 WireGuard 端点，出现「隧道已启动但 172.1.0.254 全丢」。
    resetMeshNode()
    await refreshMeshHealth()
    // 按延迟从低到高试；某个节点组网接口半死时自动落到下一个。
    let identity: VpnIdentity | undefined
    let data: VpnPeersResult | undefined
    let chosen = ''
    let lastErr: unknown
    for (const base of meshCandidates()) {
        try {
            const [id, peers] = await Promise.all([
                httpGetFrom<VpnIdentity>(base, '/vpn/identity', {device_id: device}),
                httpGetFrom<VpnPeersResult>(base, '/vpn/peers', {device_id: device}),
            ])
            identity = id
            data = peers
            chosen = base
            break
        } catch (e) {
            lastErr = e
        }
    }
    if (!identity || !data) {
        throw lastErr instanceof Error ? lastErr : new Error('组网节点均不可用')
    }
    // 固定下来：后续 /vpn/link 读到的必须是这条隧道实际用的那个入口
    pinMeshNode(chosen)
    // 同局域网优先直连 VM 的 51820；外网仍用后端的 ali:52000 中继
    const lanEp = lanServerEndpoint()
    const serverPeer = data.peers.find(p => p.role === 'server' || p.device_id === 'syc-server')
    const activeEp = lanEp || serverPeer?.endpoint || ''
    if (activeEp) localStorage.setItem(KEY_ACTIVE_SERVER_EP, activeEp)
    const peers = buildPeerConfigs(data, lanEp)
    const {invoke} = await import('@tauri-apps/api/core')
    const msg = await invoke<string>('vpn_up_embedded', {
        device,
        privateKey: identity.private_key,
        address: data.self.address,
        subnet: data.self.subnet,
        keepalive: data.self.keepalive ?? 5,
        peers,
    })
    activeTunnel = {
        device,
        base: chosen,
        privateKey: identity.private_key,
        address: data.self.address,
        subnet: data.self.subnet,
        keepalive: data.self.keepalive ?? 5,
        lanEp,
        endpoints: new Map(peers.map(p => [p.pubkey, p.endpoint || ''])),
    }
    startPeerRefresh()
    startVpnHeartbeat(device)
    return msg
}

export async function stopEmbeddedTunnel(): Promise<string> {
    const {invoke} = await import('@tauri-apps/api/core')
    try {
        return await invoke<string>('vpn_down_embedded', {iface: 'syc0'})
    } finally {
        activeTunnel = null
        stopPeerRefresh()
        stopVpnHeartbeat()
        localStorage.removeItem(KEY_ACTIVE_SERVER_EP)
        // 隧道停了就解绑组网节点，下次启动重新按延迟选
        resetMeshNode()
    }
}

// ── 对端端点刷新（打洞）────────────────────────────────────────────────────
// 隧道刚启动时，服务器还没从握手看到本机的 WireGuard endpoint，/peers 里对端
// 端点可能还是空的。启动 5s 后再拉一次，之后每 15s 对比，只有端点变化才重装隧道。

function startPeerRefresh(): void {
    stopPeerRefresh()
    const tick = async () => {
        const st = activeTunnel
        if (!st) return
        try {
            const data = await httpGetFrom<VpnPeersResult>(st.base, '/vpn/peers', {device_id: st.device})
            if (activeTunnel !== st) return
            const peers = buildPeerConfigs(data, st.lanEp)
            let changed = false
            for (const p of peers) {
                if ((st.endpoints.get(p.pubkey) || '') !== (p.endpoint || '')) {
                    changed = true
                    break
                }
            }
            if (!changed) return
            if (activeTunnel !== st) return
            const {invoke} = await import('@tauri-apps/api/core')
            await invoke('vpn_up_embedded', {
                device: st.device,
                privateKey: st.privateKey,
                address: st.address,
                subnet: st.subnet,
                keepalive: st.keepalive,
                peers,
            })
            st.endpoints = new Map(peers.map(p => [p.pubkey, p.endpoint || '']))
        } catch { /* 端点还没就绪 / 节点暂时不可用，下一轮再试 */ }
    }
    peerRefreshOneShot = window.setTimeout(tick, 5_000)
    peerRefreshTimer = window.setInterval(tick, 15_000)
}

function stopPeerRefresh(): void {
    if (peerRefreshOneShot !== undefined) {
        window.clearTimeout(peerRefreshOneShot)
        peerRefreshOneShot = undefined
    }
    if (peerRefreshTimer !== undefined) {
        window.clearInterval(peerRefreshTimer)
        peerRefreshTimer = undefined
    }
}

// ── 在线心跳 ────────────────────────────────────────────────────────────────
// 后端「在线」= last_seen 在 90s 内；没有心跳时设备会一直显示离线。
// 只上报在线与 session 续期，endpoint 留空（直连端点由 STUN/打洞那套另行处理）。

let heartbeatTimer: number | undefined
let heartbeatDevice = ''
/** 上次成功的心跳入口，避免每 2s 都先撞一次被本地代理挡住的节点 */
let heartbeatBase = ''

export async function sendVpnHeartbeat(device: string): Promise<void> {
    const body = {device_id: device, endpoint: ''}
    const bases = heartbeatBase ? [heartbeatBase] : [meshBaseUrl(), baseUrl()]
    for (const base of bases) {
        try {
            await httpPostFrom(base, '/vpn/heartbeat', body)
            heartbeatBase = base
            return
        } catch { /* 换下一个入口 */ }
    }
    heartbeatBase = ''
}

/**
 * 启动 30s 心跳：只用来续 session、刷新客户端 last_seen（主页的客户端在线）。
 * 组网页的「组网在线」由后端看服务器 WireGuard 接口的实时流量，不依赖这个心跳。
 */
export function startVpnHeartbeat(device: string): void {
    if (!device || !localStorage.getItem('token')) return
    if (heartbeatTimer !== undefined && heartbeatDevice === device) return
    stopVpnHeartbeat()
    heartbeatDevice = device
    const tick = () => {
        // 退出登录后不要再发，避免 401 每 30s 刷一次
        if (!localStorage.getItem('token')) {
            stopVpnHeartbeat()
            return
        }
        sendVpnHeartbeat(device).catch(() => {})
    }
    tick()
    heartbeatTimer = window.setInterval(tick, 30_000)
}

export function stopVpnHeartbeat(): void {
    if (heartbeatTimer !== undefined) {
        window.clearInterval(heartbeatTimer)
        heartbeatTimer = undefined
    }
    heartbeatDevice = ''
    heartbeatBase = ''
}
