export interface MeshPeerStatus {
    pubkey: string
    endpoint: string
    handshake_ms: number
    allowed_ips: string[]
}

/** 对端外层实际出口：物理网卡才算直连，tunnel 表示被系统里其它 VPN/虚拟网卡承载。 */
export type MeshUnderlay = 'physical' | 'tunnel' | 'own' | 'unknown'
export type MeshPeerMode = 'direct' | 'vpn' | 'relay' | 'unknown'

export function freshMeshHandshake(peer: MeshPeerStatus | undefined, now = Date.now()): boolean {
    return !!peer && peer.handshake_ms > 0 && now >= peer.handshake_ms && now - peer.handshake_ms < 180_000
}

/**
 * 只有「新鲜握手 + 真实持有目标 /32 + 外层出口是物理网卡」才算直连。
 * 外层由系统里其它 VPN/隧道承载时（例如打洞走了阿里云 WireGuard 中继），
 * 本质与中继相同，标成 vpn，不能假装直连。
 */
export function meshPeerMode(address: string | undefined, direct: MeshPeerStatus | undefined,
    relay: MeshPeerStatus | undefined, now = Date.now(), underlay?: MeshUnderlay): MeshPeerMode {
    if (!address) return 'unknown'
    const prefix = `${address}/32`
    if (freshMeshHandshake(direct, now) && direct!.allowed_ips.includes(prefix)) {
        // 未做 underlay 检测的平台（如 Web/Linux 查询不到）保持旧行为；
        // 检测明确是隧道就降级成 vpn，检测失败则诚实显示 unknown。
        if (underlay === 'tunnel' || underlay === 'own') return 'vpn'
        if (underlay === 'unknown') return 'unknown'
        return 'direct'
    }
    if (freshMeshHandshake(relay, now) && relay!.allowed_ips.includes(prefix)) return 'relay'
    return 'unknown'
}

export function meshIdentityMatches(local: {pubkey: string; listen_port: number}, expectedPubkey: string): boolean {
    return !!expectedPubkey && local.pubkey === expectedPubkey && local.listen_port === 51821
}
