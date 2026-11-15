package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"syc-file/config"
)

// Logger 暴露一个全局的 Zap 实例，方便在业务代码中直接 logger.Logger.Info() 调用
var Logger *zap.Logger

// level 当前生效的日志级别。
//
// 用 zap.AtomicLevel（内部是 atomic.Int32）：读写都并发安全，运行期热改只有一个原子写，
// 已经创建好的 core 会立即跟随，不需要重建 Logger、也不需要加锁。
var level = zap.NewAtomicLevelAt(zap.InfoLevel)

// CurrentLevel 启动时的日志级别快照。
//
// Deprecated: 只反映 Init 那一刻的级别，热重载后不再更新；运行期判断请用 IsLevel / IsDebug / Level()。
// 保留它只是为了不破坏可能还在读它的旧代码。
var CurrentLevel zapcore.Level = zap.InfoLevel

// init 把“热改日志级别”和“写配置监听日志”两个钩子注册给 config 包。
//
// 依赖方向是 logger → config（logger 初始化需要读 config.LogConfig），
// 所以 config 里的 watcher 不能反过来 import logger；由这里反向注入，避免导入环。
func init() {
	config.RegisterLoggerHooks(SetLevel, func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if Logger != nil {
			Logger.Warn(msg)
			return
		}
		log.Printf("%s", msg)
	})
}

// Level 返回当前生效的日志级别（并发安全，可供 GUI / 状态接口展示）。
func Level() zapcore.Level {
	return level.Level()
}

// SetLevel 设置日志级别（运行期热改）。
//
// 名字与 zapcore.ParseLevel 一致：debug/info/warn/error/dpanic/panic/fatal（大小写不敏感）。
// 空字符串、解析失败都返回错误且不改变当前级别。
func SetLevel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("无效的日志级别 %q（不能为空，可选 debug/info/warn/error/dpanic/panic/fatal）", name)
	}
	lv, err := zapcore.ParseLevel(strings.ToLower(name))
	if err != nil {
		return fmt.Errorf("无效的日志级别 %q（可选 debug/info/warn/error/dpanic/panic/fatal）: %w", name, err)
	}
	level.SetLevel(lv)
	return nil
}

// IsLevel 检查当前日志级别是否满足给定的最低级别
func IsLevel(l zapcore.Level) bool {
	return level.Enabled(l)
}

// IsDebug 当前是否为 Debug 模式
func IsDebug() bool {
	return level.Enabled(zapcore.DebugLevel)
}

// Init 初始化日志组件
func Init(cfg config.LogConfig) error {
	// 1. 确保日志目录存在
	if err := os.MkdirAll(cfg.Path, 0744); err != nil {
		return err
	}

	// 2. 配置 Lumberjack 归档/轮转策略 (完全对接你的 YAML)
	lumberJackLogger := &lumberjack.Logger{
		Filename:   filepath.Join(cfg.Path, "server.log"),
		MaxSize:    cfg.MaxSize,   // 日志文件最大大小（MB）
		MaxBackups: cfg.MaxBackup, // 最大备份数量
		MaxAge:     cfg.MaxAge,    // 最大保留天数
		Compress:   false,         // 是否压缩 disabled by default
	}

	// 3. 设置日志输出格式 (JSON 还是 Console)
	var encoder zapcore.Encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder // 将时间格式化为人类可读的格式

	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// 4. 解析日志级别（Init 开始时尚未热改，直接改包级 AtomicLevel）
	logLevel, err := zapcore.ParseLevel(strings.ToLower(strings.TrimSpace(cfg.Level)))
	if err != nil {
		logLevel = zap.InfoLevel // 默认级别
	}
	level.SetLevel(logLevel)
	CurrentLevel = logLevel

	// 5. 配置输出位置 (同时输出到文件和控制台)。level 是 AtomicLevel，运行期 SetLevel 立即生效。
	var cores []zapcore.Core

	// 如果配置了输出到文件
	if cfg.File {
		fileCore := zapcore.NewCore(encoder, zapcore.AddSync(lumberJackLogger), level)
		cores = append(cores, fileCore)
	}

	// 如果配置了输出到控制台 (方便本地开发看日志)
	if cfg.Console {
		consoleCore := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)
		cores = append(cores, consoleCore)
	}

	// 6. 生成最终的 Logger
	core := zapcore.NewTee(cores...)
	Logger = zap.New(core, zap.AddCaller()) // AddCaller 可以打印出日志是在哪一行代码记录的

	// 替换 Zap 库内部的全局 logger，这样即使别人调用 zap.L() 也能用我们的配置
	zap.ReplaceGlobals(Logger)
	return nil
}
