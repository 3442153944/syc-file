/**
 * platform.ts — 平台感知的配置读写
 *
 * Tauri 模式：server_url / token 存在 Rust SyncConfig（通过 invoke 读写）
 * Web   模式：server_url / token / device_id 存在 localStorage
 *
 * 外部代码统一调这里的函数，不直接碰 localStorage 或 invoke。
 */

import {isTauri} from '@tauri-apps/api/core'
import {baseUrl} from './net'

const KEY_SERVER = 'filesync_server_url'
const KEY_TOKEN = 'filesync_token'
const KEY_DEVICE = 'filesync_device_id'

// ── server URL ────────────────────────────────────────────────────────────────

export function getServerUrl(): string {
    // 地址由 net.ts 的「当前激活节点」决定（Tauri 下与 Rust 侧是同一个节点，
    // Web 下默认相对路径 /file 交给 nginx 反代）。节点切换后这里自动跟着变，
    // 所以每次用都要现取，别缓存到模块变量里。
    return baseUrl()
}

/**
 * 写入服务器地址。真正的节点切换请用 net.ts 的 switchNode/saveNode；这里只更新
 * 兜底值（net.ts 初始化完成前、以及 Rust 回传配置时用来对齐 localStorage）。
 */
export function setServerUrl(url: string): void {
    localStorage.setItem(KEY_SERVER, url.trimEnd().replace(/\/$/, ''))
}

// ── token ─────────────────────────────────────────────────────────────────────
// Tauri 模式下 token 由 Rust 持有，TS 侧只在登录时写一次（同步到 localStorage
// 方便 web fallback 路径复用；Rust login command 自己也会写到 SyncConfig）。

export function getToken(): string {
    return localStorage.getItem(KEY_TOKEN) || ''
}

export function setToken(token: string): void {
    if (token) {
        localStorage.setItem(KEY_TOKEN, token)
    } else {
        localStorage.removeItem(KEY_TOKEN)
    }
}

export function clearToken(): void {
    localStorage.removeItem(KEY_TOKEN)
}

// ── 头像 URL ───────────────────────────────────────────────────────────────────
// 后端存的是相对路径如 /static/avatar/xxx.png，Tauri 需拼服务器地址

export function avatarUrl(relativePath: string | undefined | null): string {
    if (!relativePath) return ''
    if (relativePath.startsWith('http://') || relativePath.startsWith('https://')) {
        return relativePath
    }
    const base = getServerUrl().replace(/\/+$/, '')
    return base + (relativePath.startsWith('/') ? '' : '/') + relativePath
}

// ── device ID ─────────────────────────────────────────────────────────────────
// Tauri 模式由 Rust 生成并持有；web 模式在 localStorage 生成一次后复用。

export function getDeviceId(): string {
    if (isTauri()) return ''   // web 模式才走这里；Tauri 调 invoke('get_device_id')
    let id = localStorage.getItem(KEY_DEVICE)
    if (!id) {
        id = crypto.randomUUID()
        localStorage.setItem(KEY_DEVICE, id)
    }
    return id
}
