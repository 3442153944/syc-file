import { invoke, isTauri } from '@tauri-apps/api/core'
import { httpPost } from '../http'
import { setToken } from '../platform'
import type { LoginData, VerifyData } from './userTypes'

export async function login(username: string, password: string): Promise<LoginData> {
  if (isTauri()) {
    // Rust 负责 HTTP + 把 token 写入 SyncConfig
    const data = await invoke<LoginData>('login', { username, password })
    // 同步写一份到 localStorage，供 web 模式的 http.ts 复用
    setToken(data.token)
    return data
  }
  const data = await httpPost<LoginData>('/user/login', { username, password })
  setToken(data.token)
  return data
}

export async function verify(): Promise<VerifyData> {
  if (isTauri()) {
    return invoke<VerifyData>('verify')
  }
  return httpPost<VerifyData>('/user/verify')
}

export async function register(username: string, password: string, email?: string): Promise<unknown> {
  if (isTauri()) {
    return invoke('register', { username, password, email })
  }
  return httpPost('/user/register', { username, password, email })
}

export async function resetPassword(username: string, oldPassword: string, newPassword: string): Promise<unknown> {
  if (isTauri()) {
    return invoke('reset_password', { username, oldPassword, newPassword })
  }
  return httpPost('/user/reset-password', { username, old_password: oldPassword, new_password: newPassword })
}
