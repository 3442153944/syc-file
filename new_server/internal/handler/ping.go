package handler

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"syc-file/config"
	"syc-file/internal/ws"
)

// 进程启动时刻，用于算 uptime。放在包级变量里，init 时取一次即可。
var startedAt = time.Now()

// 节点名：优先取 server.name，没配就退回主机名。
// 客户端（桌面端多节点灾备）靠它区分「我现在打到的是哪个入口」——两个 frp 节点
// 转发到同一台后端时节点名会一样，此时客户端只按连通性/延迟选路。
func nodeName() string {
	if n := config.Conf.Server.Name; n != "" {
		return n
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// HandlerPing 健康探测接口。
//
// 设计约束：**不碰数据库、不碰 Redis、不做任何 IO**。它被客户端低频轮询（桌面端默认
// 15 分钟一次，切换/失败时会突发并发探测所有节点），必须足够便宜，且在 DB/Redis 挂掉时
// 依然能回 200 —— 否则客户端会误判「整条链路不可用」而来回切节点。
//
// 返回的都是进程内就能拿到的信息，客户端据此判断：
//   - node/version：打到了哪个节点、版本是否一致
//   - server_time：配合本地时间算时钟偏移（同步冲突排查用）
//   - uptime：服务端是否刚重启过（重启后 WS 要重连）
//   - ws_conns：当前活跃 WS 连接数，粗略反映服务端负载
func HandlerPing() gin.HandlerFunc {
	return func(c *gin.Context) {
		now := time.Now()
		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "pong",
			"data": gin.H{
				"status":      "ok",
				"node":        nodeName(),
				"version":     config.Version,
				"server_time": now.UnixMilli(),
				"uptime":      int64(now.Sub(startedAt).Seconds()),
				"ws_conns":    ws.GetHub().ConnectionCount(),
				"goroutines":  runtime.NumGoroutine(),
			},
		})
	}
}
