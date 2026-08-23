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

// NetSampler 有状态的网卡速率采样器。
//
// ⚠ 速率 = 两次采样的字节差 / 时间差，所以**基线必须是私有的**。
// 早先这里用一个包级全局 lastNet，HTTP 一次性接口和 WS 流式推送共用它 →
// 谁先采一次就把对方的基线吃掉、重置，两边算出来的速率互相打架。
// 现在每个消费者（HTTP handler、WS broadcaster）各持一个 NetSampler，互不干扰。
type NetSampler struct {
	last netSnapshot
}

// Sample 采一次：返回累计字节与瞬时速率（字节/秒）。首次调用无基线，速率为 0。
func (s *NetSampler) Sample() (sent, recv uint64, sendRate, recvRate float64, perNIC []gin.H) {
	now := time.Now()
	if counters, err := psnet.IOCounters(false); err == nil && len(counters) > 0 {
		sent, recv = counters[0].BytesSent, counters[0].BytesRecv
	}
	if !s.last.at.IsZero() {
		if elapsed := now.Sub(s.last.at).Seconds(); elapsed > 0 {
			if sent >= s.last.sent {
				sendRate = float64(sent-s.last.sent) / elapsed
			}
			if recv >= s.last.recv {
				recvRate = float64(recv-s.last.recv) / elapsed
			}
		}
	}
	s.last = netSnapshot{at: now, sent: sent, recv: recv}

	perNIC = make([]gin.H, 0, 4)
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
	return
}

// httpNetSampler HTTP /monitor/network 专用采样器（与 WS broadcaster 的分开）。
var httpNetSampler NetSampler

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

// SystemSnapshot 采一次系统概览。纯采集、无状态，供 HTTP 一次性接口与 WS 流式推送共用。
// ⚠ 含 cpuSampleWindow 的阻塞采样（默认 300ms），不要在持锁时调用。
func SystemSnapshot() gin.H {
	return gin.H{
		"cpu":    collectCPU(),
		"memory": collectMem(),
		"host":   collectHost(),
		"disks":  collectDisks(),
	}
}

// System GET /v1/monitor/system —— CPU / 内存 / 主机 / 磁盘 概览（一次性）。
// 实时刷新走 WS（见 broadcaster.go），本接口保留给 Web 端 / 首屏快照。
func System(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": SystemSnapshot()})
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

// NetworkSnapshot 用给定采样器采一次网络概览 + WS 连接概况。
// 速率由 sampler 的私有基线算出（见 NetSampler 注释），供 HTTP 与 WS 各自复用。
func NetworkSnapshot(sampler *NetSampler) gin.H {
	sent, recv, sendRate, recvRate, perNIC := sampler.Sample()
	hub := ws.GetHub()
	return gin.H{
		"bytes_sent":         sent,
		"bytes_recv":         recv,
		"send_rate":          sendRate, // 字节/秒
		"recv_rate":          recvRate,
		"interfaces":         perNIC,
		"online_devices":     len(hub.OnlineDeviceIDs()),
		"online_users":       len(hub.GetOnlineUsers()),
		"active_connections": hub.ConnectionCount(),
		"server_time":        time.Now().Unix(),
	}
}

// Network GET /v1/monitor/network —— 网卡吞吐 + WS 连接概况（一次性）。
// 实时刷新走 WS（见 broadcaster.go）。
func Network(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": NetworkSnapshot(&httpNetSampler)})
}
