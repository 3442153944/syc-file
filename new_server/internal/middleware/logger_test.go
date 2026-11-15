package middleware

import "testing"

// 这些前缀下的响应体含一次性/长期凭据，绝不能进 debug 日志。
func TestSensitiveBodyPath(t *testing.T) {
	sensitive := []string{
		"/v1/admin/vps/nodes",
		"/v1/admin/vps/nodes/12/rotate",
		"/v1/admin/vps/nodes/12/snippet",
		"/v1/admin/dsh/tokens",
	}
	for _, p := range sensitive {
		if !sensitiveBodyPath(p) {
			t.Errorf("%s 应当脱敏", p)
		}
	}

	normal := []string{
		"/v1/ping",
		"/v1/admin/users",
		"/v1/admin/logs",
		"/v1/sync/tasks",
		"/v1/file/list",
		"/v1/admin/vpsx", // 前缀不同，不应误伤
	}
	for _, p := range normal {
		if sensitiveBodyPath(p) {
			t.Errorf("%s 不该脱敏（会丢排查信息）", p)
		}
	}
}
