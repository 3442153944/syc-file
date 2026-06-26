package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Conf 全局配置实例
var Conf = new(Config)

// Config 根节点配置，完全对齐你的 YAML
type Config struct {
	DB        DBConfig     `mapstructure:"db"`
	Log       LogConfig    `mapstructure:"log"`
	Whitelist []string     `mapstructure:"whitelist"` // 白名单路由
	Auth      AuthConfig   `mapstructure:"auth"`
	Server    ServerConfig `mapstructure:"server"`
	Redis     RedisConfig  `mapstructure:"redis"`
	File      FileConfig   `mapstructure:"file"`
	User      UserCfg      `mapstructure:"user"`
	Sync      SyncConfig   `mapstructure:"sync"`
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
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	DB   int    `mapstructure:"db"`
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

// SyncConfig 文件同步引擎配置
type SyncConfig struct {
	WorkerConcurrency   int    `mapstructure:"worker_concurrency"`
	MaxRetry            int    `mapstructure:"max_retry"`
	LockTTLSeconds      int    `mapstructure:"lock_ttl_seconds"`
	QueueTimeoutSeconds int    `mapstructure:"queue_timeout_seconds"`
	TaskTimeoutSeconds  int    `mapstructure:"task_timeout_seconds"`
	ConflictSuffix      string `mapstructure:"conflict_suffix"`
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
	viper.AddConfigPath("./config") // 假设配置文件在项目根目录

	// 开启环境变量覆盖机制 (非常重要：用于生产环境覆盖 Secret 等敏感信息)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := viper.Unmarshal(Conf); err != nil {
		return fmt.Errorf("解析配置到结构体失败: %w", err)
	}

	return nil
}
