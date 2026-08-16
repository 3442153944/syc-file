// api/admin/adminTypes.ts
// 管理域 + 监控的响应类型。字段名与后端 json tag 逐一对齐。

/** 服务端统一分页形状 */
export interface Paged<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}

// ── 用户 ──────────────────────────────────────────────────
export interface AdminUser {
  id: number
  username: string
  email: string | null
  phone: string | null
  avatar: string | null
  role: string
  status: number
  last_login: string | null
  created_at: string
  /** 当前是否有设备在线（取自 WS Hub，不是库里的字段） */
  online: boolean
  device_count: number
}

// ── 设备 ──────────────────────────────────────────────────
export interface DeviceRow {
  id: number
  user_id: number
  device_name: string
  device_type: string
  device_id: string
  os_version: string
  app_version: string
  ip_address: string
  last_active: string | null
  status: number
  created_at: string
  updated_at: string
  /** 实时在线状态以 Hub 为准；库里的 status 只是上次断开时写下的 */
  online: boolean
  pending_tasks: number
}

// ── 操作日志 ──────────────────────────────────────────────
export interface OperationLogRow {
  id: number
  user_id: number | null
  operation_type: string | null
  operation_module: string | null
  operation_desc: string | null
  request_method: string | null
  request_url: string | null
  request_params: string | null
  response_result: string | null
  ip_address: string | null
  user_agent: string | null
  /** 1=成功 0=失败（按响应体里的业务 code 判定，不是 HTTP 状态码） */
  status: number | null
  error_message: string | null
  execution_time: number | null
  created_at: string
  username: string
}

// ── 存储配额 ──────────────────────────────────────────────
export interface StorageRow {
  id: number
  user_id: number
  total_quota: number
  used_quota: number
  file_count: number
  last_sync: string | null
  username?: string
  used_percent?: number
}

// ── 角色权限 ──────────────────────────────────────────────
export interface Permission {
  id: number
  permission_code: string
  permission_name: string
  parent_id: number | null
  permission_type: string | null
  description: string | null
  sort_order: number
  status: number
  created_at: string
}

export interface RoleWithPerms {
  id: number
  role_code: string
  role_name: string
  description: string | null
  status: number
  created_at: string
  permissions: Permission[]
  user_count: number
}

// ── 监控 ──────────────────────────────────────────────────
export interface SystemMetrics {
  cpu: {
    used_percent: number
    cores: number
    model_name: string
    per_core: number[] | null
    load1: number
    load5: number
    load15: number
  }
  memory: {
    total: number
    used: number
    free: number
    used_percent: number
    swap_total: number
    swap_used: number
  }
  host: {
    hostname: string
    os: string
    platform: string
    platform_version: string
    kernel_arch: string
    uptime_seconds: number
    boot_time: number
    procs: number
    go_version: string
    server_time: number
  }
  disks: Array<{
    path: string
    fstype: string
    total: number
    used: number
    free: number
    used_percent: number
  }>
}

export interface NetworkMetrics {
  bytes_sent: number
  bytes_recv: number
  /** 字节/秒，由两次调用之间的差值算出，首次为 0 */
  send_rate: number
  recv_rate: number
  interfaces: Array<{
    name: string
    bytes_sent: number
    bytes_recv: number
    packets_sent: number
    packets_recv: number
    errin: number
    errout: number
    dropin: number
    dropout: number
  }>
  online_devices: number
  online_users: number
  active_connections: number
  server_time: number
}
