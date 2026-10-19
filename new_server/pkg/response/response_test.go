package response

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestEnvelopeAndStreamStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		status int
		write  func(*gin.Context)
	}{
		{"成功", 200, func(c *gin.Context) { OK(c, gin.H{"id": 1}) }},
		{"失败", 200, func(c *gin.Context) { Fail(c, 403, "权限不足") }},
		{"文件流失败", 403, func(c *gin.Context) { WriteHTTP(c, 403, 403, "权限不足", nil) }},
		{"分页", 200, func(c *gin.Context) { Page(c, []string{}, 0, 1, 20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			tc.write(c)
			if w.Code != tc.status {
				t.Fatalf("HTTP状态=%d", w.Code)
			}
			var body map[string]any
			if json.Unmarshal(w.Body.Bytes(), &body) != nil {
				t.Fatal("无效JSON")
			}
			for _, key := range []string{"code", "message", "data"} {
				if _, ok := body[key]; !ok {
					t.Fatalf("缺少字段 %s", key)
				}
			}
			if len(body) != 3 {
				t.Fatal("业务信封不应出现额外字段")
			}
		})
	}
}
