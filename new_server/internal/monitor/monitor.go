// Package monitor 提供系统与网络运行指标，供桌面端「监控」页展示。
//
// 全部指标取自 gopsutil（已因 available_disks 引入，无新依赖）。
// 这些接口只读、无副作用，但会暴露主机信息，故一律要求已登录（路由挂在 v1 认证组下）。
package monitor

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	psnet "github.com/shirou/gopsutil/v3/net"

	"syc-file/internal/ws"
)

// cpuSampleWindow CPU 使用率采样窗口。太短会读到 0，太长会拖慢接口。
const cpuSampleWindow = 300 * time.Millisecond

// netSnapshot 上一次网卡计数器快照，用于算速率（gopsutil 给的是累计字节数）。
type netSnapshot struct {
	at   time.Time
	sent uint64
	recv uint64
}

var lastNet netSnapshot

type cpuInfo struct {
	UsedPercent float64   `json:"used_percent"`
	Cores       int       `json:"cores"`
	ModelName   string    `json:"model_name"`
	PerCore     []float64 `json:"per_core"`
	Load1       float64   `json:"load1"`
	Load5       float64   `json:"load5"`
	Load15      float64   `json:"load15"`
}

type memInfo struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
}

type hostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformVersion string `json:"platform_version"`
	KernelArch      string `json:"kernel_arch"`
	UptimeSeconds   uint64 `json:"uptime_seconds"`
	BootTime        int64  `json:"boot_time"`
	Procs           uint64 `json:"procs"`
	GoVersion       string `json:"go_version"`
	ServerTime      int64  `json:"server_time"`
}

type diskItem struct {
	Path        string  `json:"path"`
	Fstype      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

// System GET /v1/monitor/system —— CPU / 内存 / 主机 / 磁盘 概览。
func System(c *gin.Context) {
	data := gin.H{
		"cpu":    collectCPU(),
		"memory": collectMem(),
		"host":   collectHost(),
		"disks":  collectDisks(),
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": data})
}

func collectCPU() cpuInfo {
	out := cpuInfo{Cores: runtime.NumCPU()}
	// percpu=false 取整机总使用率；再单独取每核，供前端画柱状
	if total, err := cpu.Percent(cpuSampleWindow, false); err == nil && len(total) > 0 {
		out.UsedPercent = total[0]
	}
	if per, err := cpu.Percent(0, true); err == nil {
		out.PerCore = per
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		out.ModelName = infos[0].ModelName
	}
	// Windows 上 load 不可用，取不到就留 0，前端按 0 隐藏
	if l, err := load.Avg(); err == nil && l != nil {
		out.Load1, out.Load5, out.Load15 = l.Load1, l.Load5, l.Load15
	}
	return out
}

func collectMem() memInfo {
	out := memInfo{}
	if v, err := mem.VirtualMemory(); err == nil && v != nil {
		out.Total, out.Used, out.Free, out.UsedPercent = v.Total, v.Used, v.Free, v.UsedPercent
	}
	if s, err := mem.SwapMemory(); err == nil && s != nil {
		out.SwapTotal, out.SwapUsed = s.Total, s.Used
	}
	return out
}

func collectHost() hostInfo {
	out := hostInfo{GoVersion: runtime.Version(), ServerTime: time.Now().Unix()}
	if h, err := host.Info(); err == nil && h != nil {
		out.Hostname, out.OS, out.Platform = h.Hostname, h.OS, h.Platform
		out.PlatformVersion, out.KernelArch = h.PlatformVersion, h.KernelArch
		out.UptimeSeconds, out.Procs = h.Uptime, h.Procs
		out.BootTime = int64(h.BootTime)
	}
	return out
}

// collectDisks 只报真实挂载的物理分区，拿不到用量的分区跳过（光驱/未插卡的读卡器等）。
func collectDisks() []diskItem {
	items := make([]diskItem, 0, 8)
	parts, err := disk.Partitions(false)
	if err != nil {
		return items
	}
	for _, p := range parts {
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil || usage == nil || usage.Total == 0 {
			continue
		}
		items = append(items, diskItem{
			Path:        p.Mountpoint,
			Fstype:      p.Fstype,
			Total:       usage.Total,
			Used:        usage.Used,
			Free:        usage.Free,
			UsedPercent: usage.UsedPercent,
		})
	}
	return items
}

// Network GET /v1/monitor/network —— 网卡吞吐 + WS 连接概况。
//
// 速率由两次调用之间的累计字节差算出：首次调用没有基线，速率返回 0，
// 前端按固定间隔轮询即可得到连续曲线。
func Network(c *gin.Context) {
	now := time.Now()
	var sent, recv uint64
	if counters, err := psnet.IOCounters(false); err == nil && len(counters) > 0 {
		sent, recv = counters[0].BytesSent, counters[0].BytesRecv
	}

	var sendRate, recvRate float64
	if !lastNet.at.IsZero() {
		if elapsed := now.Sub(lastNet.at).Seconds(); elapsed > 0 {
			if sent >= lastNet.sent {
				sendRate = float64(sent-lastNet.sent) / elapsed
			}
			if recv >= lastNet.recv {
				recvRate = float64(recv-lastNet.recv) / elapsed
			}
		}
	}
	lastNet = netSnapshot{at: now, sent: sent, recv: recv}

	perNIC := make([]gin.H, 0, 4)
	if counters, err := psnet.IOCounters(true); err == nil {
		for _, c2 := range counters {
			if c2.BytesSent == 0 && c2.BytesRecv == 0 {
				continue // 没跑过流量的虚拟网卡，列出来只是噪音
			}
			perNIC = append(perNIC, gin.H{
				"name": c2.Name, "bytes_sent": c2.BytesSent, "bytes_recv": c2.BytesRecv,
				"packets_sent": c2.PacketsSent, "packets_recv": c2.PacketsRecv,
				"errin": c2.Errin, "errout": c2.Errout, "dropin": c2.Dropin, "dropout": c2.Dropout,
			})
		}
	}

	hub := ws.GetHub()
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
		"bytes_sent":         sent,
		"bytes_recv":         recv,
		"send_rate":          sendRate, // 字节/秒
		"recv_rate":          recvRate,
		"interfaces":         perNIC,
		"online_devices":     len(hub.OnlineDeviceIDs()),
		"online_users":       len(hub.GetOnlineUsers()),
		"active_connections": hub.ConnectionCount(),
		"server_time":        now.Unix(),
	}})
}
