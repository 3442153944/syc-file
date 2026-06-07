// api/ws/WsParams.kt
// 职责：WebSocket 管理模块所有请求参数数据类。
// 驼峰字段名，@SerialName 对应后端 snake_case 字段，来源于后端 Go struct json tag。
package com.sunyuanling.filesync.api.ws

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// ==================== 发送消息 ====================

@Serializable
data class WsSendParams(
    /** 目标类型：user / conn / device / group */
    @SerialName("target_type")
    val targetType: String,
    /** 目标用户 ID 列表（target_type=user 时使用） */
    @SerialName("user_ids")
    val userIds: List<Int>? = null,
    /** 目标连接 ID 列表（target_type=conn 时使用） */
    @SerialName("conn_ids")
    val connIds: List<String>? = null,
    /** 目标设备 ID 列表（target_type=device 时使用） */
    @SerialName("device_ids")
    val deviceIds: List<String>? = null,
    /** 目标分组名列表（target_type=group 时使用） */
    val groups: List<String>? = null,
    /** 消息类型（必填），如 text / notification / file_sync */
    val type: String,
    /** 消息内容（必填），JSON 原文 */
    val content: String
)

// ==================== 广播消息 ====================

@Serializable
data class WsBroadcastParams(
    /** 消息类型（必填） */
    val type: String,
    /** 消息内容（必填），JSON 原文 */
    val content: String
)

// ==================== 创建分组 ====================

@Serializable
data class WsCreateGroupParams(
    /** 分组名称（必填） */
    @SerialName("group_name")
    val groupName: String,
    /** 用户 ID 列表（必填） */
    @SerialName("user_ids")
    val userIds: List<Int>
)

// ==================== 分组发送消息 ====================

@Serializable
data class WsSendToGroupParams(
    /** 分组名称（必填） */
    @SerialName("group_name")
    val groupName: String,
    /** 消息类型（必填） */
    val type: String,
    /** 消息内容（必填），JSON 原文 */
    val content: String
)

// ==================== 无请求体的接口 ====================
// GET /ws/stats           —— 无参数
// GET /ws/online          —— 无参数
// GET /ws/user/:id/connections   —— 路径参数
// DELETE /ws/conn/:conn_id       —— 路径参数
// DELETE /ws/user/:id            —— 路径参数
// DELETE /ws/device/:device_id   —— 路径参数
// GET /ws/group/:name/users      —— 路径参数
