package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"syc-file/pkg/logger"
)

func ZapLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		method := c.Request.Method

		// ======== Debug 模式：捕获请求体 ========
		var requestBody string
		if logger.IsDebug() {
			if c.Request.Body != nil {
				bodyBytes, err := io.ReadAll(c.Request.Body)
				if err == nil {
					requestBody = string(bodyBytes)
					// 重新放回 Body，让后续 handler 能正常读取
					c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				}
				// 截断过长的请求体
				if len(requestBody) > 4096 {
					requestBody = requestBody[:4096] + "...(truncated)"
				}
			}
		}

		// ======== Debug 模式：包装 ResponseWriter 以捕获响应体 ========
		var respWriter *responseBodyWriter
		if logger.IsDebug() {
			respWriter = &responseBodyWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
			c.Writer = respWriter
		}

		c.Next()

		// ======== 收集响应信息 ========
		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		responseSize := c.Writer.Size()

		// ======== Debug 模式：输出完整请求/响应 ========
		if logger.IsDebug() {
			fields := []zap.Field{
				zap.String("method", method),
				zap.String("path", path),
				zap.String("query", query),
				zap.String("ip", clientIP),
				zap.Int("status", status),
				zap.Int("resp_size", responseSize),
				zap.Duration("latency", latency),
			}

			if requestBody != "" {
				fields = append(fields, zap.String("req_body", requestBody))
			}

			if respWriter != nil && respWriter.body.Len() > 0 {
				respBody := respWriter.body.String()
				if len(respBody) > 4096 {
					respBody = respBody[:4096] + "...(truncated)"
				}
				fields = append(fields, zap.String("resp_body", respBody))
			}

			switch {
			case status >= 500:
				logger.Logger.Error("HTTP请求(debug)", fields...)
			case status >= 400:
				logger.Logger.Warn("HTTP请求(debug)", fields...)
			default:
				logger.Logger.Info("HTTP请求(debug)", fields...)
			}
			return
		}

		// ======== Info 模式：请求/响应摘要 ========
		if logger.IsLevel(zapcore.InfoLevel) {
			fields := []zap.Field{
				zap.Int("status", status),
				zap.String("method", method),
				zap.String("path", path),
				zap.String("query", query),
				zap.String("ip", clientIP),
				zap.Duration("latency", latency),
				zap.String("user-agent", c.Request.UserAgent()),
			}

			if errs := c.Errors.ByType(gin.ErrorTypePrivate).String(); errs != "" {
				fields = append(fields, zap.String("errors", errs))
			}

			switch {
			case status >= 500:
				logger.Logger.Error("HTTP请求", fields...)
			case status >= 400:
				logger.Logger.Warn("HTTP请求", fields...)
			default:
				logger.Logger.Info("HTTP请求", fields...)
			}
			return
		}

		// ======== 其他模式（warn / error / panic）：仅记录错误 ========
		if status >= 400 {
			logger.Logger.Warn("HTTP请求",
				zap.Int("status", status),
				zap.String("method", method),
				zap.String("path", path),
				zap.Duration("latency", latency),
			)
		}
	}
}

// ======== ResponseWriter 包装器，捕获响应内容 ========
type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseBodyWriter) Write(b []byte) (int, error) {
	// 只捕获 JSON/文本类型的响应，跳过二进制流
	if w.body != nil && isTextContent(w.Header().Get("Content-Type")) {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func isTextContent(contentType string) bool {
	if contentType == "" {
		return true
	}
	textTypes := []string{"application/json", "text/", "application/xml", "application/x-www-form-urlencoded"}
	ct := strings.ToLower(contentType)
	for _, t := range textTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}
