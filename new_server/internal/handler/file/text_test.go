package file

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/pkg/filecore"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

// 整文件哈希 == 对全部内容做一次 HashChunk：这是文本保存直接对内存字节算哈希的前提。
// 与 filecore.Finalize（上传校验、同步 trunk 用的那一套）对拍，各种大小都要一致。
func TestHashOfBytesEqualsFinalize(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{0, 1, 100, 4096, 1 << 20, textMaxBytes} {
		data := bytes.Repeat([]byte("abcdefghij\n"), n/11+1)[:n]
		p := filepath.Join(dir, "f.txt")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		want, _, err := filecore.Finalize(p, 4194304, uint64(n), nil, nil)
		if err != nil {
			t.Fatalf("n=%d Finalize: %v", n, err)
		}
		got, err := hashOfBytes(data)
		if err != nil {
			t.Fatalf("n=%d hashOfBytes: %v", n, err)
		}
		if got != hexStr(want) {
			t.Errorf("n=%d 哈希不一致: HashChunk=%s Finalize=%s", n, got, hexStr(want))
		}
	}
}

func hexStr(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

func TestIsEditableText(t *testing.T) {
	for _, n := range []string{"a.txt", "A.TXT", "x.json", "app.yaml", "nginx.conf", "Dockerfile", "makefile", ".env", ".gitignore", "main.go", "a.log"} {
		if !isEditableText(n) {
			t.Errorf("%s 应可编辑", n)
		}
	}
	for _, n := range []string{"a.png", "b.exe", "c.zip", "d.docx", "noext", "e.mp4"} {
		if isEditableText(n) {
			t.Errorf("%s 不应可编辑", n)
		}
	}
}

// ── HTTP 层 ───────────────────────────────────────────────────

type textEnv struct {
	t    *testing.T
	root string
	r    *gin.Engine
}

func newTextEnv(t *testing.T, authed bool) *textEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger.Logger = zap.NewNop()
	root := t.TempDir()
	oldAllowed, oldTemp := config.Conf.File.AllowedPaths, textTempPath
	config.Conf.File.AllowedPaths = []string{root}
	textTempPath = func(target, id string) string { return filepath.Join(root, ".tmp", id+".part") }
	t.Cleanup(func() { config.Conf.File.AllowedPaths, textTempPath = oldAllowed, oldTemp })

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authed {
			c.Set("UserInfo", &token.Claims{UserID: 1})
		}
	})
	r.GET("/text/read", HandlerFuncTextRead(nil, nil))
	r.POST("/text/save", HandlerFuncTextSave(nil, nil, nil))
	return &textEnv{t: t, root: root, r: r}
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *textEnv) do(req *http.Request) envelope {
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		e.t.Fatalf("业务错误应以 HTTP 200 + code 返回，实际 HTTP %d: %s", w.Code, w.Body.String())
	}
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		e.t.Fatalf("响应不是 JSON: %v: %s", err, w.Body.String())
	}
	return env
}

func (e *textEnv) read(path, name string) envelope {
	q := url.Values{"path": {path}, "name": {name}}
	return e.do(httptest.NewRequest(http.MethodGet, "/text/read?"+q.Encode(), nil))
}

func (e *textEnv) save(body map[string]any) envelope {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/text/save", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return e.do(req)
}

func (e *textEnv) write(name string, data []byte) string {
	p := filepath.Join(e.root, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

type snapshotData struct {
	Content string `json:"content"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	EOL     string `json:"eol"`
	BOM     bool   `json:"bom"`
}

func decode[T any](t *testing.T, env envelope) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(env.Data, &v); err != nil {
		t.Fatalf("data 解析失败: %v: %s", err, env.Data)
	}
	return v
}

type saveData struct {
	Saved     bool         `json:"saved"`
	Conflict  bool         `json:"conflict"`
	Unchanged bool         `json:"unchanged"`
	Hash      string       `json:"hash"`
	Current   snapshotData `json:"current"`
}

func TestTextReadBasics(t *testing.T) {
	e := newTextEnv(t, true)
	e.write("a.txt", []byte("hello\nworld\n"))

	env := e.read(e.root, "a.txt")
	if env.Code != 200 {
		t.Fatalf("code=%d %s", env.Code, env.Message)
	}
	s := decode[snapshotData](t, env)
	if s.Content != "hello\nworld\n" || s.EOL != "lf" || s.BOM || s.Size != 12 {
		t.Errorf("读取结果不对: %+v", s)
	}
	if want, _ := hashOfBytes([]byte("hello\nworld\n")); s.Hash != want {
		t.Errorf("版本号应为内容哈希: %s vs %s", s.Hash, want)
	}
	// path 已是完整路径也行
	if env2 := e.read(filepath.Join(e.root, "a.txt"), "a.txt"); env2.Code != 200 {
		t.Errorf("完整路径也应可读: %d", env2.Code)
	}
}

// CRLF 与 BOM：读出来统一成 \n 且不含 BOM（浏览器文本框里没有 \r），换行风格和 BOM 单独返回。
func TestTextReadNormalizesEOLAndBOM(t *testing.T) {
	e := newTextEnv(t, true)
	e.write("win.ini", append(append([]byte{}, utf8BOM...), []byte("a=1\r\nb=2\r\n")...))
	s := decode[snapshotData](t, e.read(e.root, "win.ini"))
	if s.Content != "a=1\nb=2\n" || s.EOL != "crlf" || !s.BOM {
		t.Errorf("应归一化为 LF 并标出 crlf/bom: %+v", s)
	}
	// 版本号是原始字节（含 BOM、CRLF）的哈希，才能和同步里记的 file_hash 对上
	raw := append(append([]byte{}, utf8BOM...), []byte("a=1\r\nb=2\r\n")...)
	if want, _ := hashOfBytes(raw); s.Hash != want {
		t.Errorf("版本号应对原始字节计算")
	}
}

func TestTextReadRejections(t *testing.T) {
	e := newTextEnv(t, true)
	e.write("bin.txt", []byte("abc\x00def"))
	e.write("gbk.txt", []byte{0xC4, 0xE3, 0xBA, 0xC3}) // GBK 的「你好」，不是合法 UTF-8
	e.write("big.log", bytes.Repeat([]byte("x"), textMaxBytes+1))
	e.write("pic.png", []byte("x"))
	cases := []struct {
		name, file string
		path       string
		want       int
	}{
		{"二进制内容", "bin.txt", e.root, 415},
		{"非 UTF-8", "gbk.txt", e.root, 415},
		{"超过 2MB", "big.log", e.root, 413},
		{"扩展名不支持", "pic.png", e.root, 415},
		{"文件不存在", "nope.txt", e.root, 404},
		{"路径不在允许范围", "passwd.txt", "/etc", 403},
		{"穿越出允许范围", "x.txt", filepath.Join(e.root, "..", ".."), 403},
	}
	for _, c := range cases {
		if got := e.read(c.path, c.file).Code; got != c.want {
			t.Errorf("%s: 期望 code %d，实际 %d", c.name, c.want, got)
		}
	}
	if got := e.read(e.root, "").Code; got != 400 {
		t.Errorf("缺参数应 400，实际 %d", got)
	}
	// 目录不是普通文件
	_ = os.Mkdir(filepath.Join(e.root, "dir.txt"), 0o755)
	if got := e.read(e.root, "dir.txt").Code; got != 400 {
		t.Errorf("目录应 400，实际 %d", got)
	}
}

func TestTextRequiresLogin(t *testing.T) {
	e := newTextEnv(t, false)
	e.write("a.txt", []byte("x"))
	if got := e.read(e.root, "a.txt").Code; got != 401 {
		t.Errorf("未登录读取应 401，实际 %d", got)
	}
	if got := e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "y", "base_hash": "h"}).Code; got != 401 {
		t.Errorf("未登录保存应 401，实际 %d", got)
	}
}

func TestTextSaveRoundTrip(t *testing.T) {
	e := newTextEnv(t, true)
	p := e.write("a.txt", []byte("v1\n"))
	cur := decode[snapshotData](t, e.read(e.root, "a.txt"))

	env := e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "v2\n", "base_hash": cur.Hash})
	res := decode[saveData](t, env)
	if env.Code != 200 || !res.Saved || res.Conflict {
		t.Fatalf("应保存成功: %+v (%s)", res, env.Message)
	}
	if b, _ := os.ReadFile(p); string(b) != "v2\n" {
		t.Errorf("磁盘内容应已更新，实际 %q", b)
	}
	// 返回的版本号必须与 filecore.Finalize 对磁盘文件算出的一致 —— 同步 trunk 用的就是这个值
	want, _, err := filecore.Finalize(p, 4194304, 3, nil, nil)
	if err != nil || res.Hash != hexStr(want) {
		t.Errorf("返回的版本号应等于文件的 blake3：%s vs %s (%v)", res.Hash, hexStr(want), err)
	}
	// 用返回的新版本号可以继续保存
	res2 := decode[saveData](t, e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "v3\n", "base_hash": res.Hash}))
	if !res2.Saved {
		t.Error("用新版本号应能继续保存")
	}
	// 没有残留临时文件
	if entries, _ := os.ReadDir(filepath.Join(e.root, ".tmp")); len(entries) != 0 {
		t.Errorf("临时目录里不该有残留，实际 %d 个", len(entries))
	}
}

// 核心：别人在我编辑期间改过 → 不覆盖，返回对方最新内容和版本号。
func TestTextSaveDetectsConflictAndDoesNotOverwrite(t *testing.T) {
	e := newTextEnv(t, true)
	p := e.write("a.txt", []byte("line1\nline2\n"))
	mine := decode[snapshotData](t, e.read(e.root, "a.txt")) // 我打开时的版本

	// 另一个人先保存了
	other := decode[saveData](t, e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "line1\nline2\nfrom-other\n", "base_hash": mine.Hash}))
	if !other.Saved {
		t.Fatal("先保存的人应成功")
	}

	// 我再用旧版本号保存 → 冲突
	env := e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "line1 edited\nline2\n", "base_hash": mine.Hash})
	res := decode[saveData](t, env)
	if env.Code != 200 || res.Saved || !res.Conflict {
		t.Fatalf("应判为冲突: %+v", res)
	}
	if res.Current.Content != "line1\nline2\nfrom-other\n" || res.Current.Hash != other.Hash {
		t.Errorf("冲突响应应带对方的最新内容和版本号: %+v", res.Current)
	}
	if b, _ := os.ReadFile(p); string(b) != "line1\nline2\nfrom-other\n" {
		t.Errorf("冲突时不得覆盖磁盘文件，实际 %q", b)
	}

	// 明确选择覆盖（force）则以我的为准
	forced := decode[saveData](t, e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "mine wins\n", "force": true}))
	if !forced.Saved {
		t.Error("force 应能覆盖")
	}
	if b, _ := os.ReadFile(p); string(b) != "mine wins\n" {
		t.Errorf("force 后应是我的内容，实际 %q", b)
	}
}

// 多人同时保存同一个版本：恰好一个成功，其余都是冲突，没有人的修改被静默覆盖。
func TestTextConcurrentSavesExactlyOneWins(t *testing.T) {
	e := newTextEnv(t, true)
	e.write("a.txt", []byte("base\n"))
	base := decode[snapshotData](t, e.read(e.root, "a.txt"))

	const n = 12
	var wg gosync.WaitGroup
	results := make([]saveData, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = decode[saveData](t, e.save(map[string]any{
				"path": e.root, "name": "a.txt", "content": "edit-" + string(rune('A'+i)) + "\n", "base_hash": base.Hash,
			}))
		}(i)
	}
	wg.Wait()
	saved, conflicts := 0, 0
	for _, r := range results {
		if r.Saved {
			saved++
		}
		if r.Conflict {
			conflicts++
		}
	}
	if saved != 1 || conflicts != n-1 {
		t.Fatalf("应恰好 1 个成功、%d 个冲突，实际 成功 %d 冲突 %d", n-1, saved, conflicts)
	}
}

// 换行风格、BOM、权限在保存后原样保留：编辑一行不该把整份文件的换行改掉。
func TestTextSavePreservesEOLBOMAndMode(t *testing.T) {
	e := newTextEnv(t, true)
	p := filepath.Join(e.root, "win.conf")
	orig := append(append([]byte{}, utf8BOM...), []byte("a=1\r\nb=2\r\n")...)
	if err := os.WriteFile(p, orig, 0o640); err != nil {
		t.Fatal(err)
	}
	cur := decode[snapshotData](t, e.read(e.root, "win.conf"))

	// 编辑框里只有 \n
	res := decode[saveData](t, e.save(map[string]any{"path": e.root, "name": "win.conf", "content": "a=1\nb=3\n", "base_hash": cur.Hash}))
	if !res.Saved {
		t.Fatal("应保存成功")
	}
	got, _ := os.ReadFile(p)
	want := append(append([]byte{}, utf8BOM...), []byte("a=1\r\nb=3\r\n")...)
	if !bytes.Equal(got, want) {
		t.Errorf("应保留 BOM 和 CRLF：期望 %q 实际 %q", want, got)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("权限应保持 0640，实际 %v", fi.Mode().Perm())
	}
}

func TestTextSaveUnchangedIsNoop(t *testing.T) {
	e := newTextEnv(t, true)
	p := e.write("a.txt", []byte("same\n"))
	before, _ := os.Stat(p)
	cur := decode[snapshotData](t, e.read(e.root, "a.txt"))
	res := decode[saveData](t, e.save(map[string]any{"path": e.root, "name": "a.txt", "content": "same\n", "base_hash": cur.Hash}))
	if !res.Saved || !res.Unchanged || res.Hash != cur.Hash {
		t.Errorf("内容没变应返回 unchanged: %+v", res)
	}
	if after, _ := os.Stat(p); !after.ModTime().Equal(before.ModTime()) {
		t.Error("内容没变不该写盘（修改时间不应变化）")
	}
}

func TestTextSaveValidation(t *testing.T) {
	e := newTextEnv(t, true)
	e.write("a.txt", []byte("x"))
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"缺 base_hash 且不 force", map[string]any{"path": e.root, "name": "a.txt", "content": "y"}, 400},
		{"扩展名不支持", map[string]any{"path": e.root, "name": "a.png", "content": "y", "base_hash": "h"}, 415},
		{"路径不允许", map[string]any{"path": "/etc", "name": "passwd.txt", "content": "y", "base_hash": "h"}, 403},
		{"文件不存在", map[string]any{"path": e.root, "name": "nope.txt", "content": "y", "base_hash": "h"}, 404},
		{"内容超过 2MB", map[string]any{"path": e.root, "name": "a.txt", "content": strings.Repeat("x", textMaxBytes+1), "base_hash": "h"}, 413},
	}
	for _, c := range cases {
		if got := e.save(c.body).Code; got != c.want {
			t.Errorf("%s: 期望 code %d，实际 %d", c.name, c.want, got)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "a.txt")); string(b) != "x" {
		t.Error("所有被拒绝的请求都不应改动文件")
	}
}
