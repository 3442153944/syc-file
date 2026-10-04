package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"syc-file/internal/supervisor"
)

// Conf 全局配置实例
var Conf = new(Config)

// Version 服务端版本号，随 /v1/ping 回给客户端，用于客户端比对各节点版本是否一致。
const Version = "1.0.0"

// 运行模式。dev 即现有的直接启动方式（本机开发 / systemd 直跑二进制），prod 即 Docker 生产部署。
// 两者差异集中在「环境相关」的行为（Gin 模式、旧密钥文件导入、外部进程托管），业务逻辑完全一致。
const (
	ModeDev  = "dev"
	ModeProd = "prod"
)

// IsProd 是否生产（Docker）模式。
func IsProd() bool { return Conf.Server.Mode == ModeProd }

// Config 根节点配置，完全对齐你的 YAML
type Config struct {
	DB         DBConfig         `mapstructure:"db"`
	Log        LogConfig        `mapstructure:"log"`
	Whitelist  []string         `mapstructure:"whitelist"` // 白名单路由
	Auth       AuthConfig       `mapstructure:"auth"`
	Server     ServerConfig     `mapstructure:"server"`
	Redis      RedisConfig      `mapstructure:"redis"`
	File       FileConfig       `mapstructure:"file"`
	User       UserCfg          `mapstructure:"user"`
	Share      ShareConfig      `mapstructure:"share"`
	QuickShare QuickShareConfig `mapstructure:"quick_share"`
	Sync       SyncConfig       `mapstructure:"sync"`
	Supervisor supervisor.Config `mapstructure:"supervisor"`
	Monitor    MonitorConfig    `mapstructure:"monitor"`
}

// MonitorConfig 系统明细监控配置（进程/端口 Top-N 采集，见 internal/monitor）。
// 与之统计维度独立的系统级 CPU/内存趋势（1 分钟一次）不受这里影响。
type MonitorConfig struct {
	// 采集间隔（秒）。<=0 时按 30 处理。
	SysDetailIntervalSeconds int `mapstructure:"sys_detail_interval_seconds"`
	// 每轮记录的进程条数上限（按 cpu*2+mem*1.5+连接数*1 加权评分取前 N 个）。<=0 时按 20 处理。
	ProcessTopN int `mapstructure:"process_top_n"`
}

// DBConfig 数据库配置 (注意：这里将 uri 拆分为 host 和 port 以适配 GORM)
type DBConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

// RedisConfig 缓存配置
type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	DB       int    `mapstructure:"db"`
	Password string `mapstructure:"password"`
}

// LogConfig 日志配置
type LogConfig struct {
	Path      string `mapstructure:"path"`
	Level     string `mapstructure:"level"`
	Format    string `mapstructure:"format"`
	Console   bool   `mapstructure:"console"`
	File      bool   `mapstructure:"file"`
	MaxSize   int    `mapstructure:"max_size"`
	MaxAge    int    `mapstructure:"max_age"`
	MaxBackup int    `mapstructure:"max_backup"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	TokenExpire   int    `mapstructure:"token_expire"`
	RefreshExpire int    `mapstructure:"refresh_expire"`
	Secret        string `mapstructure:"secret"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int `mapstructure:"port"`
	// Mode 运行模式：dev（默认，开发 / 现有的直接启动方式）或 prod（Docker 生产部署）。
	// 环境变量 SYC_MODE 优先于配置文件；镜像里固定设为 prod。见 IsProd。
	Mode string `mapstructure:"mode"`
	// Name 节点名，会在 /v1/ping 里回给客户端。桌面端多节点灾备靠它显示
	// 「当前连的是哪个入口」；留空则退回主机名。
	Name string `mapstructure:"name"`
}

// FileConfig 文件存储配置
type FileConfig struct {
	AllowedPaths []string   `mapstructure:"allowed_paths"`
	Storage      StorageCfg `mapstructure:"storage"`
	Upload       UploadCfg  `mapstructure:"upload"`
}

// StorageCfg 存储目录配置
type StorageCfg struct {
	BasePath   string `mapstructure:"base_path"`
	UploadPath string `mapstructure:"upload_path"`
	TempPath   string `mapstructure:"temp_path"`
	TrashPath  string `mapstructure:"trash_path"`
}

// UploadCfg 上传配置
type UploadCfg struct {
	MaxFileSize         int64    `mapstructure:"max_file_size"`
	MaxFilenameLength   int      `mapstructure:"max_filename_length"`
	AllowedExtensions   []string `mapstructure:"allowed_extensions"`
	ForbiddenExtensions []string `mapstructure:"forbidden_extensions"`
}

// UserCfg 用户配置
type UserCfg struct {
	AvatarPath        string   `mapstructure:"avatar_path"`
	AllowedExtensions []string `mapstructure:"allowed_extensions"`
	MaxSize           int64    `mapstructure:"max_size"`
}

// ShareConfig 分享链接配置
type ShareConfig struct {
	TempPath             string `mapstructure:"temp_path"`
	MaxExpireMinutes     int    `mapstructure:"max_expire_minutes"`
	CleanIntervalMinutes int    `mapstructure:"clean_interval_minutes"`
}

// QuickShareConfig 粘贴快传配置：每用户一个自动分配的子目录 + 独立配额，小文件走内存不落盘
type QuickShareConfig struct {
	BasePath             string `mapstructure:"base_path"`
	MaxCapacityBytes     int64  `mapstructure:"max_capacity_bytes"`
	MemoryThresholdBytes int64  `mapstructure:"memory_threshold_bytes"`
	DefaultExpireMinutes int    `mapstructure:"default_expire_minutes"`
}

// SyncConfig 文件同步引擎配置
type SyncConfig struct {
	WorkerConcurrency   int    `mapstructure:"worker_concurrency"`
	MaxRetry            int    `mapstructure:"max_retry"`
	LockTTLSeconds      int    `mapstructure:"lock_ttl_seconds"`
	QueueTimeoutSeconds int    `mapstructure:"queue_timeout_seconds"`
	TaskTimeoutSeconds  int    `mapstructure:"task_timeout_seconds"`
	ConflictSuffix      string `mapstructure:"conflict_suffix"`
	SyncCatalogue       string `mapstructure:"sync_catalogue"`
}

// IsExtensionAllowed 检查文件扩展名是否允许上传
func (c *Config) IsExtensionAllowed(ext string) bool {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	for _, forbidden := range c.File.Upload.ForbiddenExtensions {
		if strings.ToLower(forbidden) == ext {
			return false
		}
	}
	if len(c.File.Upload.AllowedExtensions) == 0 {
		return true
	}
	for _, allowed := range c.File.Upload.AllowedExtensions {
		if strings.ToLower(allowed) == ext {
			return true
		}
	}
	return false
}

// IsPathAllowed 检查路径是否落在允许的盘符/目录前缀内（大小写不敏感）
func (c *Config) IsPathAllowed(path string) bool {
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return false
	}
	for _, allowed := range c.File.AllowedPaths {
		allowedClean := filepath.Clean(allowed)
		cleanUpper := strings.ToUpper(cleanPath)
		allowedUpper := strings.ToUpper(allowedClean)
		if len(allowedUpper) <= 3 && len(allowedUpper) >= 2 && allowedUpper[1] == ':' {
			drive := allowedUpper[:2]
			if strings.HasPrefix(cleanUpper, drive+`\`) || strings.HasPrefix(cleanUpper, drive+`/`) {
				return true
			}
			continue
		}
		if strings.HasPrefix(cleanUpper, allowedUpper) {
			rest := cleanUpper[len(allowedUpper):]
			if rest == "" || rest[0] == filepath.Separator {
				return true
			}
		}
	}
	return false
}

// GetAllowedPaths 获取允许的路径列表
func (c *Config) GetAllowedPaths() []string {
	return c.File.AllowedPaths
}

// Init 初始化 Viper 并解析 YAML
func Init() error {
	viper.SetConfigName("config") // 你的 yaml 文件名 (不带后缀)
	viper.SetConfigType("yaml")
	// 配置目录：环境变量 SYC_CONFIG_DIR 优先（容器里指向数据卷 /data/config，改配置不用动镜像），默认 ./config
	cfgDir := os.Getenv("SYC_CONFIG_DIR")
	if cfgDir == "" {
		cfgDir = "./config"
	}
	viper.AddConfigPath(cfgDir)

	// 开启环境变量覆盖机制 (非常重要：用于生产环境覆盖 Secret 等敏感信息)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := viper.Unmarshal(Conf); err != nil {
		return fmt.Errorf("解析配置到结构体失败: %w", err)
	}

	if v := strings.TrimSpace(os.Getenv("SYC_MODE")); v != "" {
		Conf.Server.Mode = v
	}
	Conf.Server.Mode = strings.ToLower(strings.TrimSpace(Conf.Server.Mode))
	switch Conf.Server.Mode {
	case "":
		Conf.Server.Mode = ModeDev
	case ModeDev, ModeProd:
	default:
		return fmt.Errorf("未知运行模式 %q（server.mode / SYC_MODE 只能是 dev 或 prod）", Conf.Server.Mode)
	}

	return nil
}
