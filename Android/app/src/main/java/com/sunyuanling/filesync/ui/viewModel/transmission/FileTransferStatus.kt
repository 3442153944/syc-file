package com.sunyuanling.filesync.ui.viewModel.transmission

/**
 * 文件传输状态
 */
enum class FileTransferStatus(val displayName: String) {
    /**
     * 等待中 - 文件已加入队列，等待开始传输
     */
    WAITING("等待中"),

    /**
     * 传输中 - 文件正在传输
     */
    TRANSFERRING("传输中"),

    /**
     * 已暂停 - 传输已暂停，可以恢复
     */
    PAUSED("已暂停"),

    /**
     * 已完成 - 文件传输成功完成
     */
    COMPLETED("已完成"),

    /**
     * 失败 - 传输失败（可重试）
     */
    FAILED("失败"),

    /**
     * 已取消 - 用户主动取消传输
     */
    CANCELLED("已取消");

    /**
     * 是否为活跃状态（正在传输或等待中）
     */
    val isActive: Boolean
        get() = this == WAITING || this == TRANSFERRING

    /**
     * 是否为终止状态（完成、失败或取消）
     */
    val isTerminated: Boolean
        get() = this == COMPLETED || this == FAILED || this == CANCELLED

    /**
     * 是否可以暂停
     */
    val canPause: Boolean
        get() = this == TRANSFERRING

    /**
     * 是否可以恢复
     */
    val canResume: Boolean
        get() = this == PAUSED || this == FAILED

    /**
     * 是否可以取消
     */
    val canCancel: Boolean
        get() = this == WAITING || this == TRANSFERRING || this == PAUSED

    /**
     * 是否可以重试
     */
    val canRetry: Boolean
        get() = this == FAILED

    /**
     * 是否可以删除记录
     */
    val canDelete: Boolean
        get() = isTerminated

    /**
     * 是否成功完成
     */
    val isSuccess: Boolean
        get() = this == COMPLETED

    /**
     * 是否需要显示进度条
     */
    val showProgress: Boolean
        get() = this == TRANSFERRING || this == PAUSED

    companion object {
        /**
         * 从字符串获取状态
         */
        fun fromString(value: String): FileTransferStatus {
            return entries.find { it.name.equals(value, ignoreCase = true) } ?: WAITING
        }

        /**
         * 获取所有可过滤的状态（用于UI筛选）
         */
        fun getFilterableStatuses(): List<FileTransferStatus> {
            return listOf(WAITING, TRANSFERRING, PAUSED, COMPLETED, FAILED, CANCELLED)
        }

        /**
         * 获取所有活跃状态
         */
        fun getActiveStatuses(): List<FileTransferStatus> {
            return entries.filter { it.isActive }
        }

        /**
         * 获取所有终止状态
         */
        fun getTerminatedStatuses(): List<FileTransferStatus> {
            return entries.filter { it.isTerminated }
        }
    }
}

/**
 * 传输类型
 */
enum class TransferType(val displayName: String) {
    /**
     * 上传
     */
    UPLOAD("上传"),

    /**
     * 下载
     */
    DOWNLOAD("下载");

    companion object {
        fun fromString(value: String): TransferType {
            return entries.find { it.name.equals(value, ignoreCase = true) } ?: UPLOAD
        }
    }
}

/**
 * 传输优先级
 */
enum class TransferPriority(val displayName: String, val level: Int) {
    /**
     * 低优先级
     */
    LOW("低", 1),

    /**
     * 普通优先级
     */
    NORMAL("普通", 2),

    /**
     * 高优先级
     */
    HIGH("高", 3),

    /**
     * 紧急
     */
    URGENT("紧急", 4);

    companion object {
        fun fromLevel(level: Int): TransferPriority {
            return entries.find { it.level == level } ?: NORMAL
        }
    }
}

/**
 * 传输错误类型
 */
enum class TransferError(val displayName: String) {
    /**
     * 网络错误
     */
    NETWORK_ERROR("网络错误"),

    /**
     * 文件不存在
     */
    FILE_NOT_FOUND("文件不存在"),

    /**
     * 权限不足
     */
    PERMISSION_DENIED("权限不足"),

    /**
     * 磁盘空间不足
     */
    DISK_FULL("磁盘空间不足"),

    /**
     * 文件已存在
     */
    FILE_EXISTS("文件已存在"),

    /**
     * 服务器错误
     */
    SERVER_ERROR("服务器错误"),

    /**
     * 认证失败
     */
    AUTH_FAILED("认证失败"),

    /**
     * 超时
     */
    TIMEOUT("超时"),

    /**
     * 用户取消
     */
    USER_CANCELLED("用户取消"),

    /**
     * 未知错误
     */
    UNKNOWN("未知错误");

    companion object {
        fun fromString(value: String): TransferError {
            return entries.find { it.name.equals(value, ignoreCase = true) } ?: UNKNOWN
        }
    }
}