package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/filecore"
)

// 进程 / 端口明细：独立于 history.go 的系统级 CPU/内存趋势（那个是 1 分钟一次、
// 只有两个数），这里是按 config.monitor 配置的间隔（默认 30s）采一次"进程 Top-N
// + 监听端口 + 端口连接数统计"，Rust 侧用 sysinfo + netstat2 采（Go 采太慢，
// 见 file_lib/src/sys_info.rs），存储/归档策略照抄 history.go 那一套：
// Redis 按天分 key 存热数据（8 天 TTL），每天再批量归档进 MySQL 长期保存。
const (
	sysDetailTTL          = 8 * 24 * time.Hour
	defaultDetailInterval = 30 * time.Second
	defaultTopN           = 20
	// 秒级采集：前端进程页选了 5/15/30 分钟这类短窗口时才需要的分辨率。
	// 只在有人要的时候临时提速，没人要就退回 defaultDetailInterval，不能常驻——
	// 8 天 TTL 的 Redis 热存按 1s 一帧存不起，MySQL 归档表也会被灌爆。
	boostDetailInterval = 1 * time.Second
)

// sysDetailFrame 一次采集的完整快照，Redis 里按天存的就是这个结构的 JSON 数组。
type sysDetailFrame struct {
	Time            int64                    `json:"t"` // unix 秒
	Processes       []filecore.ProcessInfo   `json:"processes"`
	ListeningPorts  []filecore.ListeningPort `json:"listening_ports"`
	PortConnections []filecore.PortConnCount `json:"port_connections"`
	NumCpus         uint32                   `json:"num_cpus"`
}

func sysDetailKey(t time.Time) string {
	return "monitor:sysdetail:" + t.UTC().Format("20060102")
}

func detailInterval() time.Duration {
	s := config.Conf.Monitor.SysDetailIntervalSeconds
	if s <= 0 {
		return defaultDetailInterval
	}
	return time.Duration(s) * time.Second
}

func detailTopN() int {
	n := config.Conf.Monitor.ProcessTopN
	if n <= 0 {
		return defaultTopN
	}
	return n
}

var (
	// detailBoosted：是否至少有一个订阅者要秒级采集，由 broadcaster 的订阅集变化驱动
	// （见 broadcaster.go 的 syncDetailBoost）。
	detailBoosted atomic.Bool
	// 采集 goroutine 自己的 ticker，SetDetailBoost 需要跨 goroutine 立即 reset 它，
	// 不能等到下一次已经排定的 tick 才生效——那样最坏要等一个默认间隔（30s）才提速，
	// 短窗口视图刚打开时等于白等。指针读写和 Reset 调用都过这把锁。
	detailTickerMu sync.Mutex
	detailTicker   *time.Ticker
)

func effectiveDetailInterval() time.Duration {
	if detailBoosted.Load() {
		return boostDetailInterval
	}
	return detailInterval()
}

// SetDetailBoost 打开/关闭秒级采集。状态没变就什么都不做（避免每次订阅集小抖动
// 都去折腾 ticker）；状态真的翻转时立即重置 ticker 到新间隔，打开时还补采一帧，
// 不让前端刚打开秒级视图就先干等一个间隔。
func SetDetailBoost(on bool) {
	if detailBoosted.Swap(on) == on {
		return
	}
	detailTickerMu.Lock()
	t := detailTicker
	detailTickerMu.Unlock()
	if t != nil {
		t.Reset(effectiveDetailInterval())
	}
	if on {
		go recordDetailOnce()
	}
}

// StartSysDetailRecorder 后台常驻采集，独立于 WS broadcaster 的懒启动，
// 也独立于 StartHistoryRecorder 的 1 分钟节奏——间隔从 config.monitor 读，
// 或在有人要秒级视图时临时顶到 boostDetailInterval（见 SetDetailBoost）。
func StartSysDetailRecorder() {
	recordDetailOnce()
	go func() {
		interval := effectiveDetailInterval()
		ticker := time.NewTicker(interval)
		detailTickerMu.Lock()
		detailTicker = ticker
		detailTickerMu.Unlock()
		defer ticker.Stop()
		for range ticker.C {
			recordDetailOnce()
			// 配置热改 / 秒级开关都可能已经让目标间隔变了，对齐一下
			if ni := effectiveDetailInterval(); ni != interval {
				interval = ni
				ticker.Reset(interval)
			}
		}
	}()
}

func recordDetailOnce() {
	if redisClient == nil {
		return
	}
	snap, err := filecore.CollectSysSnapshot(detailTopN())
	if err != nil {
		return
	}
	frame := sysDetailFrame{
		Time:            time.Now().Unix(),
		Processes:       snap.Processes,
		ListeningPorts:  snap.ListeningPorts,
		PortConnections: snap.PortConnections,
		NumCpus:         snap.NumCpus,
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return
	}
	ctx := context.Background()
	key := sysDetailKey(time.Now())
	pipe := redisClient.Pipeline()
	pipe.RPush(ctx, key, data)
	pipe.Expire(ctx, key, sysDetailTTL)
	_, _ = pipe.Exec(ctx)
}

// StartSysDetailArchiver 每天把前一天的进程/端口明细批量归档进 MySQL。
// 和 StartDailyArchiver（系统级）错开 5 分钟跑，避免同时段一起打 DB。
func StartSysDetailArchiver() {
	archiveSysDetailYesterdayIfMissing()
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 10, 0, 0, time.UTC)
			time.Sleep(next.Sub(now))
			archiveSysDetailYesterdayIfMissing()
		}
	}()
}

func archiveSysDetailYesterdayIfMissing() {
	if db == nil || redisClient == nil {
		return
	}
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	dayStart := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.AddDate(0, 0, 1)

	var count int64
	if err := db.Model(&model.ProcessHistory{}).
		Where("time >= ? AND time < ?", dayStart, dayEnd).
		Count(&count).Error; err != nil || count > 0 {
		return
	}

	ctx := context.Background()
	vals, err := redisClient.LRange(ctx, sysDetailKey(dayStart), 0, -1).Result()
	if err != nil || len(vals) == 0 {
		return
	}

	var procRows []model.ProcessHistory
	var listenRows []model.ListeningPortHistory
	var connRows []model.PortConnHistory

	for _, v := range vals {
		var frame sysDetailFrame
		if json.Unmarshal([]byte(v), &frame) != nil {
			continue
		}
		t := time.Unix(frame.Time, 0).UTC()
		for _, p := range frame.Processes {
			procRows = append(procRows, model.ProcessHistory{
				Time:           t,
				PID:            p.PID,
				Name:           p.Name,
				CPUPercent:     float64(p.CPUPercent),
				MemBytes:       p.MemBytes,
				MemPercent:     float64(p.MemPercent),
				DiskReadBytes:  p.DiskReadBytes,
				DiskWriteBytes: p.DiskWriteBytes,
				Connections:    p.Connections,
				Score:          float64(p.Score),
			})
		}
		for _, lp := range frame.ListeningPorts {
			listenRows = append(listenRows, model.ListeningPortHistory{
				Time:        t,
				Port:        lp.Port,
				Protocol:    lp.Protocol,
				PID:         lp.PID,
				ProcessName: lp.ProcessName,
			})
		}
		for _, pc := range frame.PortConnections {
			connRows = append(connRows, model.PortConnHistory{
				Time:        t,
				Port:        pc.Port,
				Protocol:    pc.Protocol,
				Connections: pc.Connections,
			})
		}
	}

	if len(procRows) > 0 {
		_ = db.CreateInBatches(procRows, 500).Error
	}
	if len(listenRows) > 0 {
		_ = db.CreateInBatches(listenRows, 500).Error
	}
	if len(connRows) > 0 {
		_ = db.CreateInBatches(connRows, 500).Error
	}
}

// framesInRange 取 [days] 天内的原始采集帧：7 天内查 Redis，更早的从 MySQL 三张
// 归档表按时间重新拼回 sysDetailFrame（同一 Time 下的行合并成一帧）。
func framesInRange(ctx context.Context, days int) []sysDetailFrame {
	if days < 1 {
		days = 1
	}
	if days > queryMaxDays {
		days = queryMaxDays
	}
	now := time.Now().UTC()
	var frames []sysDetailFrame

	if days > historyMaxDays {
		from := now.AddDate(0, 0, -days)
		to := now.AddDate(0, 0, -historyMaxDays)
		frames = append(frames, archivedFramesInRange(from, to)...)
	}

	if redisClient != nil {
		redisDays := days
		if redisDays > historyMaxDays {
			redisDays = historyMaxDays
		}
		for i := redisDays - 1; i >= 0; i-- {
			vals, err := redisClient.LRange(ctx, sysDetailKey(now.AddDate(0, 0, -i)), 0, -1).Result()
			if err != nil {
				continue
			}
			for _, v := range vals {
				var f sysDetailFrame
				if json.Unmarshal([]byte(v), &f) == nil {
					frames = append(frames, f)
				}
			}
		}
	}

	sort.Slice(frames, func(i, j int) bool { return frames[i].Time < frames[j].Time })
	return frames
}

func archivedFramesInRange(from, to time.Time) []sysDetailFrame {
	if db == nil {
		return nil
	}
	byTime := make(map[int64]*sysDetailFrame)
	order := func(t time.Time) int64 { return t.Unix() }

	var procs []model.ProcessHistory
	if db.WithContext(context.Background()).
		Where("time >= ? AND time < ?", from, to).Order("time ASC").Find(&procs).Error == nil {
		for _, p := range procs {
			key := order(p.Time)
			f, ok := byTime[key]
			if !ok {
				f = &sysDetailFrame{Time: key}
				byTime[key] = f
			}
			f.Processes = append(f.Processes, filecore.ProcessInfo{
				PID: p.PID, Name: p.Name, CPUPercent: float32(p.CPUPercent),
				MemBytes: p.MemBytes, MemPercent: float32(p.MemPercent),
				DiskReadBytes: p.DiskReadBytes, DiskWriteBytes: p.DiskWriteBytes,
				Connections: p.Connections, Score: float32(p.Score),
			})
		}
	}

	var ports []model.ListeningPortHistory
	if db.WithContext(context.Background()).
		Where("time >= ? AND time < ?", from, to).Order("time ASC").Find(&ports).Error == nil {
		for _, lp := range ports {
			key := order(lp.Time)
			f, ok := byTime[key]
			if !ok {
				f = &sysDetailFrame{Time: key}
				byTime[key] = f
			}
			f.ListeningPorts = append(f.ListeningPorts, filecore.ListeningPort{
				Port: lp.Port, Protocol: lp.Protocol, PID: lp.PID, ProcessName: lp.ProcessName,
			})
		}
	}

	var conns []model.PortConnHistory
	if db.WithContext(context.Background()).
		Where("time >= ? AND time < ?", from, to).Order("time ASC").Find(&conns).Error == nil {
		for _, pc := range conns {
			key := order(pc.Time)
			f, ok := byTime[key]
			if !ok {
				f = &sysDetailFrame{Time: key}
				byTime[key] = f
			}
			f.PortConnections = append(f.PortConnections, filecore.PortConnCount{
				Port: pc.Port, Protocol: pc.Protocol, Connections: pc.Connections,
			})
		}
	}

	frames := make([]sysDetailFrame, 0, len(byTime))
	for _, f := range byTime {
		frames = append(frames, *f)
	}
	return frames
}

func parseDaysParam(c *gin.Context) int {
	days := 1
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	return days
}

// latestSysDetailFrame 只要最新一帧（Redis 当天 key 的最后一个元素，O(1)）。
// 前端概览页轮询刷新"当前快照"用这个，不用为了刷一眼最新数据把一整天的
// 明细都重新拉一遍。
func latestSysDetailFrame(ctx context.Context) *sysDetailFrame {
	if redisClient == nil {
		return nil
	}
	val, err := redisClient.LIndex(ctx, sysDetailKey(time.Now()), -1).Result()
	if err != nil {
		return nil
	}
	var f sysDetailFrame
	if json.Unmarshal([]byte(val), &f) != nil {
		return nil
	}
	return &f
}

// Processes GET /v1/monitor/processes?days=N&name=xxx | ?latest=1 —— 进程 Top-N
// 采集历史，原始帧（每帧一个时间点 + 当时的 Top-N 进程列表）。
//
// 概览首次加载：不传 name，days 给小值（比如 1），前端自己按需要归并/裁剪。
// 概览轮询刷新：传 latest=1，只回最新一帧，O(1)，不用每次刷新都重拉一整天。
// 详情：传 name 按进程名过滤（PID 会在进程重启后变，跨时间范围看同一个
// 进程只能按名字认），days 可以给大一些，服务端只回这一个进程的数据。
func Processes(c *gin.Context) {
	if c.Query("latest") != "" {
		out := []gin.H{}
		if f := latestSysDetailFrame(c.Request.Context()); f != nil {
			out = append(out, gin.H{"t": f.Time, "processes": f.Processes, "num_cpus": f.NumCpus})
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": out})
		return
	}
	days := parseDaysParam(c)
	name := c.Query("name")
	frames := framesInRange(c.Request.Context(), days)
	type point struct {
		Time      int64                  `json:"t"`
		Processes []filecore.ProcessInfo `json:"processes"`
		NumCpus   uint32                 `json:"num_cpus"`
	}
	out := make([]point, 0, len(frames))
	for _, f := range frames {
		procs := f.Processes
		if name != "" {
			filtered := make([]filecore.ProcessInfo, 0, 1)
			for _, p := range procs {
				if p.Name == name {
					filtered = append(filtered, p)
				}
			}
			procs = filtered
		}
		if len(procs) == 0 {
			continue
		}
		out = append(out, point{Time: f.Time, Processes: procs, NumCpus: f.NumCpus})
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": out})
}

// Ports GET /v1/monitor/ports?days=N&port=N | ?latest=1 —— 监听端口 + 端口连接数
// 统计历史，原始帧。概览轮询刷新用 latest=1（O(1)，见 Processes 的同款注释）；
// 详情用 port 只回该端口的数据（跨协议，tcp/udp 都带上）。
func Ports(c *gin.Context) {
	if c.Query("latest") != "" {
		out := []gin.H{}
		if f := latestSysDetailFrame(c.Request.Context()); f != nil {
			out = append(out, gin.H{"t": f.Time, "listening_ports": f.ListeningPorts, "port_connections": f.PortConnections})
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": out})
		return
	}
	days := parseDaysParam(c)
	var portFilter int
	if v := c.Query("port"); v != "" {
		portFilter, _ = strconv.Atoi(v)
	}
	frames := framesInRange(c.Request.Context(), days)
	type point struct {
		Time            int64                    `json:"t"`
		ListeningPorts  []filecore.ListeningPort `json:"listening_ports"`
		PortConnections []filecore.PortConnCount `json:"port_connections"`
	}
	out := make([]point, 0, len(frames))
	for _, f := range frames {
		listening, conns := f.ListeningPorts, f.PortConnections
		if portFilter > 0 {
			fl := make([]filecore.ListeningPort, 0, 1)
			for _, lp := range listening {
				if int(lp.Port) == portFilter {
					fl = append(fl, lp)
				}
			}
			listening = fl
			fc := make([]filecore.PortConnCount, 0, 1)
			for _, pc := range conns {
				if int(pc.Port) == portFilter {
					fc = append(fc, pc)
				}
			}
			conns = fc
		}
		if len(listening) == 0 && len(conns) == 0 {
			continue
		}
		out = append(out, point{Time: f.Time, ListeningPorts: listening, PortConnections: conns})
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": out})
}
