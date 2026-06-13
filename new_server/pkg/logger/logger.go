package logger

import (
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"syc-file/config"
)

// Logger 暴露一个全局的 Zap 实例，方便在业务代码中直接 logger.Logger.Info() 调用
var Logger *zap.Logger

// CurrentLevel 记录当前生效的日志级别
var CurrentLevel zapcore.Level = zap.InfoLevel

// IsLevel 检查当前日志级别是否满足给定的最低级别
func IsLevel(l zapcore.Level) bool {
	return CurrentLevel <= l
}

// IsDebug 当前是否为 Debug 模式
func IsDebug() bool {
	return CurrentLevel <= zapcore.DebugLevel
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

	// 4. 配置输出位置 (同时输出到文件和控制台)
	var cores []zapcore.Core

	// 解析日志级别
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		level = zap.InfoLevel // 默认级别
	}
	CurrentLevel = level

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

	// 5. 生成最终的 Logger
	core := zapcore.NewTee(cores...)
	Logger = zap.New(core, zap.AddCaller()) // AddCaller 可以打印出日志是在哪一行代码记录的

	// 替换 Zap 库内部的全局 logger，这样即使别人调用 zap.L() 也能用我们的配置
	zap.ReplaceGlobals(Logger)
	return nil
}
