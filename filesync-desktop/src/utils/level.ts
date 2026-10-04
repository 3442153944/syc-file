// utils/level.ts — 当前登录者的权限级别
import type { Level } from '@/api/system/systemTypes'

export const LEVEL_TEXT: Record<number, string> = { 0: '访客', 1: '用户', 2: '管理员', 3: '超级管理员' }

export function levelText(level: number | undefined | null): string {
  return LEVEL_TEXT[level ?? 1] ?? '用户'
}

/**
 * 读 localStorage 里登录时存的 userInfo。
 * 兼容升级前缓存的旧 userInfo（没有 level 字段）：role=admin 视为超级管理员（服务端升级时旧管理员都升为超管）。
 */
export function currentLevel(): Level {
  try {
    const raw = localStorage.getItem('userInfo')
    if (!raw) return 1
    const u = JSON.parse(raw) as { level?: number; role?: string }
    if (typeof u.level === 'number') return u.level as Level
    return u.role === 'admin' ? 3 : 1
  } catch {
    return 1
  }
}

export function currentUserId(): number | null {
  try {
    const raw = localStorage.getItem('userInfo')
    return raw ? (JSON.parse(raw) as { id?: number }).id ?? null : null
  } catch {
    return null
  }
}
