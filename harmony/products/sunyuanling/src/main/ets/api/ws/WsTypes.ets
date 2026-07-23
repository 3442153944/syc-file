/**
 * WsTypes
 * WebSocket 域 DTO，字段对齐后端 ws/types.go + ws/handler.go 返回结构。
 * 后端 ConnectionInfo 内嵌 DeviceInfo 对象（非扁平）。
 */

export interface DeviceInfo {
  device_id: string;
  device_type: string;
  device_name: string;
  status: string;
  platform: string;
  app_version: string;
  push_token: string;
}

export interface ConnectionInfo {
  conn_id: string;
  user_id: number;
  device: DeviceInfo | null;
  ip: string;
  connected_at: string;
  last_heartbeat: string;
  status: string;
}

export interface UserConnectionsData {
  user_id: number;
  connections: ConnectionInfo[];
  total_count: number;
}

export interface OnlineUserInfo {
  id: number;
  username: string;
  avatar: string;
  role: string;
  connections: ConnectionInfo[];
  conn_count: number;
}

export interface OnlineUsersData {
  total: number;
  users: OnlineUserInfo[];
}

export interface WsStats {
  total_connections: number;
  total_users: number;
  total_groups: number;
}
