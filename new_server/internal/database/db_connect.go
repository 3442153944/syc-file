package database

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/pkg/logger"
	gormlogger "gorm.io/gorm/logger"
)

// InitMySQL 初始化并返回一个 GORM DB 实例
func InitMySQL(cfg config.DBConfig) (*gorm.DB, error) {
	// 1. 动态拼接 DSN (Data Source Name)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Name,
	)

	logger.Logger.Info("正在连接数据库", zap.String("host", cfg.Host), zap.Int("port", cfg.Port))

	// 2. 连接数据库（注入自定义 Zap 日志适配器）
	gormLog := logger.NewGormLogger()
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: gormLog,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败: %w", err)
	}

	// 3. 配置企业级连接池 (极其重要)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层的 sql.DB 失败: %w", err)
	}

	// 设置空闲连接池中连接的最大数量
	sqlDB.SetMaxIdleConns(10)
	// 设置打开数据库连接的最大数量
	sqlDB.SetMaxOpenConns(100)
	// 设置了连接可复用的最大时间
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 设置 GORM 日志级别
	if logger.IsDebug() {
		db.Logger = gormLog.LogMode(gormlogger.Info)
	}

	logger.Logger.Info("MySQL 数据库连接成功！")

	return db, nil
}
