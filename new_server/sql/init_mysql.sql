-- ============================================
-- FileSync MySQL 初始化脚本
-- 从 PostgreSQL 迁移而来
-- 兼容 MySQL 5.7+ / 8.0+
-- ============================================

-- ============================================
-- 1. 用户表
-- ============================================
CREATE TABLE IF NOT EXISTS `user` (
    id         INT AUTO_INCREMENT PRIMARY KEY,
    username   VARCHAR(50)  NOT NULL,
    password   VARCHAR(255) NOT NULL,
    email      VARCHAR(100) NULL,
    phone      VARCHAR(20)  NULL,
    avatar     VARCHAR(255) NULL,
    role       VARCHAR(20)  NOT NULL DEFAULT 'user',
    status     TINYINT      NOT NULL DEFAULT 1 COMMENT '状态：1正常/0禁用',
    last_login DATETIME     NULL,
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_username (username),
    UNIQUE INDEX uk_email (email),
    INDEX idx_user_status (status),
    INDEX idx_user_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户表';

-- ============================================
-- 2. 设备管理表
-- ============================================
CREATE TABLE IF NOT EXISTS `device` (
    id           INT AUTO_INCREMENT PRIMARY KEY,
    user_id      INT          NOT NULL,
    device_name  VARCHAR(100) NOT NULL,
    device_type  VARCHAR(20)  NOT NULL COMMENT '设备类型：mobile/web/windows/mac/linux',
    device_id    VARCHAR(100) NOT NULL COMMENT '设备唯一标识',
    os_version   VARCHAR(50)  NULL,
    app_version  VARCHAR(50)  NULL,
    ip_address   VARCHAR(50)  NULL,
    last_active  DATETIME     NULL,
    status       TINYINT      NOT NULL DEFAULT 1 COMMENT '状态：1正常/0禁用',
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_device_user (user_id),
    INDEX idx_device_type (device_type),
    INDEX idx_device_status (status),
    UNIQUE INDEX idx_device_unique (user_id, device_id),
    CONSTRAINT fk_device_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户设备表';

-- ============================================
-- 3. 文件管理表
-- ============================================
CREATE TABLE IF NOT EXISTS `file` (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id      INT           NOT NULL,
    parent_id    BIGINT        NULL,
    file_name    VARCHAR(255)  NOT NULL,
    file_path    VARCHAR(700)  NOT NULL,
    file_type    VARCHAR(20)   NULL COMMENT '文件类型：doc/image/video/audio/other',
    file_size    BIGINT        NULL,
    file_hash    VARCHAR(64)   NULL COMMENT '文件哈希值（SHA256）',
    mime_type    VARCHAR(100)  NULL,
    is_directory TINYINT(1)    NOT NULL DEFAULT 0,
    is_deleted   TINYINT(1)    NOT NULL DEFAULT 0,
    version      INT           NOT NULL DEFAULT 1,
    share_code   VARCHAR(32)   NULL,
    share_expire DATETIME      NULL,
    deleted_at   DATETIME      NULL,
    created_at   DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_file_user (user_id),
    INDEX idx_file_parent (parent_id),
    INDEX idx_file_hash (file_hash),
    INDEX idx_file_deleted (is_deleted),
    INDEX idx_file_share (share_code),
    UNIQUE INDEX uk_file_user_path (user_id, file_path) COMMENT '同一用户下文件路径唯一，trunk 主键约束',
    CONSTRAINT fk_file_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE,
    CONSTRAINT fk_file_parent FOREIGN KEY (parent_id) REFERENCES `file`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='文件表';

-- ============================================
-- 4. 文件版本历史表
-- ============================================
CREATE TABLE IF NOT EXISTS `file_version` (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    file_id      BIGINT       NOT NULL,
    version      INT          NOT NULL,
    file_size    BIGINT       NULL,
    file_hash    VARCHAR(64)  NULL,
    storage_path VARCHAR(1000) NULL,
    created_by   INT          NULL,
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_version_file (file_id),
    UNIQUE INDEX idx_version_unique (file_id, version),
    CONSTRAINT fk_version_file FOREIGN KEY (file_id) REFERENCES `file`(id) ON DELETE CASCADE,
    CONSTRAINT fk_version_user FOREIGN KEY (created_by) REFERENCES `user`(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='文件版本历史表';

-- ============================================
-- 5. 同步文件夹配置表
-- ============================================
-- 客户端无本地持久化，sync_folder 是同步映射的权威数据源。
CREATE TABLE IF NOT EXISTS `sync_folder` (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id         INT           NOT NULL,
    name            VARCHAR(100)  NULL,
    local_path      VARCHAR(1000) NOT NULL COMMENT '客户端本地目录',
    remote_path     VARCHAR(1000) NOT NULL COMMENT '服务端远端根目录',
    direction       VARCHAR(20)   NOT NULL DEFAULT 'two_way' COMMENT '方向：two_way/upload_only/download_only',
    enabled         TINYINT(1)    NOT NULL DEFAULT 1,
    excludes        TEXT          NULL COMMENT '忽略规则（含 .synctmp/.syncpending/~$*）',
    owner_device_id VARCHAR(100)  NULL,
    created_at      DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_folder_user (user_id),
    INDEX idx_folder_enabled (enabled),
    CONSTRAINT fk_folder_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='同步文件夹配置表';

-- ============================================
-- 6. 同步任务表
-- ============================================
-- 由引擎编排：源设备上报变更 → 给其它在线设备生成 download/delete/mkdir 任务。
CREATE TABLE IF NOT EXISTS `sync_task` (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id          INT           NOT NULL,
    source_device_id VARCHAR(100)  NOT NULL COMMENT '变更来源设备（server=服务端发起）',
    target_device_id VARCHAR(100)  NOT NULL COMMENT '任务执行目标设备',
    folder_id        BIGINT        NOT NULL DEFAULT 0,
    file_id          BIGINT        NOT NULL DEFAULT 0,
    task_type        VARCHAR(20)   NOT NULL COMMENT '任务类型：download/delete/mkdir/upload',
    sync_status      VARCHAR(20)   NOT NULL DEFAULT 'pending' COMMENT 'pending/syncing/completed/failed/skipped/conflict/waiting_unlock',
    direction        VARCHAR(20)   NOT NULL DEFAULT 'download',
    relative_path    VARCHAR(1000) NOT NULL,
    file_name        VARCHAR(255)  NOT NULL,
    file_size        BIGINT        NOT NULL DEFAULT 0,
    file_hash        VARCHAR(64)   NULL,
    source_hash      VARCHAR(64)   NULL,
    base_hash        VARCHAR(64)   NULL COMMENT '源端修改前看到的 trunk hash（CAS 用）',
    lock_token       VARCHAR(64)   NULL COMMENT '文件锁令牌，用于安全释放',
    conflict         TINYINT(1)    NOT NULL DEFAULT 0,
    progress         INT           NOT NULL DEFAULT 0 COMMENT '进度（0-100）',
    priority         INT           NOT NULL DEFAULT 0,
    retry_count      INT           NOT NULL DEFAULT 0,
    max_retry        INT           NOT NULL DEFAULT 3,
    error_message    TEXT          NULL,
    started_at       DATETIME      NULL,
    completed_at     DATETIME      NULL,
    created_at       DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_sync_user (user_id),
    INDEX idx_sync_source (source_device_id),
    INDEX idx_sync_target (target_device_id),
    INDEX idx_sync_folder (folder_id),
    INDEX idx_sync_file (file_id),
    INDEX idx_sync_hash (file_hash),
    INDEX idx_sync_status (sync_status),
    INDEX idx_sync_conflict (conflict),
    CONSTRAINT fk_sync_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='同步任务表';

-- ============================================
-- 7. 同步冲突待办表
-- ============================================
-- base CAS 失败（并发分叉）时落库的待办；冲突副本字节保留在出冲突的客户端本地（.syncpending）。
CREATE TABLE IF NOT EXISTS `sync_conflict` (
    id             BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id        INT           NOT NULL,
    device_id      VARCHAR(100)  NOT NULL COMMENT '出冲突的源设备',
    folder_id      BIGINT        NOT NULL DEFAULT 0,
    file_id        BIGINT        NOT NULL DEFAULT 0,
    relative_path  VARCHAR(1000) NOT NULL,
    file_name      VARCHAR(255)  NOT NULL,
    server_hash    VARCHAR(64)   NULL COMMENT '冲突时 trunk 当前 hash',
    local_hash     VARCHAR(64)   NULL COMMENT '客户端分叉版本 hash',
    base_hash      VARCHAR(64)   NULL COMMENT '客户端修改前的 base',
    server_version INT           NOT NULL DEFAULT 0,
    status         VARCHAR(20)   NOT NULL DEFAULT 'pending' COMMENT 'pending/resolved',
    resolution     VARCHAR(20)   NULL COMMENT 'accept_server/keep_local',
    resolved_at    DATETIME      NULL,
    created_at     DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_conflict_user (user_id),
    INDEX idx_conflict_device (device_id),
    INDEX idx_conflict_folder (folder_id),
    INDEX idx_conflict_status (status),
    CONSTRAINT fk_conflict_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='同步冲突待办表';

-- ============================================
-- 6. 上传统计表
-- ============================================
CREATE TABLE IF NOT EXISTS `upload_history` (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id       INT          NOT NULL,
    file_name     VARCHAR(255) NOT NULL COMMENT '存储文件名',
    original_name VARCHAR(255) NULL,
    file_size     BIGINT       NULL,
    file_type     VARCHAR(100) NULL,
    storage_path  VARCHAR(500) NULL,
    upload_status VARCHAR(20)  NOT NULL DEFAULT 'pending' COMMENT '上传状态：pending/uploading/completed/failed/cancelled',
    upload_speed  BIGINT       NULL,
    progress      TINYINT      NOT NULL DEFAULT 0 COMMENT '上传进度（0-100）',
    ip_address    VARCHAR(50)  NULL,
    user_agent    VARCHAR(500) NULL,
    error_message TEXT         NULL,
    started_at    DATETIME     NULL,
    completed_at  DATETIME     NULL,
    created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_upload_user_id (user_id),
    INDEX idx_upload_status (upload_status),
    INDEX idx_upload_created_at (created_at),
    INDEX idx_upload_file_name (file_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='文件上传记录表';

-- ============================================
-- 7. 下载统计表
-- ============================================
CREATE TABLE IF NOT EXISTS `download_history` (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id         INT         NOT NULL,
    device_id       INT         NOT NULL,
    file_id         BIGINT      NULL,
    file_name       VARCHAR(255) NULL COMMENT '文件名（快照）',
    file_size       BIGINT      NULL,
    download_status VARCHAR(20) NOT NULL DEFAULT 'pending' COMMENT '下载状态：pending/downloading/completed/failed/cancelled',
    download_speed  BIGINT      NULL,
    ip_address      VARCHAR(50) NULL,
    started_at      DATETIME    NULL,
    completed_at    DATETIME    NULL,
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_download_user (user_id),
    INDEX idx_download_device (device_id),
    INDEX idx_download_created (created_at),
    INDEX idx_download_user_created (user_id, created_at),
    CONSTRAINT fk_download_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE,
    CONSTRAINT fk_download_device FOREIGN KEY (device_id) REFERENCES `device`(id) ON DELETE CASCADE,
    CONSTRAINT fk_download_file FOREIGN KEY (file_id) REFERENCES `file`(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='下载历史记录表';

-- ============================================
-- 8. 权限管理表
-- ============================================
CREATE TABLE IF NOT EXISTS `permission` (
    id              INT AUTO_INCREMENT PRIMARY KEY,
    permission_code VARCHAR(50)  NOT NULL,
    permission_name VARCHAR(100) NOT NULL,
    parent_id       INT          NULL,
    permission_type VARCHAR(20)  NULL COMMENT '权限类型：menu/button/api',
    description     TEXT         NULL,
    sort_order      INT          NOT NULL DEFAULT 0,
    status          TINYINT      NOT NULL DEFAULT 1 COMMENT '状态：1启用/0禁用',
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_permission_code (permission_code),
    INDEX idx_permission_parent (parent_id),
    CONSTRAINT fk_permission_parent FOREIGN KEY (parent_id) REFERENCES `permission`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='权限表';

CREATE TABLE IF NOT EXISTS `role` (
    id          INT AUTO_INCREMENT PRIMARY KEY,
    role_code   VARCHAR(50)  NOT NULL,
    role_name   VARCHAR(100) NOT NULL,
    description TEXT         NULL,
    status      TINYINT      NOT NULL DEFAULT 1 COMMENT '状态：1启用/0禁用',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_role_code (role_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='角色表';

CREATE TABLE IF NOT EXISTS `role_permission` (
    id            INT AUTO_INCREMENT PRIMARY KEY,
    role_id       INT      NOT NULL,
    permission_id INT      NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_role_permission (role_id, permission_id),
    INDEX idx_role_perm_role (role_id),
    INDEX idx_role_perm_permission (permission_id),
    CONSTRAINT fk_role_perm_role FOREIGN KEY (role_id) REFERENCES `role`(id) ON DELETE CASCADE,
    CONSTRAINT fk_role_perm_permission FOREIGN KEY (permission_id) REFERENCES `permission`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='角色权限关联表';

CREATE TABLE IF NOT EXISTS `user_role` (
    id         INT AUTO_INCREMENT PRIMARY KEY,
    user_id    INT      NOT NULL,
    role_id    INT      NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_user_role (user_id, role_id),
    INDEX idx_user_role_user (user_id),
    INDEX idx_user_role_role (role_id),
    CONSTRAINT fk_user_role_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE,
    CONSTRAINT fk_user_role_role FOREIGN KEY (role_id) REFERENCES `role`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户角色关联表';

-- ============================================
-- 9. 字典管理表
-- ============================================
CREATE TABLE IF NOT EXISTS `dict_type` (
    id          INT AUTO_INCREMENT PRIMARY KEY,
    dict_code   VARCHAR(50)  NOT NULL,
    dict_name   VARCHAR(100) NOT NULL,
    description TEXT         NULL,
    status      TINYINT      NOT NULL DEFAULT 1 COMMENT '状态：1启用/0禁用',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_dict_type_code (dict_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='字典类型表';

CREATE TABLE IF NOT EXISTS `dict_data` (
    id           INT AUTO_INCREMENT PRIMARY KEY,
    dict_type_id INT          NOT NULL,
    dict_label   VARCHAR(100) NOT NULL COMMENT '字典标签',
    dict_value   VARCHAR(100) NOT NULL COMMENT '字典值',
    dict_sort    INT          NOT NULL DEFAULT 0,
    css_class    VARCHAR(50)  NULL,
    tag_type     VARCHAR(20)  NULL,
    remark       TEXT         NULL,
    status       TINYINT      NOT NULL DEFAULT 1 COMMENT '状态：1启用/0禁用',
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_dict_data_type (dict_type_id),
    INDEX idx_dict_data_value (dict_value),
    INDEX idx_dict_data_sort (dict_sort),
    CONSTRAINT fk_dict_data_type FOREIGN KEY (dict_type_id) REFERENCES `dict_type`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='字典数据表';

-- ============================================
-- 10. 操作日志表
-- ============================================
CREATE TABLE IF NOT EXISTS `operation_log` (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id          INT          NULL,
    device_id        INT          NULL,
    operation_type   VARCHAR(50)  NULL COMMENT '操作类型：upload/download/delete/share',
    operation_module VARCHAR(50)  NULL,
    operation_desc   TEXT         NULL,
    request_method   VARCHAR(10)  NULL,
    request_url      VARCHAR(500) NULL,
    request_params   TEXT         NULL,
    response_result  TEXT         NULL,
    ip_address       VARCHAR(50)  NULL,
    user_agent       VARCHAR(500) NULL,
    status           TINYINT      NULL COMMENT '状态：1成功/0失败',
    error_message    TEXT         NULL,
    execution_time   INT          NULL COMMENT '执行时长（毫秒）',
    created_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_log_user (user_id),
    INDEX idx_log_created (created_at),
    INDEX idx_log_type (operation_type),
    CONSTRAINT fk_log_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE SET NULL,
    CONSTRAINT fk_log_device FOREIGN KEY (device_id) REFERENCES `device`(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='操作日志表';

-- ============================================
-- 11. 存储配置表
-- ============================================
CREATE TABLE IF NOT EXISTS `storage_config` (
    id          INT AUTO_INCREMENT PRIMARY KEY,
    user_id     INT      NOT NULL,
    total_quota BIGINT   NOT NULL DEFAULT 10737418240 COMMENT '总配额（字节）默认10GB',
    used_quota  BIGINT   NOT NULL DEFAULT 0,
    file_count  INT      NOT NULL DEFAULT 0,
    last_sync   DATETIME NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_storage_user (user_id),
    CONSTRAINT fk_storage_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='存储配置表';

-- ============================================
-- 12. 分享记录表
-- ============================================
CREATE TABLE IF NOT EXISTS `share_record` (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id         INT         NOT NULL,
    file_id         BIGINT      NOT NULL,
    share_code      VARCHAR(32) NOT NULL,
    share_password  VARCHAR(20) NULL,
    expire_time     DATETIME    NULL,
    download_limit  INT         NULL COMMENT '下载次数限制',
    download_count  INT         NOT NULL DEFAULT 0,
    visit_count     INT         NOT NULL DEFAULT 0,
    status          TINYINT     NOT NULL DEFAULT 1 COMMENT '状态：1有效/0失效',
    created_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_share_code (share_code),
    INDEX idx_share_user (user_id),
    INDEX idx_share_created (created_at),
    CONSTRAINT fk_share_user FOREIGN KEY (user_id) REFERENCES `user`(id) ON DELETE CASCADE,
    CONSTRAINT fk_share_file FOREIGN KEY (file_id) REFERENCES `file`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='分享记录表';

-- ============================================
-- 13. 初始化种子数据
-- ============================================

-- 初始化字典类型
INSERT IGNORE INTO dict_type (id, dict_code, dict_name, description) VALUES
(1, 'device_type', '设备类型', '用户设备类型枚举'),
(2, 'file_type', '文件类型', '文件类型枚举'),
(3, 'sync_status', '同步状态', '同步任务状态枚举'),
(4, 'download_status', '下载状态', '下载状态枚举'),
(5, 'user_status', '用户状态', '用户状态枚举'),
(6, 'operation_type', '操作类型', '操作日志类型枚举');

-- 初始化字典数据
INSERT IGNORE INTO dict_data (dict_type_id, dict_label, dict_value, dict_sort) VALUES
(1, '手机端', 'mobile', 1),
(1, 'Web端', 'web', 2),
(1, 'Windows端', 'windows', 3),
(1, 'Mac端', 'mac', 4),
(1, 'Linux端', 'linux', 5),
(2, '文档', 'doc', 1),
(2, '图片', 'image', 2),
(2, '视频', 'video', 3),
(2, '音频', 'audio', 4),
(2, '其他', 'other', 5),
(3, '等待中', 'pending', 1),
(3, '同步中', 'syncing', 2),
(3, '已完成', 'completed', 3),
(3, '失败', 'failed', 4),
(4, '等待中', 'pending', 1),
(4, '下载中', 'downloading', 2),
(4, '已完成', 'completed', 3),
(4, '失败', 'failed', 4),
(4, '已取消', 'cancelled', 5),
(5, '正常', '1', 1),
(5, '禁用', '0', 2),
(6, '上传', 'upload', 1),
(6, '下载', 'download', 2),
(6, '删除', 'delete', 3),
(6, '分享', 'share', 4),
(6, '移动', 'move', 5),
(6, '重命名', 'rename', 6);

-- 初始化角色
INSERT IGNORE INTO role (id, role_code, role_name, description) VALUES
(1, 'admin', '系统管理员', '拥有所有权限'),
(2, 'user', '普通用户', '基础文件同步权限');

-- 初始化权限
INSERT IGNORE INTO permission (id, permission_code, permission_name, permission_type, description, sort_order) VALUES
(1, 'file:upload', '文件上传', 'api', '允许上传文件', 1),
(2, 'file:download', '文件下载', 'api', '允许下载文件', 2),
(3, 'file:delete', '文件删除', 'api', '允许删除文件', 3),
(4, 'file:share', '文件分享', 'api', '允许分享文件', 4),
(5, 'file:manage', '文件管理', 'menu', '文件管理菜单', 5),
(6, 'user:manage', '用户管理', 'menu', '用户管理菜单（仅管理员）', 6),
(7, 'system:config', '系统配置', 'menu', '系统配置菜单（仅管理员）', 7);
