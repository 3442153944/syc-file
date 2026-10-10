// speedtest.go
// 客户端多路径上传前的专用测速端点。设计约束与 ping 一致：不碰数据库、不碰 Redis，
// 数据即抛——上传方向收多少丢多少，下载方向吐全局共享的零缓冲。
//
// 为什么不用 /v1/ping 凑数：RTT 必须带真实请求体/响应体的收发开销才有调度意义，
// 且多路径上传要求校验「所有节点入口指向同一后端」——ping 的语义是健康探测，
// 测速的语义是带宽采样，分开后各自可以独立演进（比如测速以后加并发采样档）。
package handler

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// 测速默认/最大样本。8MiB 在 3Mbps 链路上约 21s，客户端选档时注意；
	// RTT 微样本用 bytes=0（立即空响应，测握手+首字节）。
	speedtestDefaultBytes = 1 << 20
	speedtestMaxBytes     = 8 << 20
)

// 全局共享的零缓冲：测速下载方向专用，进程生命周期内复用，绝不透出真实数据。
var speedtestZeroBuf = make([]byte, 256<<10)

func parseSpeedtestBytes(c *gin.Context) (int, bool) {
	raw := c.Query("bytes")
	if raw == "" {
		return speedtestDefaultBytes, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > speedtestMaxBytes {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "bytes 须为 0~" + strconv.Itoa(speedtestMaxBytes),
		})
		return 0, false
	}
	return n, true
}

func speedtestEnvelope(node string, n int, ms int64) gin.H {
	return gin.H{
		"code":    200,
		"message": "ok",
		"data": gin.H{
			"node":      node,
			"bytes":     n,
			"server_ms": ms,
		},
	}
}

// HandlerSpeedtestUpload 客户端 POST N 字节垃圾数据，服务端即读即弃。
// 客户端用自己的发送耗时算上行吞吐，server_ms 供交叉校验。
func HandlerSpeedtestUpload() gin.HandlerFunc {
	return func(c *gin.Context) {
		n, ok := parseSpeedtestBytes(c)
		if !ok {
			return
		}
		start := time.Now()
		if _, err := io.Copy(io.Discard, io.LimitReader(c.Request.Body, int64(n))); err != nil {
			// 客户端中途断连等：不需要回什么，连接已经没了
			return
		}
		c.JSON(http.StatusOK, speedtestEnvelope(nodeName(), n, time.Since(start).Milliseconds()))
	}
}

// HandlerSpeedtestDownload 服务端写 N 字节零数据。客户端用自己的接收耗时算下行吞吐。
func HandlerSpeedtestDownload() gin.HandlerFunc {
	return func(c *gin.Context) {
		n, ok := parseSpeedtestBytes(c)
		if !ok {
			return
		}
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Length", strconv.Itoa(n))
		written := 0
		for written < n {
			m := n - written
			if m > len(speedtestZeroBuf) {
				m = len(speedtestZeroBuf)
			}
			k, err := c.Writer.Write(speedtestZeroBuf[:m])
			written += k
			if err != nil {
				return
			}
		}
		// 下行吞吐由客户端按接收耗时算；Content-Length 已声明，无需额外字段
	}
}
