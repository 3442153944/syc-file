/**
 * platform.ts — 平台感知的配置读写
 *
 * Tauri 模式：server_url / token 存在 Rust SyncConfig（通过 invoke 读写）
 * Web   模式：server_url / token / device_id 存在 localStorage
 *
 * 外部代码统一调这里的函数，不直接碰 localStorage 或 invoke。
 */

import { isTauri } from '@tauri-apps/api/core'

const KEY_SERVER = 'filesync_server_url'
const KEY_TOKEN  = 'filesync_token'
const KEY_DEVICE = 'filesync_device_id'

// ── server URL ────────────────────────────────────────────────────────────────

export function getServerUrl(): string {
  return localStorage.getItem(KEY_SERVER) || 'http://localhost:8991'
}

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
