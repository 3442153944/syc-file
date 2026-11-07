package file

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"syc-file/internal/model"
	"syc-file/pkg/upload_store"
)

// postChunk 直接以 raw body 发送一个分片，返回业务 code 与响应。
func postChunk(t *testing.T, env *qsTestEnv, uploadID string, index int, attemptID string, data []byte) (int, map[string]any) {
	t.Helper()
	url := fmt.Sprintf("/upload/chunk?upload_id=%s&index=%d", uploadID, index)
	if attemptID != "" {
		url += "&attempt_id=" + attemptID
	}
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("chunk %d 响应不是 JSON: %v (%s)", index, err, rec.Body.String())
	}
	code := int(out["code"].(float64))
	return code, out
}

// TestUploadChunkHotPathZeroRedisCommands 收片热路径必须 0 条 Redis 命令。
//
// 时序：init（慢路径建会话/缓存）→ 前两片预热（第一片触发 receiver open + 首次位图快照）
// → 清空计数器 → 连续收片窗口内断言「命令数 == 0」，且不存在
// HGETALL/GET/SETBIT/EXPIRE/BITCOUNT 等逐片访问。
// 窗口内已接受的片只在 Rust 内存/磁盘上，Redis 只有首次快照的空位图；
// 随后 complete 仍应成功（证明收齐判定不依赖 Redis 位图）。
func TestUploadChunkHotPathZeroRedisCommands(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	const chunkSize = 32 * 1024
	const chunkCount = 6
	content := bytes.Repeat([]byte("0123456789abcdef"), chunkSize*chunkCount/16)
	leavesHex, rootHex, fileHashHex := describeContent(t, content, chunkSize)

	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name":        "hotpath.bin",
		"quick_share": true,
		"total_size":  len(content),
		"chunk_size":  chunkSize,
		"chunk_count": chunkCount,
		"merkle_root": rootHex,
		"file_hash":   fileHashHex,
		"leaf_hashes": leavesHex,
	})
	if code != 200 {
		t.Fatalf("init: %v", resp)
	}
	uploadID := resp["data"].(map[string]any)["upload_id"].(string)

	chunk := func(i int) []byte { return content[i*chunkSize : (i+1)*chunkSize] }

	// 预热：第 0 片走慢路径（Redis 取 persisted 位图 + ReceiverOpen）。
	// 注意 receiver 的 accept 只进内存 pending、不置 dirty，所以普通收片本身不触发快照；
	// 快照只在 flush 落盘后（pending 超限自动 flush 或 complete）才发生。
	for _, idx := range []int{0, 1} {
		if c, r := postChunk(t, env, uploadID, idx, "", chunk(idx)); c != 200 {
			t.Fatalf("预热分片 %d 失败: code=%d resp=%v", idx, c, r)
		}
	}
	// 先确认 hook 确实在工作：预热阶段（receiver 不存在 → 读 Redis 位图）必须有命令被记录，
	// 否则后面的「0 命令」断言会变成永远成立的空断言。
	if warmup := env.redisCmds.Snapshot(); len(warmup) == 0 {
		t.Fatalf("Redis 命令 hook 未捕获到预热阶段的命令，计数器不可信")
	}

	env.redisCmds.Reset()
	for idx := 2; idx < chunkCount; idx++ {
		c, r := postChunk(t, env, uploadID, idx, "", chunk(idx))
		if c != 200 {
			t.Fatalf("稳态分片 %d 失败: code=%d resp=%v", idx, c, r)
		}
		data, _ := r["data"].(map[string]any)
		if got := int(data["received"].(float64)); got != idx+1 {
			t.Fatalf("分片 %d received=%d, 期望 %d", idx, got, idx+1)
		}
		wantComplete := idx == chunkCount-1
		if data["complete"].(bool) != wantComplete {
			t.Fatalf("分片 %d complete=%v, 期望 %v", idx, data["complete"], wantComplete)
		}
	}

	cmds := env.redisCmds.Snapshot()
	if len(cmds) != 0 {
		t.Fatalf("收片稳态窗口内出现 %d 条 Redis 命令，应为 0: %v", len(cmds), cmds)
	}
	for _, name := range cmds {
		switch strings.ToLower(name) {
		case "hgetall", "hget", "get", "setbit", "expire", "bitcount", "touch", "set":
			t.Fatalf("热路径出现逐片 Redis 命令 %q", name)
		}
	}

	// 收齐判定必须来自 Rust accepted 位图：窗口内片未写进 Redis 快照，complete 仍应成功。
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": uploadID})
	if code != 200 {
		t.Fatalf("complete code=%d msg=%v", code, resp["message"])
	}
	shareCode := resp["data"].(map[string]any)["share_code"].(string)
	var link model.ShareLink
	if err := env.db.Where("share_code = ?", shareCode).First(&link).Error; err != nil {
		t.Fatalf("查询分享记录失败: %v", err)
	}
	got, err := os.ReadFile(link.OriginalPath)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("落盘内容不一致: err=%v len=%d/%d", err, len(got), len(content))
	}
}

// TestBadChunkCompleteReturnsBadIndexThenRecovers 坏块端到端：
// 服务端不再在收片时提前校验叶子哈希，错误内容会被接收 → complete 在 finalize 阶段
// 定位到首个坏块，返回 422 + bad_index（客户端只需补传该片）→ 补传正确片后 complete 200
// 且落盘内容正确。
func TestBadChunkCompleteReturnsBadIndexThenRecovers(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	const chunkSize = 100000
	const chunkCount = 3
	content := make([]byte, 2*chunkSize+1234)
	for i := range content {
		content[i] = byte(i*7 + 3)
	}
	leavesHex, rootHex, fileHashHex := describeContent(t, content, chunkSize)

	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name":        "badchunk.bin",
		"quick_share": true,
		"total_size":  len(content),
		"chunk_size":  chunkSize,
		"chunk_count": chunkCount,
		"merkle_root": rootHex,
		"file_hash":   fileHashHex,
		"leaf_hashes": leavesHex,
	})
	if code != 200 {
		t.Fatalf("init: %v", resp)
	}
	uploadID := resp["data"].(map[string]any)["upload_id"].(string)
	chunk := func(i int) []byte {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(content) {
			end = len(content)
		}
		return content[start:end]
	}

	// 前两片正确；末片内容写坏（长度正确，服务端收片阶段不校验，会接受）
	for _, idx := range []int{0, 1} {
		if c, r := postChunk(t, env, uploadID, idx, "", chunk(idx)); c != 200 {
			t.Fatalf("分片 %d 失败: code=%d resp=%v", idx, c, r)
		}
	}
	bad := append([]byte(nil), chunk(2)...)
	bad[0] ^= 0xFF
	if c, r := postChunk(t, env, uploadID, 2, "", bad); c != 200 {
		t.Fatalf("坏片应被接收（不再提前校验）: code=%d resp=%v", c, r)
	}

	// 第一次 complete：finalize 定位到坏块 2
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": uploadID})
	if code != 422 {
		t.Fatalf("坏块 complete code=%d, 期望 422: %v", code, resp)
	}
	badData, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("坏块响应缺少 data: %v", resp)
	}
	if got := int(badData["bad_index"].(float64)); got != 2 {
		t.Fatalf("bad_index=%d, 期望 2", got)
	}

	// 补传正确片后重试 complete → 200 且落盘内容正确
	if c, r := postChunk(t, env, uploadID, 2, "", chunk(2)); c != 200 {
		t.Fatalf("补传分片失败: code=%d resp=%v", c, r)
	}
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": uploadID})
	if code != 200 {
		t.Fatalf("补传后 complete code=%d msg=%v", code, resp["message"])
	}
	shareCode := resp["data"].(map[string]any)["share_code"].(string)
	var link model.ShareLink
	if err := env.db.Where("share_code = ?", shareCode).First(&link).Error; err != nil {
		t.Fatalf("查询分享记录失败: %v", err)
	}
	got, err := os.ReadFile(link.OriginalPath)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("补传后落盘内容不一致: err=%v len=%d/%d", err, len(got), len(content))
	}
}

// TestReceiverOpenAcceptsLegacyShortBitmap F1 升级兼容回归。
//
// 旧版本收片用逐片 SETBIT 维护 Redis 位图，Redis String 末尾字节不补齐：当 chunk_count>8
// 且最高已收索引 < (need-1)*8 时，Redis bitmap 长度 < need=ceil(chunkCount/8)。
// New ensureReceiver 必须在 fc_receiver_open 前把旧短位图补齐到 need，否则 Rust 侧
// parse_persisted 返回 FC_ERR_ARG，升级切换时在途旧会话的 chunk/complete 会持续 500。
//
// 场景：chunkCount=20（need=3 字节），旧会话已落盘第 0 片、Redis 位图只有 1 字节 0x80。
// 修复前：补传第 1 片时 ensureReceiver 打开 receiver 失败 → 500；
// 修复后：补齐为 {0x80,0x00,0x00} → 分片 1..19 均 200，complete 200 且落盘逐字节一致。
func TestReceiverOpenAcceptsLegacyShortBitmap(t *testing.T) {
	env := setupQuickShareUploadTest(t)
	const chunkSize = 1024
	const chunkCount = 20
	content := make([]byte, chunkSize*chunkCount)
	for i := range content {
		content[i] = byte(i*17 + 5)
	}
	leavesHex, rootHex, fileHashHex := describeContent(t, content, chunkSize)

	code, resp := doJSON(t, env.router, http.MethodPost, "/upload/init", map[string]any{
		"name":        "legacy-short-bitmap.bin",
		"quick_share": true,
		"total_size":  len(content),
		"chunk_size":  chunkSize,
		"chunk_count": chunkCount,
		"merkle_root": rootHex,
		"file_hash":   fileHashHex,
		"leaf_hashes": leavesHex,
	})
	if code != 200 {
		t.Fatalf("init: %v", resp)
	}
	uploadID := resp["data"].(map[string]any)["upload_id"].(string)

	// 模拟旧版本已完成的状态：
	// 1) 第 0 片已由旧版本 ChunkWrite 直接落盘（临时文件 offset 0）
	sess, err := upload_store.Global.Get(context.Background(), uploadID)
	if err != nil {
		t.Fatalf("读会话失败: %v", err)
	}
	f, err := os.OpenFile(sess.TempPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("打开临时文件失败: %v", err)
	}
	if _, err := f.WriteAt(content[:chunkSize], 0); err != nil {
		_ = f.Close()
		t.Fatalf("模拟旧版本落盘失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭临时文件失败: %v", err)
	}
	// 2) 旧版本 Redis 位图：只置过第 0 片（SETBIT 只增长到 1 字节，need=3，故意不补齐）
	if err := env.rdb.Set(context.Background(), "upload:bits:"+uploadID, []byte{0x80}, uploadSessionTTL).Err(); err != nil {
		t.Fatalf("写入旧版本短位图失败: %v", err)
	}

	// 客户端继续补传 1..19：修复前首片就会因 FC_ERR_ARG 返回 500
	for idx := 1; idx < chunkCount; idx++ {
		off := idx * chunkSize
		c, r := postChunk(t, env, uploadID, idx, "", content[off:off+chunkSize])
		if c != 200 {
			t.Fatalf("旧短位图会话补传分片 %d 失败（F1 升级兼容未修复？）: code=%d resp=%v", idx, c, r)
		}
	}

	// complete：旧位图已知位（第 0 片）必须保留（否则会误判缺失/坏块），落盘内容逐字节一致
	code, resp = doJSON(t, env.router, http.MethodPost, "/upload/complete", map[string]any{"upload_id": uploadID})
	if code != 200 {
		t.Fatalf("旧短位图会话 complete code=%d msg=%v", code, resp["message"])
	}
	shareCode := resp["data"].(map[string]any)["share_code"].(string)
	var link model.ShareLink
	if err := env.db.Where("share_code = ?", shareCode).First(&link).Error; err != nil {
		t.Fatalf("查询分享记录失败: %v", err)
	}
	got, err := os.ReadFile(link.OriginalPath)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("旧短位图会话落盘内容不一致: err=%v len=%d/%d", err, len(got), len(content))
	}
}

// TestNormalizePersistedBitmap 对 F1 修复的长度规范化做单元级覆盖（含 e2e 未覆盖的截断分支）。
func TestNormalizePersistedBitmap(t *testing.T) {
	cases := []struct {
		name       string
		in         []byte
		chunkCount int
		want       []byte
	}{
		{"无持久化位图保持nil", nil, 20, nil},
		{"空位图保持nil", []byte{}, 20, nil},
		{"旧版本1字节短位图补齐到3字节", []byte{0x80}, 20, []byte{0x80, 0x00, 0x00}},
		{"等长原样返回", []byte{0x80, 0x40, 0x20}, 20, []byte{0x80, 0x40, 0x20}},
		{"超长截断到need", []byte{0x80, 0x40, 0x20, 0xFF}, 20, []byte{0x80, 0x40, 0x20}},
		{"单片一字节不变", []byte{0x80}, 1, []byte{0x80}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizePersistedBitmap(tc.in, tc.chunkCount)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("normalizePersistedBitmap(%x, %d) = %x, 期望 %x", tc.in, tc.chunkCount, got, tc.want)
			}
		})
	}
}
