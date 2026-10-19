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
import {netState} from './net'
import {getDeviceId} from './platform'

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

function authHeaders(): Record<string, string> {
    return {
        Token: localStorage.getItem('token') || '',
        'Device-Id': getDeviceId(),
    }
}

/** 确保本机有组网设备；返回设备名，失败抛错。 */
export async function ensureVpnDevice(): Promise<string> {
    const cached = localStorage.getItem('syc_vpn_device')
    if (cached) return cached

    const res = await fetch(`${netState.serverUrl}/v1/vpn/auto-enroll`, {
        method: 'POST',
        headers: authHeaders(),
    })
    const body = await res.json()
    if (body?.code !== 200) {
        throw new Error(body?.message || '自动入组失败（可能还没加入任何组）')
    }
    const device = body.data?.device_id as string
    if (!device) throw new Error('自动入组响应缺少 device_id')
    localStorage.setItem('syc_vpn_device', device)
    return device
}

/** 读当前设备的直连/中继状态。 */
export async function fetchVpnLink(device: string): Promise<VpnLinkResult> {
    const res = await fetch(
        `${netState.serverUrl}/v1/vpn/link?device_id=${encodeURIComponent(device)}`,
        {headers: authHeaders()},
    )
    const body = await res.json()
    if (body?.code !== 200) throw new Error(body?.message || '读取链路状态失败')
    return body.data as VpnLinkResult
}
