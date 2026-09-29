package monitor

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"syc-file/internal/ws"
)

// 监控数据的实时推送。
//
// ── 为什么从 HTTP 轮询改成 WS 推送 ────────────────────────────
// 监控是「服务端定时产出、多端订阅」的场景，用请求-响应模型是错配：
//   1. 每次轮询都要重新走一遍鉴权 + 建响应 + 断连，开销远大于往已建 WS 上推一帧；
//   2. 实时性被轮询间隔卡死——间隔多长，最坏延迟就多长；
//   3. **采样本身有成本**：CPU 使用率要阻塞采样 cpuSampleWindow（默认 300ms）。
//      轮询模型下 N 个客户端各采一次 = N×300ms；推送模型下一次采样喂所有订阅者。
//
// ── 懒启动 ────────────────────────────────────────────────
// 没人看监控页时不该空转采样。只有订阅集非空才跑 ticker，集合清空即停。
//
// ── 死连接自愈 ────────────────────────────────────────────
// 不挂断开钩子：某订阅者掉线后，下一 tick 向它 SendToConn 会失败，就地从订阅集剔除。
// 最多多采一两 tick，换来的是零耦合（不依赖 Hub 的 disconnect 回调）。

const (
	// 推送间隔范围。太快徒增采样开销，太慢失去实时意义。
	minInterval     = 1 * time.Second
	maxInterval     = 10 * time.Second
	defaultInterval = 2 * time.Second
)

// subInfo 单个订阅者：它想要的推送间隔，以及要不要进程秒级采集。
type subInfo struct {
	interval    time.Duration
	detailBoost bool
}

type broadcaster struct {
	mu      sync.Mutex
	subs    map[string]subInfo // connID → 订阅信息
	sampler NetSampler         // WS 专用网速基线，与 HTTP 的 httpNetSampler 分开
	running bool
	stop    chan struct{}
}

var globalBroadcaster = &broadcaster{subs: make(map[string]subInfo)}

// Subscribe 加入订阅集，按需拉起推送循环。interval 会被夹到 [min,max]。
// detailBoost=true 表示这个订阅者正看着一个短时间窗口的进程视图，需要后端把
// 进程/端口明细的采集频率临时顶到秒级（见 sys_detail.go 的 SetDetailBoost）。
// 同一个 connID 重复调用会覆盖旧的订阅信息——前端切换时间区间预设时就是这么
// 用的：不退订重订，直接带着新的 detailBoost 再 Subscribe 一次。
func (b *broadcaster) Subscribe(connID string, interval time.Duration, detailBoost bool) {
	if interval < minInterval {
		interval = defaultInterval
	}
	if interval > maxInterval {
		interval = maxInterval
	}
	b.mu.Lock()
	b.subs[connID] = subInfo{interval: interval, detailBoost: detailBoost}
	needStart := !b.running
	if needStart {
		b.running = true
		b.stop = make(chan struct{})
	}
	stop := b.stop
	b.mu.Unlock()
	b.syncDetailBoost()

	if needStart {
		go b.loop(stop)
	}
	// 新订阅者立刻先喂一帧，不让它干等一个 tick（首屏体验）
	go b.pushOne(connID)
}

// Unsubscribe 退订。订阅集清空后由 loop 自行发现并停机。
func (b *broadcaster) Unsubscribe(connID string) {
	b.mu.Lock()
	delete(b.subs, connID)
	b.mu.Unlock()
	b.syncDetailBoost()
}

// syncDetailBoost 按当前订阅集里有没有人要秒级视图，同步全局采集开关。
// 每次订阅集变化（新增/退订/死连接剔除）都要调一次。
func (b *broadcaster) syncDetailBoost() {
	b.mu.Lock()
	want := false
	for _, s := range b.subs {
		if s.detailBoost {
			want = true
			break
		}
	}
	b.mu.Unlock()
	SetDetailBoost(want)
}

// loop 采样 + 推送。间隔取所有订阅者请求的最小值（最挑剔的那个说了算）。
func (b *broadcaster) loop(stop chan struct{}) {
	interval := b.currentInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if b.tick() {
				return // 订阅集空了，停机
			}
			// 间隔可能因订阅者增减而变，重置 ticker
			if ni := b.currentInterval(); ni != interval {
				interval = ni
				ticker.Reset(interval)
			}
		}
	}
}

// tick 采一次并推给全体订阅者，剔除掉线的。返回 true 表示订阅集已空、应停机。
func (b *broadcaster) tick() bool {
	b.mu.Lock()
	if len(b.subs) == 0 {
		b.running = false
		b.mu.Unlock()
		return true
	}
	conns := make([]string, 0, len(b.subs))
	for id := range b.subs {
		conns = append(conns, id)
	}
	// 采样在锁外做（含 300ms 阻塞），先把连接列表拷出来
	b.mu.Unlock()

	payload := b.buildPayload()

	var dead []string
	for _, id := range conns {
		if err := ws.SendToConn(id, ws.MessageTypeMonitor, payload); err != nil {
			dead = append(dead, id)
		}
	}
	if len(dead) > 0 {
		b.mu.Lock()
		for _, id := range dead {
			delete(b.subs, id)
		}
		empty := len(b.subs) == 0
		if empty {
			b.running = false
		}
		b.mu.Unlock()
		// 掉线剔除也可能让"还有没有人要秒级"这个结论变化，同步一下，
		// 不然一个开着秒级视图的客户端悄悄掉线，采集会一直空转在高频挡位。
		b.syncDetailBoost()
		return empty
	}
	return false
}

// pushOne 给单个订阅者立刻推一帧（新订阅时用）。
func (b *broadcaster) pushOne(connID string) {
	b.mu.Lock()
	_, ok := b.subs[connID]
	b.mu.Unlock()
	if !ok {
		return
	}
	if err := ws.SendToConn(connID, ws.MessageTypeMonitor, b.buildPayload()); err != nil {
		b.Unsubscribe(connID)
	}
}

// buildPayload 组装一帧监控数据。system/network 每次都实时采；进程/端口明细
// 不在这里现采（那份采集本身较重，独立按 config.monitor 的间隔跑，见
// sys_detail.go 的 StartSysDetailRecorder）——这里只是把它"最近一次采到的结果"
// （Redis 里的最后一帧，O(1) 读）捎带着一起推下去，桌面端复用同一条 WS 连接
// 就能拿到进程/端口数据，不用另外发 HTTP 轮询、多一份连接建立开销。
// 没采集到时（Redis 未就绪等）这两个字段就是 null，前端按"暂无数据"处理。
func (b *broadcaster) buildPayload() gin.H {
	payload := gin.H{
		"system":  SystemSnapshot(),
		"network": NetworkSnapshot(&b.sampler),
	}
	if f := latestSysDetailFrame(context.Background()); f != nil {
		payload["processes"] = f.Processes
		payload["listening_ports"] = f.ListeningPorts
		payload["port_connections"] = f.PortConnections
	}
	return payload
}

func (b *broadcaster) currentInterval() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	min := maxInterval
	for _, s := range b.subs {
		if s.interval < min {
			min = s.interval
		}
	}
	return min
}

// HandleMonitorMessage 处理客户端上行的 monitor 消息：subscribe / unsubscribe。
// 由 InitBroadcaster 注册到 Hub。
//
// 上行形状：{type:"monitor", content:{event:"subscribe"|"unsubscribe", interval:2, detail_boost:false}}
// detail_boost：前端进程页选了短时间窗口（比如最近 5/15/30 分钟）时置 true，
// 服务端据此把 sys_detail 采集临时顶到秒级，见 SetDetailBoost。
func HandleMonitorMessage(conn *ws.Connection, msg *ws.Message) {
	var content struct {
		Event       string `json:"event"`
		Interval    int    `json:"interval"` // 秒
		DetailBoost bool   `json:"detail_boost"`
	}
	if err := json.Unmarshal(msg.Content, &content); err != nil {
		return
	}
	switch content.Event {
	case "subscribe":
		globalBroadcaster.Subscribe(conn.ID, time.Duration(content.Interval)*time.Second, content.DetailBoost)
	case "unsubscribe":
		globalBroadcaster.Unsubscribe(conn.ID)
	}
}

// InitBroadcaster 注册 monitor WS 处理器。在 ws.InitWS 之后、main 中调用
// （放在 monitor 包里注册，避免 ws → monitor 的反向依赖）。
func InitBroadcaster() {
	ws.GetHub().RegisterHandler(ws.MessageTypeMonitor, HandleMonitorMessage)
}
