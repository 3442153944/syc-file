package model

import "time"

// ResourceAlert 资源告警：某个 CPU 核心的占用率连续多次越过阈值时触发一条记录，
// 见 internal/monitor/resource_alert.go 的 StartResourceAlertWatcher。
// 触发条件看的是单核占用而不是单个进程——异常负载常常是好几个进程一起抢同一批
// 核心，每个进程单独看都不显眼，但核心本身已经被打满；TopProcesses 只是触发那
// 一刻顺手存一份 Top-N 进程快照供排查参考，不是判断触发与否的依据。
type ResourceAlert struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement;comment:主键" json:"id"`
	TriggeredAt  time.Time  `gorm:"index;not null;comment:触发时间" json:"triggered_at"`
	TriggerCore  int        `gorm:"comment:触发告警的核心序号(0-based)" json:"trigger_core"`
	TriggerCPU   float64    `gorm:"comment:触发时该核心的占用率(%)" json:"trigger_cpu_percent"`
	TopProcesses string     `gorm:"type:text;comment:触发时 Top-N 进程快照(JSON，仅供参考)" json:"top_processes"`
	Status       string     `gorm:"size:16;default:active;index;comment:active/resolved" json:"status"`
	ResolvedAt   *time.Time `gorm:"comment:恢复时间，未恢复为空" json:"resolved_at"`
}

func (ResourceAlert) TableName() string { return "resource_alert" }
