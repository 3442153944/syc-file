// api/ws/WsApi.kt
// 职责：WebSocket 管理 API 调用封装。
// 每个函数只做：组装参数 + 调用 Request.xSuspend，不包含业务逻辑。
// 注意：/ws/connect 是 WebSocket 升级请求，不走 Request 单例（由 WebSocketManager 处理）。
package com.sunyuanling.filesync.api.ws

import com.sunyuanling.filesync.api.ApiRoutes
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.network.Response as ApiResponse

object WsApi {

    // ==================== WebSocket 连接 ====================

    /**
     * 建立 WebSocket 连接。
     *
     * TODO: /ws/connect 是 WebSocket 升级请求（HTTP Upgrade），不走 Request 单例的 JSON 请求通道。
     * 实际的 WebSocket 连接由 WebSocketManager（network/websocket.kt）处理。
     * 此处仅保留路由常量引用，不实现 HTTP 调用。
     */

    // ==================== 在线用户 ====================

    /**
     * 获取在线用户列表。
     */
    suspend fun getOnlineUsers(): Result<ApiResponse<OnlineUsersData>> {
        return Request.getSuspend<ApiResponse<OnlineUsersData>>(ApiRoutes.WS_ONLINE)
    }

    /**
     * 获取指定用户的连接详情。
     * @param userId 用户 ID（对应路由 :id 参数）
     */
    suspend fun getUserConnections(userId: String): Result<ApiResponse<UserConnectionsData>> {
        val endpoint = ApiRoutes.WS_USER_CONNECTIONS.format(userId)
        return Request.getSuspend<ApiResponse<UserConnectionsData>>(endpoint)
    }

    // ==================== 统计 ====================

    /**
     * 获取 WebSocket 统计信息（在线用户数、活跃连接数）。
     */
    suspend fun getStats(): Result<ApiResponse<StatsData>> {
        return Request.getSuspend<ApiResponse<StatsData>>(ApiRoutes.WS_STATS)
    }

    // ==================== 发送消息 ====================

    /**
     * 向指定目标发送 WebSocket 消息。
     */
    suspend fun sendMessage(params: WsSendParams): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, WsSendParams>(
            ApiRoutes.WS_SEND, params
        )
    }

    /**
     * 广播 WebSocket 消息给所有在线用户。
     */
    suspend fun broadcastMessage(params: WsBroadcastParams): Result<ApiResponse<BroadcastData>> {
        return Request.postSuspend<ApiResponse<BroadcastData>, WsBroadcastParams>(
            ApiRoutes.WS_BROADCAST, params
        )
    }

    // ==================== 断开连接 ====================

    /**
     * 断开指定连接。
     * @param connId 连接 ID（对应路由 :conn_id 参数）
     */
    suspend fun disconnectConn(connId: String): Result<ApiResponse<Unit?>> {
        val endpoint = ApiRoutes.WS_DISCONNECT_CONN.format(connId)
        return Request.requestSuspend<ApiResponse<Unit?>, Unit>(
            "DELETE", endpoint, null, null
        )
    }

    /**
     * 断开指定用户的所有连接。
     * @param userId 用户 ID（对应路由 :id 参数）
     */
    suspend fun disconnectUser(userId: String): Result<ApiResponse<Unit?>> {
        val endpoint = ApiRoutes.WS_DISCONNECT_USER.format(userId)
        return Request.requestSuspend<ApiResponse<Unit?>, Unit>(
            "DELETE", endpoint, null, null
        )
    }

    /**
     * 断开指定设备的连接。
     * @param deviceId 设备 ID（对应路由 :device_id 参数）
     */
    suspend fun disconnectDevice(deviceId: String): Result<ApiResponse<Unit?>> {
        val endpoint = ApiRoutes.WS_DISCONNECT_DEVICE.format(deviceId)
        return Request.requestSuspend<ApiResponse<Unit?>, Unit>(
            "DELETE", endpoint, null, null
        )
    }

    // ==================== 分组管理 ====================

    /**
     * 创建 WebSocket 分组。
     */
    suspend fun createGroup(params: WsCreateGroupParams): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, WsCreateGroupParams>(
            ApiRoutes.WS_GROUP, params
        )
    }

    /**
     * 向分组发送消息。
     */
    suspend fun sendToGroup(params: WsSendToGroupParams): Result<ApiResponse<Unit?>> {
        return Request.postSuspend<ApiResponse<Unit?>, WsSendToGroupParams>(
            ApiRoutes.WS_GROUP_SEND, params
        )
    }

    /**
     * 获取分组内用户列表。
     * @param groupName 分组名（对应路由 :name 参数）
     */
    suspend fun getGroupUsers(groupName: String): Result<ApiResponse<GroupUsersData>> {
        val endpoint = ApiRoutes.WS_GROUP_USERS.format(groupName)
        return Request.getSuspend<ApiResponse<GroupUsersData>>(endpoint)
    }
}
