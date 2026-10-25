export interface MeshPeerStatus {
    pubkey: string
    endpoint: string
    handshake_ms: number
    allowed_ips: string[]
}

export function freshMeshHandshake(peer: MeshPeerStatus | undefined, now = Date.now()): boolean {
    return !!peer && peer.handshake_ms > 0 && now >= peer.handshake_ms && now - peer.handshake_ms < 180_000
}

/** A probing peer may handshake without owning the destination's data route. */
export function meshPeerMode(address: string | undefined, direct: MeshPeerStatus | undefined,
    relay: MeshPeerStatus | undefined, now = Date.now()): 'direct' | 'relay' | 'unknown' {
    if (!address) return 'unknown'
    const prefix = `${address}/32`
    if (freshMeshHandshake(direct, now) && direct!.allowed_ips.includes(prefix)) return 'direct'
    if (freshMeshHandshake(relay, now) && relay!.allowed_ips.includes(prefix)) return 'relay'
    return 'unknown'
}

export function meshIdentityMatches(local: {pubkey: string; listen_port: number}, expectedPubkey: string): boolean {
    return !!expectedPubkey && local.pubkey === expectedPubkey && local.listen_port === 51821
}
