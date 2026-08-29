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
    is_quick_share TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '是否为粘贴快传生成的分享',
    storage_kind  VARCHAR(10)   NOT NULL DEFAULT 'disk' COMMENT '内容存储位置：disk/memory',
    UNIQUE INDEX uk_share_link_code (share_code),
    INDEX idx_share_link_user (user_id),
    INDEX idx_share_link_expire (expire_time),
    INDEX idx_share_link_status (status),
    INDEX idx_share_link_quota (user_id, status, is_quick_share)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='分享链接记录表';

-- ============================================
-- 粘贴快传功能新增字段：如果 share_link 表已经存在（早于本次改动建的表），
-- 需要手动执行下面这段 ALTER，且必须在部署新版 Go 二进制之前执行——
-- share_link 表不走 GORM AutoMigrate，新代码一上线就会按新增字段读写这张表，
-- 列不存在的话，不止粘贴快传，连已经在用的普通分享功能也会一起报错。
-- 已经跑过一次 CREATE TABLE 建出新结构的，跳过这一段即可。
-- ============================================
ALTER TABLE share_link
  ADD COLUMN is_quick_share TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否为粘贴快传生成的分享',
  ADD COLUMN storage_kind VARCHAR(10) NOT NULL DEFAULT 'disk' COMMENT '内容存储位置：disk/memory',
  ADD INDEX idx_share_link_quota (user_id, status, is_quick_share);
