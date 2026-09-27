package model

import "time"

// MonitorHistory 系统监控历史采样点的长期归档。
// Redis 只留 7~8 天热数据供快速查询（见 internal/monitor/history.go 的
// historyTTL），这张表由每天的归档任务写入前一天的全部采样点，长期保存，
// 供仪表盘查看超过一周的历史趋势。
type MonitorHistory struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement;comment:主键" json:"id"`
	Time              time.Time `gorm:"index;not null;comment:采集时间" json:"time"`
	CPUPercent        float64   `gorm:"comment:CPU 使用率(%)" json:"cpu_percent"`
	MemPercent        float64   `gorm:"comment:内存使用率(%)" json:"mem_percent"`
	MemUsed           uint64    `gorm:"comment:内存已用字节数" json:"mem_used"`
	SendRate          float64   `gorm:"comment:网络发送速率(字节/秒)" json:"send_rate"`
	RecvRate          float64   `gorm:"comment:网络接收速率(字节/秒)" json:"recv_rate"`
	OnlineDevices     int       `gorm:"comment:在线设备数" json:"online_devices"`
	ActiveConnections int       `gorm:"comment:活跃 WS 连接数" json:"active_connections"`
}

func (MonitorHistory) TableName() string { return "monitor_history" }
