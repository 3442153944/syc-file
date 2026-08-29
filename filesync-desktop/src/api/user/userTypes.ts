export interface UserInfo {
  id: number
  username: string
  email?: string
  phone?: string
  role?: string
  avatar?: string
  created_at?: string
  /** 粘贴快传全局唤起快捷键（Tauri accelerator 语法），未设置时为 null */
  quick_share_hotkey?: string | null
  /** 粘贴快传后分享链接的默认有效期（分钟），未设置时为 null（用服务端全局默认值） */
  quick_share_expire_minutes?: number | null
}

export interface LoginData {
  token: string
  user: UserInfo
}

export interface VerifyData {
  token?: string
  user?: UserInfo
}
