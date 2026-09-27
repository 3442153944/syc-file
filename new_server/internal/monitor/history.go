package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/internal/ws"
)

// 历史记录：给桌面端仪表盘画趋势图用。
//
// 与 broadcaster.go 的懒启动是两回事——broadcaster 只在有人开着监控页时才采样，
// 没人看就停机；历史记录必须常驻后台采，不然没人开监控页的那段时间图表就是个洞。
//
// 两层存储：
//   - Redis 按天分 key（monitor:history:20060102），每个 key 一个 List，TTL 8 天
//     （比 7 天的对外保留窗口多留一天余量）——这是热路径，最近一周走这里，快。
//   - MySQL monitor_history 表长期保存：每天由 StartDailyArchiver 把前一天的
//     Redis 数据整体搬过去一份，查询超过一周的范围时从这里补。Redis 到期自然
//     回收，不用另外清理；MySQL 这份才是"一直能查"的依据。
const (
	historyInterval = 1 * time.Minute
	historyTTL      = 8 * 24 * time.Hour
	historyMaxDays  = 7   // 这个范围内直接走 Redis
	queryMaxDays    = 365 // 单次查询允许的最大跨度，避免无限制扫表
)

// HistoryPoint 图表用得着的字段，比实时快照精简（不含每核占用/磁盘/主机信息）。
type HistoryPoint struct {
	Time              int64   `json:"t"` // unix 秒
	CPUPercent        float64 `json:"cpu"`
	MemPercent        float64 `json:"mem"`
	MemUsed           uint64  `json:"mem_used"`
	SendRate          float64 `json:"send_rate"`
	RecvRate          float64 `json:"recv_rate"`
	OnlineDevices     int     `json:"online_devices"`
	ActiveConnections int     `json:"active_connections"`
}

var (
	redisClient    *redis.Client
	db             *gorm.DB
	historySampler NetSampler // 独立基线，不与 HTTP/WS broadcaster 的采样器互相打架
)

// Init 注入 Redis / DB 客户端。在 main 里两者都初始化好之后、
// StartHistoryRecorder / StartDailyArchiver 之前调用。
func Init(rdb *redis.Client, gormDB *gorm.DB) {
	redisClient = rdb
	db = gormDB
}

func historyKey(t time.Time) string {
	return "monitor:history:" + t.UTC().Format("20060102")
}

// StartHistoryRecorder 后台常驻采样，独立于 WS broadcaster 的懒启动。
// 随进程退出而结束，不需要手动停止。
func StartHistoryRecorder() {
	recordOnce() // 启动立刻记一帧，不等第一个 tick
	go func() {
		ticker := time.NewTicker(historyInterval)
		defer ticker.Stop()
		for range ticker.C {
			recordOnce()
		}
	}()
}

func recordOnce() {
	if redisClient == nil {
		return
	}
	c := collectCPU()
	m := collectMem()
	_, _, sendRate, recvRate, _ := historySampler.Sample()
	hub := ws.GetHub()
	point := HistoryPoint{
		Time:              time.Now().Unix(),
		CPUPercent:        c.UsedPercent,
		MemPercent:        m.UsedPercent,
		MemUsed:           m.Used,
		SendRate:          sendRate,
		RecvRate:          recvRate,
		OnlineDevices:     len(hub.OnlineDeviceIDs()),
		ActiveConnections: hub.ConnectionCount(),
	}
	data, err := json.Marshal(point)
	if err != nil {
		return
	}
	ctx := context.Background()
	key := historyKey(time.Now())
	pipe := redisClient.Pipeline()
	pipe.RPush(ctx, key, data)
	pipe.Expire(ctx, key, historyTTL)
	_, _ = pipe.Exec(ctx)
}

// StartDailyArchiver 每天 UTC 零点过后，把前一天的 Redis 数据整批写入 MySQL 长期保存。
// 启动时先补一次——服务器可能正好跨天重启，错过了那次零点触发。
func StartDailyArchiver() {
	archiveYesterdayIfMissing()
	go func() {
		for {
			now := time.Now().UTC()
			// 零点后 5 分钟再跑，让当天最后一帧先落好 Redis
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 5, 0, 0, time.UTC)
			time.Sleep(next.Sub(now))
			archiveYesterdayIfMissing()
		}
	}()
}

// archiveYesterdayIfMissing 幂等：MySQL 里已有前一天的数据就跳过，
// 避免正常零点触发和启动补跑重复写入。
func archiveYesterdayIfMissing() {
	if db == nil || redisClient == nil {
		return
	}
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	dayStart := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)

	var count int64
	if err := db.Model(&model.MonitorHistory{}).
		Where("time >= ? AND time < ?", dayStart, dayEnd).
		Count(&count).Error; err != nil || count > 0 {
		return
	}

	ctx := context.Background()
	vals, err := redisClient.LRange(ctx, historyKey(dayStart), 0, -1).Result()
	if err != nil || len(vals) == 0 {
		return
	}
	rows := make([]model.MonitorHistory, 0, len(vals))
	for _, v := range vals {
		var p HistoryPoint
		if json.Unmarshal([]byte(v), &p) != nil {
			continue
		}
		rows = append(rows, model.MonitorHistory{
			Time:              time.Unix(p.Time, 0).UTC(),
			CPUPercent:        p.CPUPercent,
			MemPercent:        p.MemPercent,
			MemUsed:           p.MemUsed,
			SendRate:          p.SendRate,
			RecvRate:          p.RecvRate,
			OnlineDevices:     p.OnlineDevices,
			ActiveConnections: p.ActiveConnections,
		})
	}
	if len(rows) == 0 {
		return
	}
	_ = db.CreateInBatches(rows, 500).Error
}

// queryArchive 从 MySQL 长期归档表补 [from, to) 范围的历史点（早于 Redis 保留窗口的部分）。
func queryArchive(ctx context.Context, from, to time.Time) []HistoryPoint {
	if db == nil {
		return nil
	}
	var rows []model.MonitorHistory
	if err := db.WithContext(ctx).
		Where("time >= ? AND time < ?", from, to).
		Order("time ASC").
		Find(&rows).Error; err != nil {
		return nil
	}
	points := make([]HistoryPoint, 0, len(rows))
	for _, r := range rows {
		points = append(points, HistoryPoint{
			Time:              r.Time.Unix(),
			CPUPercent:        r.CPUPercent,
			MemPercent:        r.MemPercent,
			MemUsed:           r.MemUsed,
			SendRate:          r.SendRate,
			RecvRate:          r.RecvRate,
			OnlineDevices:     r.OnlineDevices,
			ActiveConnections: r.ActiveConnections,
		})
	}
	return points
}

// History GET /v1/monitor/history?days=N —— 最近 N 天的采样点，按时间升序返回画图用。
// N 缺省 1，夹到 [1, queryMaxDays]；[1,historyMaxDays] 内走 Redis，
// 更早的部分从 MySQL 归档表补（见 StartDailyArchiver）。
//
// 图表打开着之后的实时增量不走这个接口轮询——前端复用已有的 WS 监控推送
// （useMonitor，同一条连接、同一份数据，Dashboard 卡片缩略图也是这么接的），
// 没必要为同一份数据再单独开一条轮询通道。
func History(c *gin.Context) {
	days := 1
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	if days < 1 {
		days = 1
	}
	if days > queryMaxDays {
		days = queryMaxDays
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	var points []HistoryPoint

	if days > historyMaxDays {
		from := now.AddDate(0, 0, -days)
		to := now.AddDate(0, 0, -historyMaxDays)
		points = append(points, queryArchive(ctx, from, to)...)
	}

	if redisClient != nil {
		redisDays := days
		if redisDays > historyMaxDays {
			redisDays = historyMaxDays
		}
		for i := redisDays - 1; i >= 0; i-- {
			vals, err := redisClient.LRange(ctx, historyKey(now.AddDate(0, 0, -i)), 0, -1).Result()
			if err != nil {
				continue
			}
			for _, v := range vals {
				var p HistoryPoint
				if json.Unmarshal([]byte(v), &p) == nil {
					points = append(points, p)
				}
			}
		}
	}

	sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": points})
}
