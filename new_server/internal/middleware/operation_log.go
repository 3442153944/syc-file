package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/token"
)

// 操作日志中间件：把「谁、什么时候、做了什么、成没成功」落到 operation_log 表。
//
// operation_log 表建库时就有，但一直没人写。没有它，线上出问题只能翻 zap 文本日志，
// 且无法按用户/模块检索、无法在管理端展示。
//
// 只记**写操作**（POST/PUT/DELETE）：GET 是读，量大且无副作用，记下来只会把表撑爆。
// 另外几类高频写请求也要排除（见 skipLogging），否则一次同步就能写进去成百上千行。

// maxRecordedBody 请求/响应体最多记多少字节。分片上传的 body 是几 MB 的二进制，
// 整个记下来会把表撑爆，也没有任何排查价值。
const maxRecordedBody = 2000

// skipLogging 高频/无价值的路径前缀，命中则不记。
var skipLogging = []string{
	"/v1/file/upload/chunk",     // 每个分片一条，一个大文件就是几百条
	"/v1/sync/tasks/",           // complete/failed/blocked 回报，同步一轮成百上千次
	"/v1/sync/scan",             // 全量清单上报，body 巨大
	"/v1/sync/notify",           // 变更上报，高频
	"/v1/ws/",                   // WS 相关（连接本身是 GET，这里兜住其余）
	"/v1/user/verify",           // 每次启动都调
	"/v1/monitor/",              // 监控页轮询
}

// bodyRecorder 包一层 ResponseWriter 以便留存响应体。
type bodyRecorder struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (w bodyRecorder) Write(b []byte) (int, error) {
	if w.buf.Len() < maxRecordedBody {
		w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// OperationLogger 返回操作日志中间件。db 为 nil 时退化成空中间件。
func OperationLogger(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db == nil || !shouldLog(c) {
			c.Next()
			return
		}

		start := time.Now()
		reqBody := readRequestBody(c)
		recorder := bodyRecorder{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = recorder

		c.Next()

		// 写库放协程里：日志不该拖慢请求，更不该因为写库失败而影响响应
		go writeLog(db, c, reqBody, recorder.buf.String(), time.Since(start))
	}
}

func shouldLog(c *gin.Context) bool {
	switch c.Request.Method {
	case "POST", "PUT", "DELETE", "PATCH":
	default:
		return false
	}
	path := c.Request.URL.Path
	for _, p := range skipLogging {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	return true
}

// readRequestBody 读出 body 后必须原样塞回去，否则后面的 ShouldBindJSON 会读到空。
func readRequestBody(c *gin.Context) string {
	if c.Request.Body == nil {
		return ""
	}
	// 大 body（上传等）不读，避免把内存打满
	if c.Request.ContentLength > maxRecordedBody*4 {
		return "(body 过大，未记录)"
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxRecordedBody*4))
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(data))
	return truncate(string(data), maxRecordedBody)
}

func writeLog(db *gorm.DB, c *gin.Context, reqBody, respBody string, cost time.Duration) {
	defer func() { _ = recover() }() // 日志绝不能把请求带崩

	path := c.Request.URL.Path
	entry := model.OperationLog{
		OperationType:   ptr(operationTypeOf(c.Request.Method)),
		OperationModule: ptr(moduleOf(path)),
		OperationDesc:   ptr(c.Request.Method + " " + path),
		RequestMethod:   ptr(c.Request.Method),
		RequestURL:      ptr(truncate(c.Request.URL.RequestURI(), 500)),
		RequestParams:   ptr(desensitize(reqBody)),
		ResponseResult:  ptr(truncate(respBody, maxRecordedBody)),
		IPAddress:       ptr(c.ClientIP()),
		UserAgent:       ptr(truncate(c.Request.UserAgent(), 500)),
		ExecutionTime:   ptrInt(int(cost.Milliseconds())),
	}
	if claimsAny, ok := c.Get("UserInfo"); ok && claimsAny != nil {
		uid := uint(claimsAny.(*token.Claims).UserID)
		entry.UserID = &uid
	}
	// 业务码优先：全站错误都是 HTTP 200 + body.code，只看 HTTP 状态会把失败记成成功
	status := int8(1)
	if code := businessCode(respBody); code != 0 && code != 200 {
		status = 0
		entry.ErrorMessage = ptr(truncate(respBody, 500))
	} else if c.Writer.Status() >= 400 {
		status = 0
	}
	entry.Status = &status

	_ = db.Create(&entry).Error
}

// businessCode 从响应体里抠出业务 code；不是 JSON 或没有 code 字段则返回 0。
func businessCode(body string) int {
	if !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return 0
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return 0
	}
	return envelope.Code
}

// desensitize 抹掉 body 里的密码类字段——操作日志是给人看的，不该留明文口令。
func desensitize(body string) string {
	if body == "" {
		return ""
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return truncate(body, maxRecordedBody)
	}
	for k := range obj {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "password") || strings.Contains(lower, "token") ||
			strings.Contains(lower, "secret") {
			obj[k] = "***"
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return truncate(body, maxRecordedBody)
	}
	return truncate(string(out), maxRecordedBody)
}

// moduleOf 从路径推断业务模块，供管理端按模块筛选。
func moduleOf(path string) string {
	trimmed := strings.TrimPrefix(path, "/v1/")
	if i := strings.Index(trimmed, "/"); i > 0 {
		return trimmed[:i]
	}
	if trimmed == "" {
		return "other"
	}
	return trimmed
}

func operationTypeOf(method string) string {
	switch method {
	case "POST":
		return "create"
	case "PUT", "PATCH":
		return "update"
	case "DELETE":
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(截断)"
}

func ptr(s string) *string { return &s }
func ptrInt(i int) *int    { return &i }
