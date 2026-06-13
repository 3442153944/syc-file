package logger

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// GormLogger 基于 Zap 的 GORM 日志适配器
type GormLogger struct {
	SlowThreshold time.Duration
}

func NewGormLogger() *GormLogger {
	return &GormLogger{
		SlowThreshold: 200 * time.Millisecond,
	}
}

func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return l
}

func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if IsDebug() {
		Logger.Info("GORM", zap.String("msg", msg), zap.Any("data", data))
	}
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	Logger.Warn("GORM", zap.String("msg", msg), zap.Any("data", data))
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	Logger.Error("GORM", zap.String("msg", msg), zap.Any("data", data))
}

func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	fields := []zap.Field{
		zap.String("sql", sql),
		zap.Int64("rows", rows),
		zap.Duration("elapsed", elapsed),
	}

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		fields = append(fields, zap.Error(err))
		Logger.Error("GORM SQL错误", fields...)
		return
	case err != nil:
		Logger.Debug("GORM 未找到记录", fields...) // RecordNotFound 只打 Debug
		return
	}

	// Debug 模式：输出所有 SQL
	if IsDebug() {
		Logger.Debug("GORM SQL", fields...)
		return
	}

	// Info 模式：仅记录慢查询
	if IsLevel(zapcore.InfoLevel) && elapsed > l.SlowThreshold {
		Logger.Info("GORM 慢查询", fields...)
	}

	// 其他模式：不记录 SQL
}
