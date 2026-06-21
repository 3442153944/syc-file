package main

import (
	"context"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"
	"syc-file/config"
	"syc-file/internal/database"
	"syc-file/internal/handler"
	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/device_store"
	"syc-file/pkg/logger"
	"time"
)

func main() {
	// 1. 初始化配置 (Viper)
	if err := config.Init(); err != nil {
		panic("配置初始化失败: " + err.Error())
	}

	// 2. 初始化日志 (Zap + Lumberjack)
	// 将 config 模块中解析好的 Log 配置传给 logger 模块
	if err := logger.Init(config.Conf.Log); err != nil {
		panic("日志初始化失败: " + err.Error())
	}
	// 程序退出前刷新日志缓冲
	defer func(Logger *zap.Logger) {
		err := Logger.Sync()
		if err != nil {
			logger.Logger.Error("日志缓冲刷新失败", zap.Error(err))
		}
	}(logger.Logger)

	logger.Logger.Info("配置与日志初始化成功", zap.Int("port", config.Conf.Server.Port))

	// 3. 初始化 Gin 引擎
	// 以前是 r := gin.Default()，现在改为 gin.New()，并手动挂载我们的 Zap 中间件和默认的恢复中间件
	r := gin.New()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Token"},
		ExposeHeaders:    []string{"New-Token", "Token-Refreshed"},
		AllowCredentials: false,
		MaxAge:           86400 * time.Second,
	}))
	r.Use(middleware.ZapLogger(), gin.Recovery())

	//建立数据库连接
	db, err := database.InitMySQL(config.Conf.DB)
	if err != nil {
		logger.Logger.Error("数据库连接失败", zap.Error(err))
	}
	logger.Logger.Info("数据库连接成功")

	// 自动迁移数据库表结构
	if err := db.AutoMigrate(
		&model.User{},
		&model.Device{},
		&model.File{},
		&model.FileVersion{},
		&model.SyncTask{},
		&model.UploadHistory{},
		&model.DownloadHistory{},
		&model.Permission{},
		&model.Role{},
		&model.RolePermission{},
		&model.UserRole{},
		&model.DictType{},
		&model.DictData{},
		&model.OperationLog{},
		&model.StorageConfig{},
		&model.ShareRecord{},
	); err != nil {
		logger.Logger.Fatal("数据库迁移失败", zap.Error(err))
	}
	logger.Logger.Info("数据库表迁移完成")

	//建立缓存连接
	redisClient, err := database.InitRedis(config.Conf.Redis)
	if err != nil {
		logger.Logger.Error("缓存连接失败", zap.Error(err))
	}
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Logger.Fatal("Redis连接测试失败", zap.Error(err))
	}

	logger.Logger.Info("Redis连接成功")

	//初始化ws
	ws.InitWS(db)

	//初始化设备状态Redis存储
	device_store.Init(redisClient)

	// 4. 注册路由
	r.GET("/ping", func(c *gin.Context) {
		// 在业务代码里打印日志的正确姿势
		logger.Logger.Info("收到 ping 请求")
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	handler.RegisterRouters(r, db, redisClient)

	// 5. 启动服务
	addr := fmt.Sprintf(":%d", config.Conf.Server.Port)
	logger.Logger.Info("服务器准备启动", zap.String("addr", addr))

	if err := r.Run(addr); err != nil {
		logger.Logger.Fatal("服务器启动失败", zap.Error(err))
	}
}
