import {test} from 'node:test'
import assert from 'node:assert/strict'
import {meshPeerMode, meshIdentityMatches, type MeshPeerStatus} from '../src/utils/meshLinkState.ts'

const now = 1_000_000
const status = (ips: string[], handshake = now - 1_000): MeshPeerStatus => ({
    pubkey: 'public', endpoint: '192.168.31.182:51821', handshake_ms: handshake, allowed_ips: ips,
})
test('fresh probing handshake without /32 keeps data traffic on relay', () => {
    assert.equal(meshPeerMode('172.1.0.4', status([]), status(['172.1.0.4/32']), now), 'relay')
})
test('confirmed peer owning /32 is direct', () => {
    assert.equal(meshPeerMode('172.1.0.4', status(['172.1.0.4/32']), status([]), now), 'direct')
})
test('server handshake alone does not prove a relay route exists', () => {
    assert.equal(meshPeerMode('172.1.0.4', undefined, status(['172.1.0.254/32']), now), 'unknown')
})
test('expired and future handshakes do not show direct', () => {
    for (const handshake of [0, now - 180_000, now + 1])
        assert.equal(meshPeerMode('172.1.0.4', status(['172.1.0.4/32'], handshake), undefined, now), 'unknown')
})
test('UP service must match registered public identity and advertised UDP port', () => {
    assert.equal(meshIdentityMatches({pubkey: 'registered', listen_port: 51821}, 'registered'), true)
    assert.equal(meshIdentityMatches({pubkey: 'old-service', listen_port: 51821}, 'registered'), false)
    assert.equal(meshIdentityMatches({pubkey: 'registered', listen_port: 59208}, 'registered'), false)
    assert.equal(meshIdentityMatches({pubkey: '', listen_port: 51821}, ''), false)
})
