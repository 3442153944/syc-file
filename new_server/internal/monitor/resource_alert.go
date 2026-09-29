package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/cpu"

	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/filecore"
	"syc-file/pkg/logger"
)

// 资源告警：看的是单个 CPU 核心的占用率，不是单个进程的占用率。异常负载常常是
// 好几个进程一起抢同一批核心——每个进程单独看都不到 80%、显不出异常，但核心
// 本身已经被打满，进程之间在抢时间片；只按"哪个进程超阈值"来判断会把这种情况
// 完全漏掉。per-core 数据走 gopsutil 的 cpu.Percent，用固定的短窗口
// （alertSampleWindow）而不是 monitor.go 那种"距上次调用以来"的增量算法——
// 后者靠包级共享状态，会跟 WS 推送/HTTP 接口那边的调用互相打乱采样窗口。
//
// 连续多次越过阈值才判定为真告警，防的是"一个采样点瞬时冒尖、下一个就回落"的抖动。
//
// 触发后做三件事：① 落一条 resource_alert 记录，顺手采一份 Top-N 进程快照存进去
// 供排查参考（不是触发依据）；② 那份快照顺带也补进常规的 process_history，告警
// 时刻能在进程页图表上对上；③ 广播一条通知，桌面端弹出来。
//
// 触发之后转入"加密巡检"：仍然看同一个信号（哪个核心占用最高），只是把检查频率
// 从 3s 基线提到 0.5s，直到那个最高值退回到（触发阈值-10%）以下才判定恢复，
// 退回 3s 基线，并再广播一条"已恢复"通知。
//
// 基线巡检（3s）自己独立跑，不复用 sys_detail 那条给图表用的采集 ticker——两者
// 用途不同：sys_detail 默认 30s 是为了控制存储量，告警巡检要的是"尽快发现异常"，
// 拉太长会让"突发"变成"过了好一会儿才发现"，等于没防住。
const (
	alertBaselineInterval = 3 * time.Second
	alertTrackInterval    = 500 * time.Millisecond
	alertTriggerThreshold = 80.0 // 单核占用率（%），连续 breachStreak 次超过才算
	alertRecoverThreshold = alertTriggerThreshold - 10.0
	alertBreachStreak     = 3
	alertScanTopN         = 20 // 触发时顺带采的进程快照条数，仅供参考用
	// gopsutil 的 cpu.Percent(0, ...) 用的是包级共享的"上次调用时间点"，跟
	// monitor.go 的 collectCPU()（WS 推送、/monitor/system 接口都在用）共用同一份
	// 状态——两边各自独立调用会互相打乱对方的采样窗口。这里改用非零的固定窗口，
	// 自成一份独立测量，不跟其它调用方互相干扰。
	alertSampleWindow = 200 * time.Millisecond
)

type activeAlert struct {
	id uint64
}

// StartResourceAlertWatcher 后台常驻巡检，独立于 sys_detail 的历史采集 ticker。
// 在 db/redisClient 就绪（Init 已调用）之后、main 里其它 monitor 启动函数旁边调用。
func StartResourceAlertWatcher() {
	go func() {
		streak := 0
		var active *activeAlert
		interval := alertBaselineInterval
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			perCore, err := cpu.Percent(alertSampleWindow, true)
			if err != nil || len(perCore) == 0 {
				continue
			}
			maxCore, maxIdx := maxPerCore(perCore)

			if active != nil {
				active = checkRecovery(active, maxCore)
			} else {
				active, streak = checkTrigger(maxCore, maxIdx, streak)
			}

			want := alertBaselineInterval
			if active != nil {
				want = alertTrackInterval
			}
			if want != interval {
				interval = want
				ticker.Reset(interval)
			}
		}
	}()
}

// checkTrigger 巡检态：没有进行中的告警时，看这轮占用最高的那个核心有没有连续
// alertBreachStreak 次越过阈值。跨轮次的"连续"靠 streak 计数器维持，一旦某轮没
// 超阈值就清零——防的就是抖动，不是真的追踪某个特定核心连续超没超（哪个核心最高
// 每轮都可能不一样，这里只关心"有没有核心被打满"这个事实）。
func checkTrigger(maxCore float64, maxIdx int, streak int) (*activeAlert, int) {
	if maxCore < alertTriggerThreshold {
		return nil, 0
	}
	streak++
	if streak < alertBreachStreak {
		return nil, streak
	}
	return fireAlert(maxIdx, maxCore), 0
}

// checkRecovery 加密巡检态：看当前最高的核心占用是否已经退到恢复阈值以下。
func checkRecovery(active *activeAlert, maxCore float64) *activeAlert {
	if maxCore >= alertRecoverThreshold {
		return active // 还没退下去，继续盯
	}
	resolveAlert(active.id)
	return nil
}

func maxPerCore(perCore []float64) (best float64, idx int) {
	for i, v := range perCore {
		if v > best {
			best = v
			idx = i
		}
	}
	return
}

// fireAlert 落库 + 补采一份进程快照（供参考 + 顺带进常规历史）+ 推送通知。
func fireAlert(coreIdx int, coreCPU float64) *activeAlert {
	snapshotJSON := []byte("[]")
	if snap, err := filecore.CollectSysSnapshot(alertScanTopN); err == nil {
		sorted := append([]filecore.ProcessInfo(nil), snap.Processes...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].CPUPercent > sorted[j].CPUPercent })
		if b, err := json.Marshal(sorted); err == nil {
			snapshotJSON = b
		}
	}
	go recordDetailOnce() // 让告警时刻也进常规历史，进程页图表上能对上

	alert := model.ResourceAlert{
		TriggeredAt:  time.Now(),
		TriggerCore:  coreIdx,
		TriggerCPU:   coreCPU,
		TopProcesses: string(snapshotJSON),
		Status:       "active",
	}
	if db != nil {
		if err := db.Create(&alert).Error; err != nil {
			logger.Logger.Warn("资源告警落库失败")
		}
	}

	ws.NotifyAll(
		"资源告警",
		fmt.Sprintf("CPU 核心 #%d 占用达到 %.1f%%，已开始加密巡检", coreIdx, coreCPU),
		"warning",
	)

	return &activeAlert{id: alert.ID}
}

func resolveAlert(id uint64) {
	if db != nil && id > 0 {
		now := time.Now()
		if err := db.Model(&model.ResourceAlert{}).Where("id = ?", id).
			Updates(map[string]interface{}{"status": "resolved", "resolved_at": now}).Error; err != nil {
			logger.Logger.Warn("资源告警状态更新失败")
		}
	}
	ws.NotifyAll("资源告警已恢复", "CPU 占用已回落，告警解除", "info")
}

// alertOut 对外的告警行：TopProcesses 用 json.RawMessage 而不是 string，直接把
// 库里存的 JSON 数组原样嵌进响应体，前端拿到手就是数组，不用先反序列化一层字符串。
type alertOut struct {
	ID           uint64          `json:"id"`
	TriggeredAt  int64           `json:"triggered_at"` // unix 秒，跟其它监控接口的时间格式保持一致
	TriggerCore  int             `json:"trigger_core"`
	TriggerCPU   float64         `json:"trigger_cpu_percent"`
	TopProcesses json.RawMessage `json:"top_processes"`
	Status       string          `json:"status"`
	ResolvedAt   int64           `json:"resolved_at"` // 0 表示未恢复
}

// Alerts GET /v1/monitor/alerts?days=N&status=active|resolved —— 资源告警历史。
// status 不传则不过滤，按触发时间倒序（最新的在前，告警历史列表要的就是这个顺序）。
func Alerts(c *gin.Context) {
	if db == nil {
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": []alertOut{}})
		return
	}
	days := parseDaysParam(c)
	if days < 1 {
		days = 1
	}
	since := time.Now().AddDate(0, 0, -days)

	q := db.Model(&model.ResourceAlert{}).Where("triggered_at >= ?", since)
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}

	var rows []model.ResourceAlert
	if err := q.Order("triggered_at DESC").Limit(500).Find(&rows).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": []alertOut{}})
		return
	}

	out := make([]alertOut, 0, len(rows))
	for _, r := range rows {
		resolvedAt := int64(0)
		if r.ResolvedAt != nil {
			resolvedAt = r.ResolvedAt.Unix()
		}
		raw := json.RawMessage(r.TopProcesses)
		if !json.Valid(raw) {
			raw = json.RawMessage("[]")
		}
		out = append(out, alertOut{
			ID:           r.ID,
			TriggeredAt:  r.TriggeredAt.Unix(),
			TriggerCore:  r.TriggerCore,
			TriggerCPU:   r.TriggerCPU,
			TopProcesses: raw,
			Status:       r.Status,
			ResolvedAt:   resolvedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": out})
}
