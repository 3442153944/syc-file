package com.sunyuanling.filesync.dataClass

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class StatsResponse(
    val code: Int,
    val message: String,
    val data: StatsData
)

@Serializable
data class StatsData(
    val stats: Stats
)

@Serializable
data class Stats(
    @SerialName("online_users") val onlineUsers: Int,
    @SerialName("active_connections") val activeConnections: Int
)