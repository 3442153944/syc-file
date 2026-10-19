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
import {httpDelete, httpGet, httpPost} from './http'

export interface VpnLinkPeer {
    device_id: string
    endpoint: string
    mode: 'direct' | 'relay'
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
    if (cached) return cached

    const data = await httpPost<{ device_id: string }>('/vpn/auto-enroll')
    const device = data?.device_id
    if (!device) throw new Error('自动入组响应缺少 device_id')
    localStorage.setItem('syc_vpn_device', device)
    return device
}

/** 读当前设备的直连/中继状态。 */
export async function fetchVpnLink(device: string): Promise<VpnLinkResult> {
    return httpGet<VpnLinkResult>('/vpn/link', {device_id: device})
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

export interface VpnPeersResult {
    self: { address: string; subnet: string; keepalive: number; mtu: number }
    peers: { pubkey: string; allowed_ips: string[]; endpoint?: string }[]
}

interface VpnIdentity {
    device_id: string
    address: string
    subnet: string
    pubkey: string
    private_key: string
}

/**
 * Windows：用内嵌/已安装的 WireGuard 启动隧道（Linux 请用 syc up）。
 * 私钥不落盘：每次用当前 JWT 向 /vpn/identity 换取，身份跟着会话走。
 */
export async function startEmbeddedTunnel(device: string): Promise<string> {
    const [identity, data] = await Promise.all([
        httpGet<VpnIdentity>('/vpn/identity', {device_id: device}),
        httpGet<VpnPeersResult>('/vpn/peers', {device_id: device}),
    ])
    const {invoke} = await import('@tauri-apps/api/core')
    return invoke<string>('vpn_up_embedded', {
        device,
        private_key: identity.private_key,
        address: data.self.address,
        subnet: data.self.subnet,
        keepalive: data.self.keepalive ?? 5,
        peers: data.peers.map(p => ({
            pubkey: p.pubkey,
            allowed_ips: p.allowed_ips,
            endpoint: p.endpoint || null,
        })),
    })
}

export async function stopEmbeddedTunnel(): Promise<string> {
    const {invoke} = await import('@tauri-apps/api/core')
    return invoke<string>('vpn_down_embedded', {iface: 'syc0'})
}
