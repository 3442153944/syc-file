package app

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"syc-file/config"
	"syc-file/internal/desktopapi"
)

// 本文件把 config.Config 反射成「桌面控制台可编辑字段表」：
//   - 路径用点分（与 config.yaml 嵌套一致），保存走 config.UpdateValues（保注释 + 备份 + 原子写）；
//   - 分组/中文标签/说明/下拉候选是人工维护的小表，没列到的字段用路径兜底，不会漏；
//   - 热字段标记直接问 config.IsHot，避免 GUI 自己维护一份容易漂移的清单。

var configFieldGroups = map[string]string{
	"server":      "服务",
	"log":         "日志",
	"monitor":     "监控",
	"file":        "上传与文件",
	"user":        "用户",
	"share":       "分享",
	"quick_share": "快传",
	"sync":        "同步",
	"supervisor":  "外部进程",
	"thumbnail":   "缩略图",
	"vpn":         "组网",
	"auth":        "认证",
	"redis":       "Redis",
	"db":          "数据库",
	"whitelist":   "安全",
	"gui":         "桌面控制台",
}

// configGroupOrder 控制表单分组顺序（未列出的分组按名字排在后面）。
var configGroupOrder = []string{
	"服务", "日志", "监控", "上传与文件", "用户", "分享", "快传",
	"同步", "外部进程", "缩略图", "组网", "认证", "Redis", "数据库", "安全", "桌面控制台",
}

var configFieldLabels = map[string]string{
	"server.port":                         "HTTP 端口",
	"server.mode":                         "运行模式",
	"server.name":                         "节点名称",
	"log.path":                            "日志目录",
	"log.level":                           "日志级别",
	"log.format":                          "日志格式",
	"log.console":                         "输出到控制台",
	"log.file":                            "输出到文件",
	"log.max_size":                        "单文件上限(MB)",
	"log.max_age":                         "保留天数",
	"log.max_backup":                      "保留份数",
	"monitor.sys_detail_interval_seconds": "明细采集间隔(秒)",
	"monitor.process_top_n":               "进程 Top-N",
	"file.allowed_paths":                  "允许的根路径",
	"file.storage.base_path":              "存储根目录",
	"file.storage.upload_path":            "上传目录",
	"file.storage.temp_path":              "临时目录",
	"file.storage.trash_path":             "回收站目录",
	"file.upload.max_file_size":           "单文件上限(字节)",
	"file.upload.max_filename_length":     "文件名长度上限",
	"file.upload.allowed_extensions":      "允许的扩展名",
	"file.upload.forbidden_extensions":    "禁止的扩展名",
	"user.avatar_path":                    "头像目录",
	"user.allowed_extensions":             "用户允许的扩展名",
	"user.max_size":                       "用户空间上限(字节)",
	"share.temp_path":                     "分享临时目录",
	"share.max_expire_minutes":            "分享最长有效期(分)",
	"share.clean_interval_minutes":        "分享清理间隔(分)",
	"quick_share.base_path":               "快传存储目录",
	"quick_share.max_capacity_bytes":      "快传总容量上限",
	"quick_share.memory_threshold_bytes":  "快传内存阈值",
	"quick_share.default_expire_minutes":  "快传默认有效期(分)",
	"sync.worker_concurrency":             "同步并发数",
	"sync.max_retry":                      "同步最大重试",
	"sync.lock_ttl_seconds":               "同步锁 TTL(秒)",
	"sync.queue_timeout_seconds":          "队列超时(秒)",
	"sync.task_timeout_seconds":           "任务超时(秒)",
	"sync.conflict_suffix":                "冲突文件名后缀",
	"sync.sync_catalogue":                 "同步目录清单文件",
	"supervisor.enabled":                  "启用外部进程托管",
	"supervisor.log_dir":                  "托管日志目录",
	"thumbnail.disabled":                  "关闭缩略图",
	"thumbnail.dir":                       "缩略图持久目录",
	"thumbnail.temp_dir":                  "缩略图临时目录",
	"thumbnail.temp_ttl_minutes":          "缩略图临时 TTL(分)",
	"thumbnail.workers":                   "缩略图并发数",
	"thumbnail.default_width":             "缩略图默认宽度",
	"thumbnail.no_backfill":               "启动时不回填缩略图",
	"thumbnail.ffmpeg":                    "ffmpeg 路径",
	"thumbnail.ffprobe":                   "ffprobe 路径",
	"vpn.pool":                            "组网地址池",
	"vpn.stun_port":                       "STUN 端口",
	"vpn.server_address":                  "服务端组网地址",
	"vpn.server_pubkey":                   "服务端 WireGuard 公钥",
	"vpn.server_endpoint":                 "服务端公网端点",
	"vpn.server_lan_endpoints":            "服务端 LAN 端点",
	"vpn.server_wg_port":                  "服务端 WireGuard 端口",
	"vpn.server_interface":                "服务端 WireGuard 接口名",
	"auth.enabled":                        "启用鉴权",
	"auth.token_expire":                   "Token 有效期(分)",
	"auth.refresh_expire":                 "Refresh 有效期(分)",
	"auth.secret":                         "JWT 密钥",
	"redis.host":                          "Redis 地址",
	"redis.port":                          "Redis 端口",
	"redis.db":                            "Redis 库号",
	"redis.password":                      "Redis 密码",
	"db.host":                             "数据库地址",
	"db.port":                             "数据库端口",
	"db.name":                             "数据库名",
	"db.user":                             "数据库用户",
	"db.password":                         "数据库密码",
	"whitelist":                           "IP 白名单",
	"gui.enabled":                         "启动后端时自动拉起控制台",
	"gui.ui_path":                         "控制台程序路径",
	"gui.start_minimized":                 "启动时最小化到托盘",
	"gui.backend_path":                    "后端程序路径",
	"gui.backend_workdir":                 "后端工作目录",
}

var configFieldDescriptions = map[string]string{
	"server.port":             "后端 HTTP/WebSocket 监听端口；改动后需要重启。",
	"server.mode":             "dev=本机/直跑；prod=Docker 生产部署（环境变量 SYC_MODE 优先）。",
	"server.name":             "返回给客户端的节点标识；留空使用主机名。",
	"log.level":               "改动即时生效；调 debug 会放大日志体积。",
	"log.format":              "json 便于采集，console 更适合人工看；需要重启。",
	"file.allowed_paths":      "同步与下载只在这些目录内生效；一行一个目录前缀。",
	"file.allowed_extensions": "一行一个扩展名（含点号），留空不限制。",
	"auth.secret":             "JWT 签名密钥；改动后所有已签发 Token 失效。",
	"db.host":                 "MySQL 地址；改动后需要重启。",
	"redis.host":              "Redis 地址；改动后需要重启。",
	"gui.enabled":             "后端启动成功后自动在登录用户桌面拉起控制台；Docker/systemd 部署建议关闭。",
	"gui.ui_path":             "留空 = 与后端同目录的 syc-file-gui.exe；相对路径按后端 exe 目录解析。",
	"gui.start_minimized":     "控制台启动时只驻留托盘，不弹窗口（控制台自己读取）。",
}

var configFieldOptions = map[string][]string{
	"server.mode": {"dev", "prod"},
	"log.level":   {"debug", "info", "warn", "error"},
	"log.format":  {"json", "console"},
}

// ConfigFields 返回配置表单字段目录（当前生效值）。
func (a *App) ConfigFields() (desktopapi.ConfigView, error) {
	cfg := config.Live()
	if cfg == nil {
		cfg = config.Conf
	}
	if cfg == nil {
		return desktopapi.ConfigView{}, fmt.Errorf("配置尚未初始化")
	}
	fields := collectConfigFields(cfg)
	return desktopapi.ConfigView{Path: a.ConfigPath(), Fields: fields}, nil
}

// SaveConfigFields 保存一组字段修改，返回备份路径与热/冷分类。
func (a *App) SaveConfigFields(values map[string]string) (desktopapi.SaveFieldsResult, error) {
	view, err := a.ConfigFields()
	if err != nil {
		return desktopapi.SaveFieldsResult{}, err
	}
	allowed := make(map[string]struct{}, len(view.Fields))
	for _, field := range view.Fields {
		if !field.ReadOnly {
			allowed[field.Path] = struct{}{}
		}
	}
	filtered := make(map[string]string, len(values))
	hot := make([]string, 0, len(values))
	cold := make([]string, 0, len(values))
	for path, value := range values {
		if _, ok := allowed[path]; !ok {
			return desktopapi.SaveFieldsResult{}, fmt.Errorf("未知或只读的配置项: %s", path)
		}
		filtered[path] = value
		if config.IsHot(path) {
			hot = append(hot, path)
		} else {
			cold = append(cold, path)
		}
	}
	if len(filtered) == 0 {
		return desktopapi.SaveFieldsResult{}, fmt.Errorf("没有需要保存的字段")
	}
	backup, err := config.UpdateValues(a.ConfigPath(), filtered)
	if err != nil {
		return desktopapi.SaveFieldsResult{}, err
	}
	sort.Strings(hot)
	sort.Strings(cold)
	return desktopapi.SaveFieldsResult{BackupPath: backup, HotApplied: hot, NeedRestart: cold}, nil
}

// collectConfigFields 反射遍历配置结构，生成按「分组 → 路径」排序的字段列表。
func collectConfigFields(cfg *config.Config) []desktopapi.ConfigField {
	var fields []desktopapi.ConfigField
	walkConfigValue(reflect.ValueOf(cfg).Elem(), "", &fields)
	groupIndex := make(map[string]int, len(configGroupOrder))
	for i, name := range configGroupOrder {
		groupIndex[name] = i
	}
	sort.SliceStable(fields, func(i, j int) bool {
		gi, iok := groupIndex[fields[i].Group]
		if !iok {
			gi = len(configGroupOrder)
		}
		gj, jok := groupIndex[fields[j].Group]
		if !jok {
			gj = len(configGroupOrder)
		}
		if gi != gj {
			return gi < gj
		}
		if fields[i].Group != fields[j].Group {
			return fields[i].Group < fields[j].Group
		}
		return fields[i].Path < fields[j].Path
	})
	return fields
}

func walkConfigValue(value reflect.Value, prefix string, out *[]desktopapi.ConfigField) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			structField := typ.Field(i)
			if structField.PkgPath != "" { // 未导出
				continue
			}
			tag := structField.Tag.Get("mapstructure")
			if tag == "" || tag == "-" {
				tag = strings.ToLower(structField.Name[:1]) + structField.Name[1:]
			}
			child := tag
			if prefix != "" {
				child = prefix + "." + tag
			}
			walkConfigValue(value.Field(i), child, out)
		}
	case reflect.Slice:
		// 只把 []string 当可编辑列表；[]struct（如 supervisor.processes）跳过，避免拼出不可逆的文本。
		if value.Type().Elem().Kind() != reflect.String {
			return
		}
		items := make([]string, 0, value.Len())
		for i := 0; i < value.Len(); i++ {
			items = append(items, value.Index(i).String())
		}
		*out = append(*out, newConfigField(prefix, strings.Join(items, "\n"), desktopapi.KindList))
	case reflect.String:
		*out = append(*out, newConfigField(prefix, value.String(), ""))
	case reflect.Bool:
		*out = append(*out, newConfigField(prefix, strconv.FormatBool(value.Bool()), desktopapi.KindBool))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		*out = append(*out, newConfigField(prefix, strconv.FormatInt(value.Int(), 10), desktopapi.KindInt))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		*out = append(*out, newConfigField(prefix, strconv.FormatUint(value.Uint(), 10), desktopapi.KindInt))
	case reflect.Float32, reflect.Float64:
		*out = append(*out, newConfigField(prefix, strconv.FormatFloat(value.Float(), 'f', -1, 64), ""))
	}
}

// newConfigField 根据路径和类型补全标签/分组/选项/掩码。
func newConfigField(path, value string, kind desktopapi.FieldKind) desktopapi.ConfigField {
	field := desktopapi.ConfigField{
		Path:        path,
		Label:       configFieldLabels[path],
		Group:       configFieldGroup(path),
		Value:       value,
		Kind:        kind,
		Options:     configFieldOptions[path],
		Description: configFieldDescriptions[path],
		HotReload:   config.IsHot(path),
	}
	if field.Label == "" {
		field.Label = path
	}
	if len(field.Options) > 0 && field.Kind == "" {
		field.Kind = desktopapi.KindSelect
	}
	if field.Kind == "" {
		field.Kind = desktopapi.KindString
	}
	if isSecretConfigPath(path) {
		field.Kind = desktopapi.KindSecret
		field.Value = "********"
	}
	return field
}

func configFieldGroup(path string) string {
	top := path
	if idx := strings.IndexByte(path, '.'); idx >= 0 {
		top = path[:idx]
	}
	if group, ok := configFieldGroups[top]; ok {
		return group
	}
	return "其它"
}

func isSecretConfigPath(path string) bool {
	last := path
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 {
		last = path[idx+1:]
	}
	last = strings.ToLower(last)
	return strings.Contains(last, "password") || strings.Contains(last, "secret")
}
