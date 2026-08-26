-- ============================================
-- 13. 分享链接表（分享链接功能）
-- 由用户手动执行本脚本创建表结构
-- ============================================
CREATE TABLE IF NOT EXISTS `share_link` (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    user_id       BIGINT UNSIGNED NOT NULL COMMENT '对应用户表 id（GORM uint → bigint unsigned）',
    share_code    VARCHAR(36)   NOT NULL COMMENT '分享码（UUID）',
    original_path VARCHAR(1000) NOT NULL COMMENT '源文件绝对路径',
    file_name     VARCHAR(255)  NOT NULL COMMENT '原始文件名',
    temp_name     VARCHAR(255)  NOT NULL COMMENT 'temp 目录中的文件名（UUID+扩展名）',
    file_size     BIGINT        NOT NULL DEFAULT 0,
    expire_time   DATETIME      NOT NULL COMMENT '链接过期时间',
    status        TINYINT       NOT NULL DEFAULT 1 COMMENT '状态：1有效/0已过期/2已吊销',
    expired_at    DATETIME      NULL COMMENT '实际销毁时间',
    created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_share_link_code (share_code),
    INDEX idx_share_link_user (user_id),
    INDEX idx_share_link_expire (expire_time),
    INDEX idx_share_link_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='分享链接记录表';
