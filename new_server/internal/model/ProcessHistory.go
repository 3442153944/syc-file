package model

import "time"

// ProcessHistory 进程资源占用历史（Top-N，见 internal/monitor/sys_detail.go）。
// 一次采集对应同一个 Time 下的多行（每个进入 Top-N 的进程一行）。
type ProcessHistory struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement;comment:主键" json:"id"`
	Time           time.Time `gorm:"index;not null;comment:采集时间" json:"time"`
	PID            uint32    `gorm:"comment:进程 PID" json:"pid"`
	Name           string    `gorm:"size:255;comment:进程名" json:"name"`
	CPUPercent     float64   `gorm:"comment:CPU 使用率(%)" json:"cpu_percent"`
	MemBytes       uint64    `gorm:"comment:内存占用字节数" json:"mem_bytes"`
	MemPercent     float64   `gorm:"comment:内存占用占系统总内存百分比" json:"mem_percent"`
	DiskReadBytes  uint64    `gorm:"comment:采集间隔内磁盘读字节数" json:"disk_read_bytes"`
	DiskWriteBytes uint64    `gorm:"comment:采集间隔内磁盘写字节数" json:"disk_write_bytes"`
	Connections    uint32    `gorm:"comment:该进程持有的 TCP/UDP 连接数" json:"connections"`
	Score          float64   `gorm:"comment:排序用综合评分(2*cpu+1.5*mem+1*连接数,均归一化)" json:"score"`
}

func (ProcessHistory) TableName() string { return "process_history" }

// ListeningPortHistory 监听端口历史。
type ListeningPortHistory struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement;comment:主键" json:"id"`
	Time        time.Time `gorm:"index;not null;comment:采集时间" json:"time"`
	Port        uint16    `gorm:"comment:端口号" json:"port"`
	Protocol    string    `gorm:"size:8;comment:协议(tcp/udp)" json:"protocol"`
	PID         uint32    `gorm:"comment:监听进程 PID" json:"pid"`
	ProcessName string    `gorm:"size:255;comment:监听进程名" json:"process_name"`
}

func (ListeningPortHistory) TableName() string { return "listening_port_history" }

// PortConnHistory 端口连接数历史（按端口/协议聚合，不含每条连接明细）。
type PortConnHistory struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement;comment:主键" json:"id"`
	Time        time.Time `gorm:"index;not null;comment:采集时间" json:"time"`
	Port        uint16    `gorm:"comment:端口号" json:"port"`
	Protocol    string    `gorm:"size:8;comment:协议(tcp/udp)" json:"protocol"`
	Connections uint32    `gorm:"comment:该端口当前连接数" json:"connections"`
}

func (PortConnHistory) TableName() string { return "port_conn_history" }
