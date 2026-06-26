export interface UserInfo {
  id: number
  username: string
  email?: string
  phone?: string
  role?: string
  avatar?: string
  created_at?: string
}

export interface LoginData {
  token: string
  user: UserInfo
}

export interface VerifyData {
  token?: string
  user?: UserInfo
}
