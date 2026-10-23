-- vps_node.sql — VPS 节点面板的菜单入口
--
-- 表本身由 GORM AutoMigrate 在服务启动时创建（model.VpsNode → vps_node），
-- 这里只补 sys_route 里的菜单行：内置路由目录（internal/system/catalog.go）只在
-- 路由表为空时做一次性初始化，已上线的库不会自动补插，所以要手工加一行。
--
-- 用法（dev 库）：
--   mysql -h 192.168.31.120 -u syncfile -p syncfile-dev < vps_node.sql
--
-- 说明：
--   * builtin = 1：与代码内置目录一致，页面在「路由管理」里不可删除，只能停用/改展示；
--   * min_level = 3：仅管理员可见（与 DSH 访问令牌同级别）；
--   * component = views/admin/VpsNodeManage.vue：必须与客户端 src/views 下文件路径一致，
--     否则客户端会忽略该条目（页面随客户端发版）。

INSERT INTO sys_route
    (code, parent_code, path, name, component, title, icon,
     sort, hidden, min_level, perm, enabled, builtin, created_at, updated_at)
SELECT 'admin.vps', 'admin', '/admin/vps', 'AdminVpsNodes',
       'views/admin/VpsNodeManage.vue', '节点面板', 'setting',
       85, 0, 3, '', 1, 1, NOW(), NOW()
FROM DUAL
WHERE NOT EXISTS (SELECT 1 FROM sys_route WHERE code = 'admin.vps');
