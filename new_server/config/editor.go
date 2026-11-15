package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// 配置文件的权限与备份命名约定。
const (
	// configFileMode 配置文件（含备份、临时文件）的权限。
	configFileMode = 0o600
	// backupStampLayout 备份文件后缀的时间格式：config.yaml.bak-YYYYMMDD-HHMMSS。
	backupStampLayout = "20060102-150405"
)

// fileWriteMu 串行化“备份 + 原子替换”。
//
// 同一进程内 GUI 可能从多个 goroutine 保存配置，不加锁的话两个保存会互相覆盖备份、
// 交错写临时文件。跨进程不用锁：后端只读配置文件，写入方只有 GUI。
var fileWriteMu sync.Mutex

// LoadFile 读取并解析配置文件，归一化规则与 Init 一致（mode/vpn/SYC_MODE 等）。
//
// 只做“语法 + 归一化”，不做“必需段”校验；需要校验请再调 Validate。
func LoadFile(path string) (*Config, error) {
	text, err := ReadText(path)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadText(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// LoadText 解析 YAML 文本为 Config。
//
// 解码复用 viper 的 mapstructure 规则（与 Init 完全同一套 tag / 弱类型转换），
// 再跑一遍与 Init 相同的归一化，保证“磁盘上的配置”和“启动时读到的配置”一致。
func LoadText(text string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(text)); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}
	cfg := new(Config)
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("解析配置结构失败: %w", err)
	}
	if err := normalize(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ReadText 读取配置文件原文（保留注释与未知键）。
func ReadText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	return string(data), nil
}

// Validate 校验一份配置是否可用于运行。
//
// 覆盖 desktopapi 契约要求的“必需段（db/redis/auth/server）+ 可解析”，
// 失败时调用方（GUI 保存 / watcher 热重载）必须放弃落盘或放弃应用。
func Validate(c *Config) error {
	if c == nil {
		return errors.New("配置为空")
	}

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 无效: %d（应在 1-65535 之间）", c.Server.Port)
	}
	switch c.Server.Mode {
	case ModeDev, ModeProd:
	default:
		return fmt.Errorf("server.mode 无效: %q（只能是 dev 或 prod）", c.Server.Mode)
	}

	if strings.TrimSpace(c.DB.Host) == "" {
		return errors.New("缺少 db 段或 db.host 为空（数据库配置为必需段）")
	}
	if c.DB.Port <= 0 || c.DB.Port > 65535 {
		return fmt.Errorf("db.port 无效: %d（应在 1-65535 之间）", c.DB.Port)
	}

	if strings.TrimSpace(c.Redis.Host) == "" {
		return errors.New("缺少 redis 段或 redis.host 为空（缓存配置为必需段）")
	}
	if c.Redis.Port <= 0 || c.Redis.Port > 65535 {
		return fmt.Errorf("redis.port 无效: %d（应在 1-65535 之间）", c.Redis.Port)
	}
	if c.Redis.DB < 0 {
		return fmt.Errorf("redis.db 无效: %d（不能为负数）", c.Redis.DB)
	}

	if c.Auth.TokenExpire <= 0 {
		return fmt.Errorf("auth.token_expire 无效: %d（必须大于 0 天）", c.Auth.TokenExpire)
	}
	if c.Auth.RefreshExpire < 0 {
		return fmt.Errorf("auth.refresh_expire 无效: %d（不能为负数）", c.Auth.RefreshExpire)
	}

	if lv := strings.TrimSpace(strings.ToLower(c.Log.Level)); lv != "" {
		if _, ok := validLogLevels[lv]; !ok {
			return fmt.Errorf("log.level 无效: %q（可选 debug/info/warn/error/dpanic/panic/fatal）", c.Log.Level)
		}
	}
	if f := strings.TrimSpace(strings.ToLower(c.Log.Format)); f != "" && f != "json" && f != "console" {
		return fmt.Errorf("log.format 无效: %q（只能是 json 或 console）", c.Log.Format)
	}

	if c.File.Upload.MaxFileSize < 0 {
		return fmt.Errorf("file.upload.max_file_size 无效: %d（不能为负数）", c.File.Upload.MaxFileSize)
	}
	if c.File.Upload.MaxFilenameLength < 0 {
		return fmt.Errorf("file.upload.max_filename_length 无效: %d（不能为负数）", c.File.Upload.MaxFilenameLength)
	}

	return nil
}

// validLogLevels 与 zapcore.ParseLevel 支持的级别保持一致（配置层不 import zap，避免依赖泄漏）。
var validLogLevels = map[string]struct{}{
	"debug": {}, "info": {}, "warn": {}, "error": {}, "dpanic": {}, "panic": {}, "fatal": {},
}

// SaveText 用整段 YAML 原文覆盖保存配置文件。
//
// 流程：先解析 + 校验（失败原文件一个字节都不动）→ 备份 → 写临时文件 → rename 原子替换。
// 返回保存前生成的备份路径；首次创建（文件不存在）时备份路径为空。
func SaveText(path, text string) (backup string, err error) {
	cfg, err := LoadText(text)
	if err != nil {
		return "", err
	}
	if err := Validate(cfg); err != nil {
		return "", fmt.Errorf("配置校验失败: %w", err)
	}

	fileWriteMu.Lock()
	defer fileWriteMu.Unlock()

	backup, err = backupFile(path)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(text)); err != nil {
		return "", err
	}
	return backup, nil
}

// UpdateValues 按点分路径只改指定键，其余内容（注释、顺序、未知键）原样保留。
//
// values 的 key 是 ConfigField.Path（如 "log.level"、"file.upload.max_file_size"），
// value 是字符串表示：布尔 "true"/"false"，整数十进制，列表用换行分隔（契约见 desktopapi）。
// 保存前同样会跑一遍解析 + Validate，非法 YAML / 校验失败不落盘。
func UpdateValues(path string, values map[string]string) (backup string, err error) {
	if len(values) == 0 {
		return "", nil
	}

	fileWriteMu.Lock()
	defer fileWriteMu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	parsed, err := parseYAMLDoc(string(raw))
	if err != nil {
		return "", err
	}

	// 排序后逐项写入：同一次保存的落盘顺序稳定，出错时也便于定位是哪个字段。
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := setValueByPath(parsed.root, k, values[k]); err != nil {
			return "", err
		}
	}

	text, err := parsed.encode()
	if err != nil {
		return "", err
	}
	// 复检：改完的文本必须仍能被解析并通过校验，否则不落盘。
	newCfg, err := LoadText(text)
	if err != nil {
		return "", fmt.Errorf("更新后的配置无法解析: %w", err)
	}
	if err := Validate(newCfg); err != nil {
		return "", fmt.Errorf("配置校验失败: %w", err)
	}

	backup, err = backupFile(path)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(text)); err != nil {
		return "", err
	}
	return backup, nil
}

// ---------------------------------------------------------------------------
// yaml.Node 编辑
// ---------------------------------------------------------------------------

// yamlDoc 解析后的 YAML 文档。
//
// 额外记住原文件是不是 CRLF 换行：yaml.v3 的注释文本里，CRLF 会被记成额外的空行，
// 直接“解析→编码”会在每条注释之间插入空行。所以解析前统一转 LF，写回时再恢复 CRLF，
// 保证 Windows 上的配置文件保存后换行风格不变、注释格式不变。
type yamlDoc struct {
	doc  *yaml.Node
	root *yaml.Node
	crlf bool
}

// parseYAMLDoc 把配置文本解析成 yaml.Node 文档（空文件 / 只有注释的文件按空映射处理）。
func parseYAMLDoc(text string) (*yamlDoc, error) {
	crlf := strings.Contains(text, "\r\n")
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	var d yaml.Node
	if err := yaml.Unmarshal([]byte(normalized), &d); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}
	if d.Kind == 0 {
		// 空文件 / 只有注释的文件：yaml 不会生成文档节点，注释也不会进节点树，
		// 这里手工把开头的注释行收进 DocumentNode.HeadComment，保证保存后注释还在。
		d = yaml.Node{Kind: yaml.DocumentNode, HeadComment: leadingCommentLines(normalized)}
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		d.Content = []*yaml.Node{root}
		return &yamlDoc{doc: &d, root: root, crlf: crlf}, nil
	}
	if d.Kind != yaml.DocumentNode || len(d.Content) == 0 {
		return nil, errors.New("配置文件格式异常：缺少文档根节点")
	}
	root := d.Content[0]
	if root.Kind == yaml.MappingNode {
		return &yamlDoc{doc: &d, root: root, crlf: crlf}, nil
	}
	// 只写了注释（或只有一个空文档）时 yaml 会给一个 null 标量节点：当空映射处理。
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		mapping := &yaml.Node{
			Kind: yaml.MappingNode, Tag: "!!map",
			HeadComment: root.HeadComment, LineComment: root.LineComment, FootComment: root.FootComment,
		}
		d.Content[0] = mapping
		return &yamlDoc{doc: &d, root: mapping, crlf: crlf}, nil
	}
	return nil, errors.New("配置文件根节点必须是映射（key: value 结构）")
}

// leadingCommentLines 抽出文本开头的注释行（用于“只有注释的文件”这种 yaml 不生成节点的场景）。
func leadingCommentLines(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n")
}

// encode 把编辑后的节点重新序列化为 YAML 文本（2 空格缩进，与现有文件保持一致）。
//
// 必须编码 DocumentNode 而不是 Content[0]：yaml.v3 会把文件开头、与第一个键之间隔着
// 空行的注释块挂在 DocumentNode.HeadComment 上，只编码根映射会把这段“文件头注释”丢掉。
func (d *yamlDoc) encode() (string, error) {
	if d == nil || d.doc == nil || len(d.doc.Content) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.doc); err != nil {
		return "", fmt.Errorf("生成 YAML 失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("生成 YAML 失败: %w", err)
	}
	out := buf.String()
	if d.crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}

// setValueByPath 按 "a.b.c" 找到（或创建）映射链，并把叶子节点改成 value。
func setValueByPath(root *yaml.Node, path, value string) error {
	parts := strings.Split(path, ".")
	if len(parts) == 0 || strings.TrimSpace(path) == "" {
		return errors.New("字段路径不能为空")
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("字段路径非法: %q", path)
		}
	}

	mapping := root
	for i := 0; i < len(parts)-1; i++ {
		val, _, ok := mappingLookup(mapping, parts[i])
		if !ok {
			child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapping.Content = append(mapping.Content, keyNode(parts[i]), child)
			mapping = child
			continue
		}
		if val.Kind != yaml.MappingNode {
			return fmt.Errorf("字段 %q 已存在且不是映射，无法写入 %s", strings.Join(parts[:i+1], "."), path)
		}
		mapping = val
	}

	leafKey := parts[len(parts)-1]
	existing, _, ok := mappingLookup(mapping, leafKey)
	if !ok {
		newNode, err := buildValueNode(path, value, nil)
		if err != nil {
			return err
		}
		mapping.Content = append(mapping.Content, keyNode(leafKey), newNode)
		return nil
	}
	return replaceNodeValue(existing, path, value)
}

// mappingLookup 在映射节点的直接子项里按 key 精确查找（返回值节点与键节点）。
func mappingLookup(mapping *yaml.Node, key string) (value, keyNode *yaml.Node, ok bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], mapping.Content[i], true
		}
	}
	return nil, nil, false
}

// replaceNodeValue 就地替换叶子节点的值，保留它原有的注释（行尾注释等）。
func replaceNodeValue(existing *yaml.Node, path, value string) error {
	newNode, err := buildValueNode(path, value, existing)
	if err != nil {
		return err
	}
	// 注释属于“用户写的内容”，换值不能丢。
	newNode.HeadComment = existing.HeadComment
	newNode.LineComment = existing.LineComment
	newNode.FootComment = existing.FootComment
	// 类型没变时保留原有引号/块风格，减少无意义 diff（例如 "123456" 仍是双引号）。
	if existing.Kind == yaml.ScalarNode && newNode.Kind == yaml.ScalarNode && existing.Tag == newNode.Tag {
		newNode.Style = existing.Style
	}
	*existing = *newNode
	return nil
}

// buildValueNode 根据 Config 的字段类型（未知字段则参考旧节点类型）把字符串转成 YAML 节点。
func buildValueNode(path, value string, existing *yaml.Node) (*yaml.Node, error) {
	if t := configFieldType(path); t != nil {
		switch t.Kind() {
		case reflect.Struct:
			// 例如 "file.upload"、"supervisor"：这是配置段不是叶子，直接赋值会把整段覆盖掉。
			return nil, fmt.Errorf("字段 %s 是配置段（包含子字段），不能直接赋值", path)
		case reflect.Slice, reflect.Array:
			return buildSequenceNode(value), nil
		case reflect.Bool:
			b, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("字段 %s 需要布尔值（true/false），收到 %q", path, value)
			}
			return scalarNode("!!bool", strconv.FormatBool(b)), nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("字段 %s 需要整数，收到 %q", path, value)
			}
			return scalarNode("!!int", strconv.FormatInt(n, 10)), nil
		default:
			return scalarNode("!!str", value), nil
		}
	}

	// Config 里没有的扩展段（如 gui.*）：能读到旧节点就沿用旧类型，否则按字符串写。
	if existing != nil {
		switch {
		case existing.Kind == yaml.MappingNode:
			return nil, fmt.Errorf("字段 %s 是配置段（包含子字段），不能直接赋值", path)
		case existing.Kind == yaml.SequenceNode:
			return buildSequenceNode(value), nil
		case existing.Tag == "!!bool":
			b, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("字段 %s 需要布尔值（true/false），收到 %q", path, value)
			}
			return scalarNode("!!bool", strconv.FormatBool(b)), nil
		case existing.Tag == "!!int":
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("字段 %s 需要整数，收到 %q", path, value)
			}
			return scalarNode("!!int", strconv.FormatInt(n, 10)), nil
		}
	}
	return scalarNode("!!str", value), nil
}

// buildSequenceNode 把“一行一项”的字符串转成 YAML 序列节点。
func buildSequenceNode(value string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seq.Content = append(seq.Content, scalarNode("!!str", line))
	}
	return seq
}

func scalarNode(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

func keyNode(key string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
}

// configFieldType 按点分路径取 Config 对应字段的 reflect.Type；未知路径返回 nil。
func configFieldType(path string) reflect.Type {
	parts := strings.Split(path, ".")
	if len(parts) == 0 || strings.TrimSpace(path) == "" {
		return nil
	}
	t := reflect.TypeOf(Config{})
	for _, part := range parts {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil
		}
		f, ok := structFieldByKey(t, part)
		if !ok {
			return nil
		}
		t = f.Type
	}
	return t
}

// structFieldByKey 按 mapstructure tag（缺省为小写字段名）查找结构体字段。
func structFieldByKey(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := t.Field(i).Name
		if tag, ok := f.Tag.Lookup("mapstructure"); ok {
			tag = strings.Split(tag, ",")[0]
			if tag == "-" {
				continue
			}
			if tag != "" {
				name = tag
			}
		}
		if strings.EqualFold(name, key) || strings.EqualFold(t.Field(i).Name, key) {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// ---------------------------------------------------------------------------
// 备份与原子写盘
// ---------------------------------------------------------------------------

// backupFile 把当前文件内容复制到 config.yaml.bak-YYYYMMDD-HHMMSS（同目录、0600）。
// 同一秒内多次保存时追加序号，避免前一次备份被覆盖。文件不存在时返回空路径（首次创建）。
func backupFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("读取待备份配置失败: %w", err)
	}
	stamp := time.Now().Format(backupStampLayout)
	backup := path + ".bak-" + stamp
	for i := 1; ; i++ {
		if _, statErr := os.Stat(backup); os.IsNotExist(statErr) {
			break
		}
		backup = fmt.Sprintf("%s.bak-%s-%d", path, stamp, i)
	}
	if err := os.WriteFile(backup, data, configFileMode); err != nil {
		return "", fmt.Errorf("备份配置文件失败: %w", err)
	}
	return backup, nil
}

// writeFileAtomic 先写同目录临时文件（0600），再 rename 原子替换目标文件。
// 这样即使写到一半进程崩溃，磁盘上也只会是“旧的完整文件”或“新的完整文件”。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时配置文件失败: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时配置文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("刷新临时配置文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时配置文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, configFileMode); err != nil {
		return fmt.Errorf("设置配置文件权限失败: %w", err)
	}
	if err := renameWithRetry(tmpName, path); err != nil {
		return fmt.Errorf("原子替换配置文件失败: %w", err)
	}
	cleanup = false
	return nil
}

// renameWithRetry Windows 下杀毒软件 / 另一个进程短暂持有文件句柄时 rename 会失败，稍等重试。
func renameWithRetry(oldPath, newPath string) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = os.Rename(oldPath, newPath); err == nil {
			return nil
		}
		time.Sleep(time.Duration(20*(i+1)) * time.Millisecond)
	}
	return err
}
