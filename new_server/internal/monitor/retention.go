package monitor

import (
	"database/sql"
	"fmt"
	"time"

	"syc-file/pkg/logger"
)

// 监控历史长期表（MySQL）不清理会无限增长——process_history 这类明细表每次
// 采集就是一批行（一个进程一行），不清理一天就能涨 ~260MB。清理策略是逐级
// 降采样，不是简单按天硬删：
//
//   - 3 天以内：保持原始密度（采多密就存多密，近实时图表需要细节）。
//   - 3 天 ~ 1 年：降到每小时保留一条采集时刻（同一小时内的其它时刻整批删掉）。
//   - 1 年以上：再降到每天保留一条采集时刻。
//
// 原策略是 30 天原始密度，磁盘增速和旧图表查询都扛不住（2026-10-05 改成 3 天）；
// 超过 3 天的历史图表本来也看不出 30s 级别的差别，按小时粒度足够。
//
// "保留一条采集时刻"而不是"保留一行"：process_history/listening_port_history/
// port_conn_history 每次采集对应同一个 time 下的好几行（每个 Top-N 进程/端口各一
// 行），降采样要按 time 整体取舍，不能只删到剩一行——那样会把同一时刻其它进程的
// 数据也弄丢，破坏这一条采集记录的完整性。
//
// resource_alert 不在这套降采样范围内：它是离散的告警事件，不是周期性采样点，
// 按"每小时留一条"会真的把同一小时内先后发生的两次不同告警丢掉一次，这是数据
// 丢失而不是压缩。告警本身触发概率低、量不大，不清理也不会把库撑爆，就不清了。
const (
	retentionHourlyTierAge = 3 * 24 * time.Hour   // 超过这个年龄才降到按小时（原 30 天，磁盘增速扛不住）
	retentionDailyTierAge  = 365 * 24 * time.Hour // 超过这个年龄才降到按天
)

// downsampleTables：periodic 采样表，每次采集在同一个 time 下可能有多行。
var downsampleTables = []struct{ table, timeCol string }{
	{"process_history", "time"},
	{"listening_port_history", "time"},
	{"port_conn_history", "time"},
	{"monitor_history", "time"},
}

// StartRetentionCleaner 每天跑一次降采样清理，错开凌晨其它归档任务的时间点
// （StartDailyArchiver/StartSysDetailArchiver 分别在 0:00/0:05 前后，这里放 0:30）。
//
// 首次清理放后台异步跑：改成 3 天窗口后，第一次要把积压的多天原始数据降采样，
// 可能要几十秒到几分钟；同步跑会顶住进程启动，让 deploy.sh 的 40s 健康检查超时回滚。
// 清理是幂等的，晚几秒执行没有任何副作用。
func StartRetentionCleaner() {
	go func() {
		retentionCleanupOnce()
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 30, 0, 0, time.UTC)
			time.Sleep(next.Sub(now))
			retentionCleanupOnce()
		}
	}()
}

func retentionCleanupOnce() {
	if db == nil {
		return
	}
	now := time.Now()
	hourlyTierEnd := now.Add(-retentionHourlyTierAge) // 3 天前
	dailyTierEnd := now.Add(-retentionDailyTierAge)   // 1 年前

	for _, t := range downsampleTables {
		// 3 天 ~ 1 年：按小时降采样。每天重跑一遍是幂等的——已经降过的区间
		// 每小时本来就只剩 1 条，NOT IN 子查询选出来的"要保留的"就是它自己，
		// 删除条件命不中任何行，白跑一次而已，不会越删越多或重复计费。
		if err := downsampleRange(t.table, t.timeCol, "%Y-%m-%d %H", dailyTierEnd, hourlyTierEnd); err != nil {
			logger.Logger.Warn("监控历史按小时降采样失败: " + t.table + ": " + err.Error())
		}
		// 1 年以上：按天降采样，不设下界——早年的数据一起收进来，反正降到
		// 天粒度之后体量已经很小，没必要再设更远的硬删除线。
		if err := downsampleRange(t.table, t.timeCol, "%Y-%m-%d", time.Unix(0, 0), dailyTierEnd); err != nil {
			logger.Logger.Warn("监控历史按天降采样失败: " + t.table + ": " + err.Error())
		}
	}
}

// downsampleRange 在 [from, to) 区间内按 bucketFormat 分桶降采样：
// 区间按天切开逐段执行，避免一次 DELETE 扫/删多天数据（首次清理积压时是百万级行，
// 单条语句事务过大）；并用表里实际的 MIN(time) 收缩下界，防止 [epoch, 1y-ago) 这类
// 区间被切出成千上万个空 DELETE（空表直接返回）。
func downsampleRange(table, timeCol, bucketFormat string, from, to time.Time) error {
	if !from.Before(to) {
		return nil
	}
	row := db.Table(table).Select("MIN(" + timeCol + ") AS min_time").Row()
	var mt sql.NullTime
	if err := row.Scan(&mt); err != nil {
		return err
	}
	if !mt.Valid {
		return nil // 空表
	}
	if mt.Time.After(from) {
		from = mt.Time
	}
	const chunk = 24 * time.Hour
	for start := from; start.Before(to); start = start.Add(chunk) {
		end := start.Add(chunk)
		if end.After(to) {
			end = to
		}
		if err := downsampleChunk(table, timeCol, bucketFormat, start, end); err != nil {
			return err
		}
	}
	return nil
}

// downsampleChunk 对单个不超过 24h 的区间执行一次分桶降采样：每桶只留最早的
// 一个 time，其余同表同 time 的行整批删除。
//
// 用 DELETE ... WHERE time NOT IN (子查询里的派生表) 而不是直接子查询同一张表——
// MySQL 不允许"UPDATE/DELETE 的目标表同时出现在自己的 FROM 子句里"，套一层派生表
// （kept）绕开这个限制，是标准写法。
func downsampleChunk(table, timeCol, bucketFormat string, from, to time.Time) error {
	query := fmt.Sprintf(`
		DELETE FROM %s
		WHERE %s >= ? AND %s < ?
		AND %s NOT IN (
			SELECT keep_time FROM (
				SELECT MIN(%s) AS keep_time
				FROM %s
				WHERE %s >= ? AND %s < ?
				GROUP BY DATE_FORMAT(%s, ?)
			) AS kept
		)`, table, timeCol, timeCol, timeCol, timeCol, table, timeCol, timeCol, timeCol)
	return db.Exec(query, from, to, from, to, bucketFormat).Error
}
