package com.sunyuanling.filesync.network

import kotlinx.serialization.Serializable

/**
 * 统一响应结构
 * 注意：code 与 HTTP 状态码一致，200 表示成功
 */
@Serializable
data class Response<T>(
    /** 状态码（与 HTTP 状态码一致，200 表示成功） */
    val code: Int,

    /** 响应消息 */
    val message: String,

    /** 响应数据（可选） */
    val data: T? = null
)

/**
 * 无数据响应（仅返回状态）
 */
typealias EmptyResponse = Response<Unit>

/**
 * 分页响应
 */
@Serializable
data class PageData<T>(
    /** 当前页数据 */
    val list: List<T>,

    /** 总记录数 */
    val total: Long,

    /** 当前页码 */
    val page: Int,

    /** 每页大小 */
    val pageSize: Int,

    /** 总页数 */
    val totalPages: Int
)

/**
 * 响应扩展函数
 */

/** 检查响应是否成功（HTTP 200） */
fun <T> Response<T>.isSuccess(): Boolean = code == 200

/** 检查是否为客户端错误（4xx） */
fun <T> Response<T>.isClientError(): Boolean = code in 400..499

/** 检查是否为服务器错误（5xx） */
fun <T> Response<T>.isServerError(): Boolean = code in 500..599

/** 检查是否未授权 */
fun <T> Response<T>.isUnauthorized(): Boolean = code == 401

/** 检查是否禁止访问 */
fun <T> Response<T>.isForbidden(): Boolean = code == 403

/** 检查是否未找到 */
fun <T> Response<T>.isNotFound(): Boolean = code == 404

/**
 * 获取数据或抛出异常
 */
fun <T> Response<T>.getDataOrThrow(): T {
    return if (isSuccess() && data != null) {
        data
    } else {
        throw ResponseException(code, message)
    }
}

/**
 * 获取数据或返回 null
 */
fun <T> Response<T>.getDataOrNull(): T? {
    return if (isSuccess()) data else null
}

/**
 * 获取数据或返回默认值
 */
fun <T> Response<T>.getDataOrDefault(default: T): T {
    return if (isSuccess() && data != null) data else default
}

/**
 * 响应异常类
 */
class ResponseException(
    val code: Int,
    override val message: String
) : Exception("[$code] $message") {

    val isUnauthorized: Boolean get() = code == 401
    val isForbidden: Boolean get() = code == 403
    val isNotFound: Boolean get() = code == 404
    val isBadRequest: Boolean get() = code == 400
    val isServerError: Boolean get() = code in 500..599
}