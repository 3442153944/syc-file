// api/ws/WsResponse.kt
// 职责：WebSocket 管理模块所有响应数据类（仅 data 字段内容）。
// code / message 外层由 network.Response<T> 统一承载。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag 及 gin.H 返回。
package com.sunyuanling.filesync.api.ws

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// ==================== 在线用户列表 ====================
// 对应后端 handler.go GetOnlineUsers

@Serializable
data class OnlineUsersData(
    val total: Int = 0,
    val users: List<OnlineUserInfo> = emptyList()
)

@Serializable
data class OnlineUserInfo(
    val id: Int = 0,
    val username: String = "",
    val avatar: String? = null,
    val role: String? = null,
    /** 用户的连接列表（ConnectionInfo[]） */
    val connections: List<ConnectionInfo>? = null,
    @SerialName("conn_count") val connCount: Int = 0
)

// ==================== 用户连接详情 ====================
// 对应后端 types.go UserConnectionsInfo / ConnectionInfo / DeviceInfo

@Serializable
data class UserConnectionsData(
    @SerialName("user_id") val userId: Int = 0,
    val connections: List<ConnectionInfo> = emptyList(),
    @SerialName("total_count") val totalCount: Int = 0
)

@Serializable
data class ConnectionInfo(
    @SerialName("conn_id") val connId: String = "",
    @SerialName("user_id") val userId: Int = 0,
    val device: DeviceInfo? = null,
    val ip: String = "",
    /** 后端 time.Time → RFC3339 字符串 */
    @SerialName("connected_at") val connectedAt: String = "",
    /** 后端 time.Time → RFC3339 字符串 */
    @SerialName("last_heartbeat") val lastHeartbeat: String = "",
    val status: String = ""
)

@Serializable
data class DeviceInfo(
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("device_type") val deviceType: String = "",
    @SerialName("device_name") val deviceName: String = "",
    val status: String = "",
    val platform: String = "",
    @SerialName("app_version") val appVersion: String = "",
    @SerialName("push_token") val pushToken: String = ""
    // TODO: extra 字段为 map[string]interface{}，类型不确定，暂不定义
)

// ==================== WebSocket 统计 ====================
// 对应后端 handler.go GetStats

@Serializable
data class StatsData(
    val stats: StatsInfo = StatsInfo()
)

@Serializable
data class StatsInfo(
    @SerialName("online_users") val onlineUsers: Int = 0,
    @SerialName("active_connections") val activeConnections: Int = 0
)

// ==================== 广播响应 ====================
// 对应后端 handler.go BroadcastMessage 成功返回

@Serializable
data class BroadcastData(
    val online: Int = 0
)

// ==================== 分组用户列表 ====================
// 对应后端 handler.go GetGroupUsers

@Serializable
data class GroupUsersData(
    @SerialName("group_name") val groupName: String = "",
    /** 用户 ID 列表（[]uint → List<Int>） */
    val users: List<Int> = emptyList(),
    val count: Int = 0
)

// ==================== 无数据响应 ====================
// send / disconnectConn / disconnectUser / disconnectDevice / createGroup / sendToGroup
// 这些接口的 data 字段为 null，由 network.Response<T?> 承载，不需要单独的数据类。
