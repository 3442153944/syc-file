package monitor

import (
	"fmt"
	"time"

	"syc-file/pkg/logger"
)

// 监控历史长期表（MySQL）不清理会无限增长——process_history 这类明细表每次
// 采集就是一批行（一个进程一行），月复一月攒下去体量很可观。清理策略是逐级
// 降采样，不是简单按天硬删：
//
//   - 1 个月以内：保持原始密度（采多密就存多密，图表需要细节）。
//   - 1 个月 ~ 1 年：降到每小时保留一条采集时刻（同一小时内的其它时刻整批删掉）。
//   - 1 年以上：再降到每天保留一条采集时刻。
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
	retentionHourlyTierAge = 30 * 24 * time.Hour  // 超过这个年龄才降到按小时
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
func StartRetentionCleaner() {
	retentionCleanupOnce()
	go func() {
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
	hourlyTierEnd := now.Add(-retentionHourlyTierAge) // 1 个月前
	dailyTierEnd := now.Add(-retentionDailyTierAge)   // 1 年前

	for _, t := range downsampleTables {
		// 1 个月 ~ 1 年：按小时降采样。每天重跑一遍是幂等的——已经降过的区间
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

// downsampleRange 在 [from, to) 区间内，按 bucketFormat（MySQL DATE_FORMAT 格式串）
// 分桶，每桶只留最早的一个 time，其余同表同 time 的行整批删除。
//
// 用 DELETE ... WHERE time NOT IN (子查询里的派生表) 而不是直接子查询同一张表——
// MySQL 不允许"UPDATE/DELETE 的目标表同时出现在自己的 FROM 子句里"，套一层派生表
// （kept）绕开这个限制，是标准写法。
func downsampleRange(table, timeCol, bucketFormat string, from, to time.Time) error {
	sql := fmt.Sprintf(`
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
	return db.Exec(sql, from, to, from, to, bucketFormat).Error
}
