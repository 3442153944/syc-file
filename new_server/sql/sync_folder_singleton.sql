-- ============================================
-- sync_folder 收敛为"每用户唯一一条"迁移脚本
-- 由用户手动执行，且必须在部署新版 Go 二进制（已改为 upsert 语义）之前执行——
-- 新代码假设 sync_folder.user_id 唯一，旧库如果还有重复行，新代码的
-- ON DUPLICATE KEY 逻辑跑不起来（约束还没加），必须先跑本脚本。
--
-- 执行前务必先备份 sync_folder / sync_task / sync_conflict 三张表，
-- 并先在测试库跑一遍确认结果符合预期。
-- ============================================

-- 第一步：只读排查，确认是否真的存在重复（不能假设为 0，必须先看结果）
SELECT user_id, COUNT(*) AS c FROM sync_folder GROUP BY user_id HAVING c > 1;

-- 如果上面查询返回 0 行，说明当前库没有重复数据，可以跳过第二~四步，
-- 直接执行最后的 ALTER TABLE 加唯一约束。

-- 第二步：物化每个 user_id 要保留之外的“待删”folder id
-- 保留规则：enabled=1 优先 > updated_at 最近 > id 最大，兜底选最可能是
-- 用户当前实际在用的那一条。
DROP TEMPORARY TABLE IF EXISTS sync_folder_losers;
CREATE TEMPORARY TABLE sync_folder_losers AS
SELECT id FROM (
    SELECT id, ROW_NUMBER() OVER (
        PARTITION BY user_id ORDER BY enabled DESC, updated_at DESC, id DESC
    ) AS rn
    FROM sync_folder
) t WHERE rn > 1;

-- 第三步：清理挂在待删 folder 上的非终态任务/冲突（folder_id 无外键，不会自动级联）。
-- 终态记录（completed/failed/skipped/resolved）不动，留作历史审计。
DELETE st FROM sync_task st
JOIN sync_folder_losers l ON st.folder_id = l.id
WHERE st.sync_status IN ('pending', 'syncing', 'waiting_unlock', 'conflict');

DELETE sc FROM sync_conflict sc
JOIN sync_folder_losers l ON sc.folder_id = l.id
WHERE sc.status = 'pending';

-- 第四步：删除待删 folder 行
DELETE FROM sync_folder WHERE id IN (SELECT id FROM sync_folder_losers);

DROP TEMPORARY TABLE IF EXISTS sync_folder_losers;

-- 第五步：加唯一约束（新建库通过 init_mysql.sql 已自带，这里是给已存在的库补上）。
-- 索引名必须叫 idx_sync_folder_user_id：这是 GORM AutoMigrate 按 model.SyncFolder.UserID
-- 的 gorm:"uniqueIndex" 标签算出来的默认名字，cmd/main.go 每次启动都会跑 AutoMigrate，
-- 名字对不上它就认不出"唯一约束已经满足"，会在旧的同名非唯一索引上重新折腾，
-- 之前就是这样把叫别的名字的唯一索引干掉、换回一个非唯一的，约束名存实亡。
-- 如果这张表上已经有旧的 idx_sync_folder_user_id（非唯一）索引，先 DROP 掉再建。
ALTER TABLE sync_folder ADD UNIQUE INDEX idx_sync_folder_user_id (user_id);

-- 验证：下面这条应返回 0 行
SELECT user_id, COUNT(*) AS c FROM sync_folder GROUP BY user_id HAVING c > 1;
