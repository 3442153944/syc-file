package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// editorFixture 一份“像真实 config.yaml”的测试配置：必需段齐全、带注释、带未知键（gui）。
// 用字符串而不是文件，方便每个用例自己写临时文件。
const editorFixture = `# 顶层注释：数据库配置
db:
  host: 127.0.0.1
  port: 3306
  name: syncfile
  user: root
  password: "123456"

# 缓存配置
redis:
  host: 127.0.0.1
  port: 6379
  db: 0

# 日志配置
log:
  path: ./log
  level: info
  format: json        # 输出格式
  console: true
  file: true
  max_size: 100       # 日志文件最大大小（MB）

# 白名单路由
whitelist:
  - /v1/ping
  - /v1/user/login

# 认证配置
auth:
  enabled: true
  token_expire: 7
  refresh_expire: 1
  secret: "secret-value"

server:
  port: 8991
  mode: dev

file:
  allowed_paths:
    - "D:"
  storage:
    base_path: "FileSync"
  upload:
    max_file_size: 10737418240     # 最大文件大小 10GB（字节）
    max_filename_length: 255       # 最大文件名长度
    # forbidden_extensions:
    #   - ".exe"

# 未知键：GUI 自己的配置，后端不解析但要原样保留
gui:
  enabled: true          # 仅 GUI 使用
  start_minimized: true
`

// writeEditorFixture 在临时目录写一份 config.yaml，返回目录与文件路径。
func writeEditorFixture(t *testing.T, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	// 归一化会读 SYC_MODE，清掉外部环境保证用例可重复。
	t.Setenv("SYC_MODE", "")
	return dir, path
}

// commentLines 抽出每一行里的注释文本（从第一个 # 开始），保持原顺序。
// 用来断言“重新编码后注释一条不丢、不重复、顺序不变”。
func commentLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			out = append(out, strings.TrimRight(line[i:], "\r"))
		}
	}
	return out
}

// TestUpdateValuesOnCommentOnlyFile 只有注释的空文件：可以从零建出必需段，注释保留。
func TestUpdateValuesOnCommentOnlyFile(t *testing.T) {
	clearMeshEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "# 只有注释的配置文件\n# 第二行注释\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := UpdateValues(path, map[string]string{
		"server.port":       "8991",
		"server.mode":       "dev",
		"db.host":           "127.0.0.1",
		"db.port":           "3306",
		"redis.host":        "127.0.0.1",
		"redis.port":        "6379",
		"auth.token_expire": "7",
		"log.level":         "info",
	}); err != nil {
		t.Fatalf("UpdateValues(纯注释文件): %v", err)
	}

	text := readFileString(t, path)
	for _, comment := range []string{"# 只有注释的配置文件", "# 第二行注释"} {
		if !strings.Contains(text, comment) {
			t.Fatalf("纯注释文件的注释丢失: %q\n---\n%s", comment, text)
		}
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("从零建出的配置未通过校验: %v", err)
	}
	if cfg.Server.Port != 8991 || cfg.DB.Host != "127.0.0.1" || cfg.Redis.Port != 6379 {
		t.Fatalf("建出的配置内容不对: %#v", cfg)
	}
}

// TestUpdateValuesRejectsSectionPath 配置段（映射）不能被当成叶子字段直接赋值。
func TestUpdateValuesRejectsSectionPath(t *testing.T) {
	clearMeshEnv(t)
	_, path := writeEditorFixture(t, editorFixture)

	for _, badPath := range []string{"file.upload", "gui"} {
		if _, err := UpdateValues(path, map[string]string{badPath: "x"}); err == nil {
			t.Fatalf("%s 是配置段，应拒绝直接赋值", badPath)
		}
		if got := readFileString(t, path); got != editorFixture {
			t.Fatalf("拒绝 %s 时不应改动文件", badPath)
		}
	}
}

// readFileString 读文件，断言存在并且内容符合预期。
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件 %s 失败: %v", path, err)
	}
	return string(data)
}

// TestLoadTextNormalizesLikeInit 校验 LoadText/LoadFile 的归一化与 Init 一致：
// mode 默认/小写、vpn 默认值、SYC_MODE 覆盖、LAN 端点去重。
func TestLoadTextNormalizesLikeInit(t *testing.T) {
	clearMeshEnv(t)
	text := editorFixture + `
vpn:
  server_lan_endpoints:
    - " 10.12.3.1:51820 "
    - "10.12.3.1:51820"
    - "10.12.3.2:51820"
`
	cfg, err := LoadText(text)
	if err != nil {
		t.Fatalf("LoadText: %v", err)
	}
	if cfg.Server.Mode != ModeDev {
		t.Fatalf("server.mode = %q, want %q", cfg.Server.Mode, ModeDev)
	}
	if cfg.Vpn.Pool != "172.1.0.0/16" {
		t.Fatalf("vpn.pool = %q, want 默认 172.1.0.0/16", cfg.Vpn.Pool)
	}
	if cfg.Vpn.ServerAddress != "172.1.0.254" {
		t.Fatalf("vpn.server_address = %q, want 默认 172.1.0.254", cfg.Vpn.ServerAddress)
	}
	if cfg.Vpn.StunPort != 3478 || cfg.Vpn.ServerWGPort != 51820 {
		t.Fatalf("vpn 端口默认值不对: stun=%d wg=%d", cfg.Vpn.StunPort, cfg.Vpn.ServerWGPort)
	}
	wantEndpoints := []string{"10.12.3.1:51820", "10.12.3.2:51820"}
	if !reflect.DeepEqual(cfg.Vpn.ServerLANEndpoints, wantEndpoints) {
		t.Fatalf("LAN 端点去重/去空白不对: %#v", cfg.Vpn.ServerLANEndpoints)
	}

	// 大小写归一化 + SYC_MODE 覆盖
	upperCfg, err := LoadText("server:\n  port: 1\n  mode: DEV\n")
	if err != nil {
		t.Fatalf("LoadText(大写 mode): %v", err)
	}
	if upperCfg.Server.Mode != ModeDev {
		t.Fatalf("mode 应小写归一化为 dev，得到 %q", upperCfg.Server.Mode)
	}
	t.Setenv("SYC_MODE", "prod")
	envCfg, err := LoadText(editorFixture)
	if err != nil {
		t.Fatalf("LoadText(SYC_MODE=prod): %v", err)
	}
	if envCfg.Server.Mode != ModeProd {
		t.Fatalf("SYC_MODE 应覆盖配置文件，得到 %q", envCfg.Server.Mode)
	}
}

// TestLoadFileReadsDisk 校验 LoadFile = ReadText + LoadText，且读的是磁盘内容。
func TestLoadFileReadsDisk(t *testing.T) {
	clearMeshEnv(t)
	_, path := writeEditorFixture(t, editorFixture)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Log.Level != "info" || cfg.Server.Port != 8991 {
		t.Fatalf("LoadFile 结果不对: log.level=%q server.port=%d", cfg.Log.Level, cfg.Server.Port)
	}
	text, err := ReadText(path)
	if err != nil || text != editorFixture {
		t.Fatalf("ReadText 应返回原文: err=%v len=%d", err, len(text))
	}
}

// TestLoadTextRejectsInvalidYAML 非法 YAML 必须报错。
func TestLoadTextRejectsInvalidYAML(t *testing.T) {
	if _, err := LoadText("db: [\n"); err == nil {
		t.Fatal("非法 YAML 应返回错误")
	}
	if _, err := LoadText("just-a-scalar"); err == nil {
		t.Fatal("根节点不是映射时应返回错误")
	}
}

// TestValidate 必需段/关键值校验；现有示例配置必须能通过。
func TestValidate(t *testing.T) {
	clearMeshEnv(t)
	cfg, err := LoadText(editorFixture)
	if err != nil {
		t.Fatalf("LoadText: %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("合法配置不应校验失败: %v", err)
	}
	if err := Validate(nil); err == nil {
		t.Fatal("nil 配置应校验失败")
	}

	bad := *cfg
	bad.Server.Port = 0
	if err := Validate(&bad); err == nil {
		t.Fatal("server.port=0 应校验失败")
	}
	bad = *cfg
	bad.DB.Host = "  "
	if err := Validate(&bad); err == nil {
		t.Fatal("db.host 为空应校验失败")
	}
	bad = *cfg
	bad.Redis.Host = ""
	if err := Validate(&bad); err == nil {
		t.Fatal("redis.host 为空应校验失败")
	}
	bad = *cfg
	bad.Auth.TokenExpire = 0
	if err := Validate(&bad); err == nil {
		t.Fatal("auth.token_expire=0 应校验失败")
	}
	bad = *cfg
	bad.Log.Level = "verbose"
	if err := Validate(&bad); err == nil {
		t.Fatal("非法 log.level 应校验失败")
	}

	// 仓库里的示例配置必须能通过校验（GUI 第一次打开就是读它）。
	example, err := LoadFile(filepath.Join("..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatalf("加载 config.example.yaml 失败: %v", err)
	}
	if err := Validate(example); err != nil {
		t.Fatalf("config.example.yaml 未通过校验: %v", err)
	}
	// 嵌套结构（含 list<struct>）必须完整解出来，不能只解出顶层键。
	if len(example.Supervisor.Processes) != 3 {
		t.Fatalf("supervisor.processes 解析数量 = %d, want 3", len(example.Supervisor.Processes))
	}
	if p := example.Supervisor.Processes[0]; p.Name != "frpc-ddns" || len(p.Args) != 2 {
		t.Fatalf("supervisor.processes[0] 解析不完整: %#v", p)
	}
	if len(example.File.AllowedPaths) != 4 || example.Thumbnail.Workers != 8 {
		t.Fatalf("示例配置嵌套字段解析不对: allowed_paths=%v workers=%d",
			example.File.AllowedPaths, example.Thumbnail.Workers)
	}
}

// TestUpdateValuesOnExampleConfig 用真实示例配置跑一遍编辑：注释/未知结构保留、结果仍可校验。
func TestUpdateValuesOnExampleConfig(t *testing.T) {
	clearMeshEnv(t)
	original, err := ReadText(filepath.Join("..", "config", "config.example.yaml"))
	if err != nil {
		t.Fatalf("读 config.example.yaml 失败: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	backup, err := UpdateValues(path, map[string]string{
		"log.level":                      "debug",
		"server.port":                    "8992",
		"file.allowed_paths":             "D:\nE:",
		"quick_share.max_capacity_bytes": "5368709120",
	})
	if err != nil {
		t.Fatalf("UpdateValues(config.example): %v", err)
	}
	if got := readFileString(t, backup); got != original {
		t.Fatal("备份内容应等于示例配置原文")
	}

	text := readFileString(t, path)
	// 文件头注释、深层段注释都要在
	for _, comment := range []string{
		"# config.example.yaml — 配置模板",
		"# nginx 侧需要新增一段 internal location",
		"# 图片缩略图（Windows/Linux amd64 构建已内嵌 ffmpeg/ffprobe，无需系统安装）。",
	} {
		if !strings.Contains(text, comment) {
			t.Fatalf("示例配置注释丢失: %q", comment)
		}
	}

	// 注释逐条保留：出现次数与顺序都不能变（防止“重新编码把注释挪走/复制”）。
	if got, want := commentLines(text), commentLines(original); !reflect.DeepEqual(got, want) {
		t.Fatalf("注释行集合被改变:\n got %d 行\nwant %d 行\n%v\n%v", len(got), len(want), got, want)
	}
	// 换行风格保持原样（示例文件是 CRLF）：yaml.v3 对 CRLF 注释有“多插空行”的坑，
	// 解析前统一 LF、写回再恢复 CRLF 就是为了避开它。
	if !strings.Contains(text, "\r\n") {
		t.Fatal("更新后丢失 CRLF 换行风格")
	}
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		t.Fatal("更新后混入了裸 LF 换行")
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(更新后示例): %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("更新后的示例配置未通过校验: %v", err)
	}
	if cfg.Log.Level != "debug" || cfg.Server.Port != 8992 {
		t.Fatalf("更新没生效: level=%q port=%d", cfg.Log.Level, cfg.Server.Port)
	}
	if !reflect.DeepEqual(cfg.File.AllowedPaths, []string{"D:", "E:"}) {
		t.Fatalf("allowed_paths = %#v", cfg.File.AllowedPaths)
	}
	if cfg.QuickShare.MaxCapacityBytes != 5368709120 {
		t.Fatalf("quick_share.max_capacity_bytes = %d", cfg.QuickShare.MaxCapacityBytes)
	}
}

// TestUpdateValuesPreservesCommentsAndUnknownKeys 更新只改目标键：注释、未知键、其它值都保留。
func TestUpdateValuesPreservesCommentsAndUnknownKeys(t *testing.T) {
	clearMeshEnv(t)
	dir, path := writeEditorFixture(t, editorFixture)

	backup, err := UpdateValues(path, map[string]string{
		"log.level":                 "debug",
		"file.upload.max_file_size": "2048",
		"whitelist":                 "/v1/ping\n\n/v1/health\n",
		"auth.enabled":              "false",
	})
	if err != nil {
		t.Fatalf("UpdateValues: %v", err)
	}
	if backup == "" {
		t.Fatal("UpdateValues 应生成备份")
	}
	if got := readFileString(t, backup); got != editorFixture {
		t.Fatal("备份内容应等于保存前的原文")
	}
	if !regexp.MustCompile(`config\.yaml\.bak-\d{8}-\d{6}(-\d+)?$`).MatchString(backup) {
		t.Fatalf("备份文件名不符合约定: %s", backup)
	}

	text := readFileString(t, path)

	// 注释全部保留（顶层、段内、行尾）
	for _, comment := range []string{
		"# 顶层注释：数据库配置",
		"# 缓存配置",
		"# 输出格式",
		"# 最大文件大小 10GB（字节）",
		"# 仅 GUI 使用",
		"# forbidden_extensions:",
	} {
		if !strings.Contains(text, comment) {
			t.Fatalf("注释丢失: %q\n---\n%s", comment, text)
		}
	}
	// 未知键（gui 段）原样保留
	for _, raw := range []string{"gui:", "start_minimized: true"} {
		if !strings.Contains(text, raw) {
			t.Fatalf("未知键丢失: %q\n---\n%s", raw, text)
		}
	}

	// 仅目标键被改
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(更新后): %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("log.level = %q, want debug", cfg.Log.Level)
	}
	if cfg.File.Upload.MaxFileSize != 2048 {
		t.Fatalf("file.upload.max_file_size = %d, want 2048", cfg.File.Upload.MaxFileSize)
	}
	if !reflect.DeepEqual(cfg.Whitelist, []string{"/v1/ping", "/v1/health"}) {
		t.Fatalf("whitelist = %#v", cfg.Whitelist)
	}
	if cfg.Auth.Enabled {
		t.Fatal("auth.enabled 应被改为 false")
	}
	// 未改动的字段保持原值
	if cfg.File.Upload.MaxFilenameLength != 255 || cfg.Log.Format != "json" || cfg.Server.Port != 8991 {
		t.Fatalf("非目标字段被意外修改: %#v", cfg.File.Upload)
	}

	// 目录里不应残留临时文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

// TestUpdateValuesCreatesMissingKey 注释掉的键（forbidden_extensions）也能新增出来。
func TestUpdateValuesCreatesMissingKey(t *testing.T) {
	clearMeshEnv(t)
	_, path := writeEditorFixture(t, editorFixture)

	if _, err := UpdateValues(path, map[string]string{
		"file.upload.forbidden_extensions": ".exe\n.bat",
	}); err != nil {
		t.Fatalf("UpdateValues: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !reflect.DeepEqual(cfg.File.Upload.ForbiddenExtensions, []string{".exe", ".bat"}) {
		t.Fatalf("forbidden_extensions = %#v", cfg.File.Upload.ForbiddenExtensions)
	}
	// 其它键和注释仍在
	text := readFileString(t, path)
	if !strings.Contains(text, "# 白名单路由") || !strings.Contains(text, "max_filename_length: 255") {
		t.Fatalf("新增键时破坏了其它内容:\n%s", text)
	}
}

// TestUpdateValuesInvalidInputDoesNotWrite 非法 YAML / 校验失败 / 类型错误都不能落盘。
func TestUpdateValuesInvalidInputDoesNotWrite(t *testing.T) {
	clearMeshEnv(t)
	dir, path := writeEditorFixture(t, editorFixture)

	// 1) 已存在的文件本身是非法 YAML
	invalidPath := filepath.Join(dir, "invalid.yaml")
	invalid := "db: [\n"
	if err := os.WriteFile(invalidPath, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateValues(invalidPath, map[string]string{"log.level": "debug"}); err == nil {
		t.Fatal("非法 YAML 应返回错误")
	}
	if got := readFileString(t, invalidPath); got != invalid {
		t.Fatalf("非法 YAML 文件被改动: %q", got)
	}

	// 2) 值合法但校验不过（server.port=0）
	if _, err := UpdateValues(path, map[string]string{"server.port": "0"}); err == nil {
		t.Fatal("server.port=0 应校验失败")
	}
	if got := readFileString(t, path); got != editorFixture {
		t.Fatal("校验失败后原文件不应被改动")
	}

	// 3) 值类型不对（整数写成字符串）
	if _, err := UpdateValues(path, map[string]string{"auth.token_expire": "abc"}); err == nil {
		t.Fatal("非法整数应返回错误")
	}
	if got := readFileString(t, path); got != editorFixture {
		t.Fatal("类型错误后原文件不应被改动")
	}

	// 4) 不允许假装成功：所有失败都应该没有生成备份
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Fatalf("失败路径不应生成备份: %s", e.Name())
		}
	}
}

// TestSaveTextInvalidDoesNotWrite 整段原文保存同样先校验后落盘。
func TestSaveTextInvalidDoesNotWrite(t *testing.T) {
	clearMeshEnv(t)
	_, path := writeEditorFixture(t, editorFixture)

	// 语法非法
	if _, err := SaveText(path, "db: [\n"); err == nil {
		t.Fatal("非法 YAML 应返回错误")
	}
	if got := readFileString(t, path); got != editorFixture {
		t.Fatal("非法 YAML 不应落盘")
	}

	// 语法合法但缺必需段
	if _, err := SaveText(path, "server:\n  port: 8991\n"); err == nil {
		t.Fatal("缺少必需段的配置应校验失败")
	}
	if got := readFileString(t, path); got != editorFixture {
		t.Fatal("校验失败不应落盘")
	}

	// 合法内容：落盘 + 备份 + 保留原文注释
	newText := strings.Replace(editorFixture, "level: info", "level: warn", 1)
	backup, err := SaveText(path, newText)
	if err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if backup == "" {
		t.Fatal("SaveText 应生成备份")
	}
	if got := readFileString(t, path); got != newText {
		t.Fatal("SaveText 内容不是原文")
	}
	if got := readFileString(t, backup); got != editorFixture {
		t.Fatal("备份内容应等于保存前原文")
	}
	cfg, err := LoadFile(path)
	if err != nil || cfg.Log.Level != "warn" {
		t.Fatalf("保存后 LoadFile 不对: err=%v level=%q", err, cfg.Log.Level)
	}
}

// TestSaveTextCreatesFile 文件不存在时也能创建（无备份）。
func TestSaveTextCreatesFile(t *testing.T) {
	clearMeshEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	backup, err := SaveText(path, editorFixture)
	if err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if backup != "" {
		t.Fatalf("首次创建不应有备份，得到 %q", backup)
	}
	if got := readFileString(t, path); got != editorFixture {
		t.Fatal("首次创建内容不对")
	}
}
