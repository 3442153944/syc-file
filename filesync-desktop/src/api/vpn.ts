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
import {ApiError, httpDelete, httpGet, httpGetFrom, httpPost, httpPostFrom} from './http'
import {baseUrl, meshCandidates, meshBaseUrl, pinMeshNode, refreshMeshHealth, resetMeshNode} from './net'
import {ref} from 'vue'
import {freshMeshHandshake, meshPeerMode, meshIdentityMatches} from '../utils/meshLinkState'

export const meshMaintenanceError = ref('')

export interface VpnLinkPeer {
    device_id: string
    endpoint: string
    mode: 'direct' | 'relay' | 'unknown'
    pubkey?: string
    address?: string
    role?: string
}

export interface VpnLinkResult {
    mode: 'direct' | 'relay' | 'connected' | 'idle' | 'unknown'
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
    const {invoke, isTauri} = await import('@tauri-apps/api/core')
    if (!isTauri()) { link.mode = link.peers.length ? 'unknown' : 'idle'; return link }
    const status = await invoke<{pubkey: string; endpoint: string; handshake_ms: number; allowed_ips: string[]}[]>('vpn_peer_status').catch(() => [])
    const fresh = freshMeshHandshake
    const relay = status.find(p => p.pubkey === server?.pubkey)
    for (const peer of link.peers) {
        const direct = status.find(p => p.pubkey === peer.pubkey)
        peer.mode = meshPeerMode(peer.address, direct, relay)
        if (peer.mode === 'direct') peer.endpoint = direct?.endpoint || peer.endpoint
    }
    const others = link.peers.filter(p => p !== server)
    link.mode = !others.length ? fresh(relay) ? 'connected' : 'unknown'
        : others.some(p => p.mode === 'direct') ? 'direct' : others.some(p => p.mode === 'relay') ? 'relay' : 'unknown'
    return link
}

/** 本机隧道实际配置的服务器端点（用于判断直连还是中转） */
const KEY_ACTIVE_SERVER_EP = 'syc_vpn_server_endpoint'

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
    candidates?: string[]
}

export interface VpnPeersResult {
    self: { address: string; subnet: string; pubkey: string; keepalive: number; mtu: number }
    peers: VpnPeer[]
}

interface VpnIdentity {
    device_id: string
    address: string
    subnet: string
    pubkey: string
    private_key: string
}

// Authenticated delivery only. Never persist this object in localStorage/config.
let memoryIdentity: {token: string; base: string; device: string; value: VpnIdentity} | null = null
let identityGeneration = 0

export function clearMeshIdentity(): void { memoryIdentity = null; identityGeneration++ }

export async function afterMeshAuthentication(token: string): Promise<void> {
    const {isTauri} = await import('@tauri-apps/api/core')
    if (!isTauri() || !token) return
    const base = meshBaseUrl()
    if (memoryIdentity?.token === token && memoryIdentity.base === base) return
    clearMeshIdentity()
    const generation = identityGeneration
    try {
        const enrolled = await httpPostFrom<{device_id: string}>(base, '/vpn/auto-enroll')
        const value = await httpGetFrom<VpnIdentity>(base, '/vpn/identity', {device_id: enrolled.device_id})
        if (generation !== identityGeneration) return
        memoryIdentity = {token, base, device: enrolled.device_id, value}
        localStorage.setItem('syc_vpn_device', enrolled.device_id)
    } catch (e) { if (generation === identityGeneration) meshMaintenanceError.value = `组网身份获取失败：${String(e)}` }
}

interface VpnPeerConfig {
    pubkey: string
    allowed_ips: string[]
    endpoint: string | null
}

/** 服务器 peer 用组网入口（局域网 51820 / 外网 ali:52000），其它 peer 用后端下发的实时端点 */
async function buildPeerConfigs(data: VpnPeersResult, round = 0): Promise<VpnPeerConfig[]> {
    const {invoke} = await import('@tauri-apps/api/core')
    const [status, localNetworks] = await Promise.all([
        invoke<{pubkey: string; endpoint: string; handshake_ms: number}[]>('vpn_peer_status').catch(() => []),
        invoke<string[]>('vpn_lan_endpoints', {cidrs: true}).catch(() => []),
    ])
    const peers = data.peers.map(p => {
        const isServer = p.role === 'server' || p.device_id === 'syc-server'
        const attempt = activeTunnel?.attempts.get(p.pubkey)
        const observed = status.find(s => s.pubkey === p.pubkey && s.handshake_ms > 0 && Date.now() - s.handshake_ms < 180_000
            && (!attempt || s.handshake_ms >= attempt.since))
        return {
            pubkey: p.pubkey,
            allowed_ips: p.allowed_ips,
            endpoint: p.endpoint || '',
            candidates: p.candidates || [],
            role: isServer ? 'server' : (p.role || 'member'),
            confirmed_endpoint: observed ? (isServer ? attempt?.endpoint || p.endpoint || observed.endpoint : observed.endpoint) : null,
        }
    })
    return invoke<VpnPeerConfig[]>('vpn_mesh_plan', {input: {peers, round, local_networks: localNetworks}})
}

// 隧道运行期状态：用于隧道启动后重新拉取对端实时端点（打洞）。
// 前端只在启动调用期间持有私钥；Windows 系统服务使用原生层写入的配置文件。
interface ActiveTunnelState {
    device: string
    base: string
    attempts: Map<string, {endpoint: string; since: number}>
    fingerprint: string
    round: number
    refreshing: boolean
    pubkey: string
}

let activeTunnel: ActiveTunnelState | null = null
let peerRefreshTimer: number | undefined
let peerRefreshOneShot: number | undefined
let resumingTunnel = false
let startingTunnel: Promise<string> | null = null

/** Reattach coordinator maintenance after UI reload/restart without rotating keys.
 * A service being UP does not prove its identity belongs to the logged-in device.
 */
export async function resumeEmbeddedTunnel(device: string): Promise<void> {
    if (resumingTunnel || activeTunnel?.device === device) return
    resumingTunnel = true
    try {
        const {invoke} = await import('@tauri-apps/api/core')
        const base = meshBaseUrl()
        const [local, data, status] = await Promise.all([
            invoke<{pubkey: string; listen_port: number; memory_backed: boolean}>('vpn_interface_status'),
            httpGetFrom<VpnPeersResult>(base, '/vpn/peers', {device_id: device}),
            invoke<{pubkey: string; endpoint: string}[]>('vpn_peer_status'),
        ])
        if (!local.memory_backed || !meshIdentityMatches(local, data.self.pubkey)) {
            // Normal authenticated identity API owns private-key handling; no
            // private material is read from the old native service/configuration.
            await startEmbeddedTunnel(device)
            return
        }
        activeTunnel = {device, base, pubkey: data.self.pubkey, round: 0, refreshing: false, fingerprint: '',
            attempts: new Map(status.map(p => [p.pubkey, {endpoint: p.endpoint, since: 0}]))}
        await sendVpnHeartbeat(device)
        startPeerRefresh()
        startVpnHeartbeat(device)
        meshMaintenanceError.value = ''
    } catch (e) {
        meshMaintenanceError.value = `组网维护未恢复：${String(e)}`
        throw e
    } finally { resumingTunnel = false }
}

/**
 * Windows：用内嵌/已安装的 WireGuard 启动隧道（Linux 请用 syc up）。
 * 每次启动用当前 JWT 向 /vpn/identity 获取身份，再交给原生层配置 Windows 系统服务。
 */
export function startEmbeddedTunnel(device: string): Promise<string> {
    if (startingTunnel) return startingTunnel
    const pending = doStartEmbeddedTunnel(device)
    startingTunnel = pending
    void pending.finally(() => { if (startingTunnel === pending) startingTunnel = null }).catch(() => {})
    return pending
}

async function doStartEmbeddedTunnel(device: string): Promise<string> {
    const token = localStorage.getItem('token')
    if (!token) throw new Error('请先登录')
    const generation = identityGeneration
    const stillAuthenticated = () => token === localStorage.getItem('token') && generation === identityGeneration
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
            // Read peers after authenticated identity retrieval, and reject a
            // concurrent registration change rather than install mismatched keys.
            const id = await httpGetFrom<VpnIdentity>(base, '/vpn/identity', {device_id: device})
            const peers = await httpGetFrom<VpnPeersResult>(base, '/vpn/peers', {device_id: device})
            if (!stillAuthenticated()) throw new Error('登录身份已变化，已取消组网连接')
            if (id.pubkey !== peers.self.pubkey) throw new Error('组网身份发生变化，请重新连接')
            identity = id
            memoryIdentity = {token: localStorage.getItem('token') || '', base, device, value: id}
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
    // UDP 端点与 LAN 候选由协调服务明确下发，不从 HTTP 主机猜测 WG 位置。
    const serverPeer = data.peers.find(p => p.role === 'server' || p.device_id === 'syc-server')
    const activeEp = serverPeer?.endpoint || ''
    if (activeEp) localStorage.setItem(KEY_ACTIVE_SERVER_EP, activeEp)
    const peers = await buildPeerConfigs(data)
    if (!stillAuthenticated()) throw new Error('登录身份已变化，已取消组网连接')
    const {invoke} = await import('@tauri-apps/api/core')
    const startedAt = Math.floor(Date.now() / 1000) * 1000
    const msg = await invoke<string>('vpn_up_embedded', {
        device,
        privateKey: identity.private_key,
        publicKey: identity.pubkey,
        address: data.self.address,
        subnet: data.self.subnet,
        keepalive: data.self.keepalive ?? 5,
        peers,
    })
    if (!stillAuthenticated()) throw new Error('登录身份已变化，已取消组网连接')
    activeTunnel = {
        device,
        pubkey: identity.pubkey,
        base: chosen,
        attempts: new Map(peers.map(p => [p.pubkey, {endpoint: p.endpoint || '', since: startedAt}])),
        fingerprint: JSON.stringify(peers), round: 0, refreshing: false,
    }
    startPeerRefresh()
    startVpnHeartbeat(device)
    meshMaintenanceError.value = ''
    return msg
}

export async function stopEmbeddedTunnel(): Promise<string> {
    clearMeshIdentity()
    stopPeerRefresh()
    stopVpnHeartbeat()
    // Logout/cancel may arrive while native startup is still consuming its pipe.
    // Wait for it, then stop, so an old login cannot leave a late tunnel running.
    await startingTunnel?.catch(() => {})
    const {invoke} = await import('@tauri-apps/api/core')
    try {
        return await invoke<string>('vpn_down_embedded', {iface: 'syc0'})
    } finally {
        clearMeshIdentity()
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
// 端点可能还是空的。启动 5s 后再拉一次，之后每 15s 对比，以 syncconf 更新对端而不重装隧道。

function startPeerRefresh(): void {
    stopPeerRefresh()
    const tick = async () => {
        const st = activeTunnel
        if (!st || st.refreshing) return
        st.refreshing = true
        try {
            const {invoke} = await import('@tauri-apps/api/core')
            const [data, local] = await Promise.all([
                httpGetFrom<VpnPeersResult>(st.base, '/vpn/peers', {device_id: st.device}),
                invoke<{pubkey: string; listen_port: number}>('vpn_interface_status'),
            ])
            if (activeTunnel !== st) return
            if (data.self.pubkey !== st.pubkey || !meshIdentityMatches(local, data.self.pubkey)) {
                clearMeshIdentity()
                await startEmbeddedTunnel(st.device)
                return
            }
            const peers = await buildPeerConfigs(data, ++st.round)
            const fingerprint = JSON.stringify(peers)
            if (st.fingerprint === fingerprint) return
            if (activeTunnel !== st) return
            const since = Math.floor(Date.now() / 1000) * 1000
            await invoke('vpn_update_peers', {peers})
            st.attempts = new Map(peers.map(p => [p.pubkey, st.attempts.get(p.pubkey)?.endpoint === p.endpoint
                ? st.attempts.get(p.pubkey)! : {endpoint: p.endpoint || '', since}]))
            st.fingerprint = fingerprint
            meshMaintenanceError.value = ''
        } catch (e) {
            if (activeTunnel !== st) return
            if (e instanceof ApiError && ([401, 403].includes(e.code) || [401, 403].includes(e.status)))
                await stopEmbeddedTunnel().catch(() => {})
            meshMaintenanceError.value = `对端更新失败：${String(e)}`
        }
        finally { st.refreshing = false }
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
    const lanEndpoints = (await import('@tauri-apps/api/core')).isTauri()
        ? await (await import('@tauri-apps/api/core')).invoke<string[]>('vpn_lan_endpoints').catch(() => []) : []
    const body = {device_id: device, endpoint: '', lan_endpoints: lanEndpoints}
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
