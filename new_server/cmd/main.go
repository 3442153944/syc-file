package main

import (
	"context"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syc-file/config"
	"syc-file/internal/database"
	"syc-file/internal/handler"
	filehandler "syc-file/internal/handler/file"
	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/internal/monitor"
	"syc-file/internal/sync"
	"syc-file/internal/ws"
	"syc-file/pkg/device_store"
	"syc-file/pkg/logger"
	"syc-file/pkg/upload_store"
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
		&model.SyncConflict{},
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
		&model.SyncFolder{},
		&model.AppRelease{},
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

	//初始化监控推送器（注册 WS monitor 处理器）
	monitor.InitBroadcaster()

	//初始化设备状态Redis存储
	device_store.Init(redisClient)

	//初始化分片上传会话Redis存储
	upload_store.Init(redisClient)

	//启动临时文件清理器（清理过期上传遗留的 .part）
	filehandler.StartTempJanitor()

	//初始化文件同步引擎（Redis队列 + worker）
	syncEngine := sync.InitSync(db, redisClient, config.Conf.Sync)

	// 4. 注册路由
	r.GET("/ping", func(c *gin.Context) {
		// 在业务代码里打印日志的正确姿势
		logger.Logger.Info("收到 ping 请求")
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	// 操作日志：记录写操作（谁、做了什么、成没成功）到 operation_log 表，供管理端查询。
	// 必须在 RegisterRouters 之前 Use，且要在 db 就绪之后（所以没法和上面的中间件写在一起）。
	r.Use(middleware.OperationLogger(db))

	// 头像等静态资源。DB 里 user.avatar 存的是相对路径（默认 static/avatar/xxx.png），
	// 客户端直接用「服务器根 + 该相对路径」取图，所以这里的挂载点必须和 avatar_path 逐字一致。
	// 此前服务端根本没挂静态路由，头像能不能显示全看前面的反向代理有没有单独配 —— 换个入口
	// （不同端口/不同前缀）就 404。挂上之后任何能打到本服务的入口都取得到。
	if avatarPath := config.Conf.User.AvatarPath; avatarPath != "" {
		rel := strings.Trim(filepath.ToSlash(avatarPath), "/")
		wd, _ := os.Getwd()
		r.Static("/"+rel, filepath.Join(wd, filepath.FromSlash(rel)))
		logger.Logger.Info("静态资源已挂载", zap.String("url", "/"+rel), zap.String("dir", filepath.Join(wd, filepath.FromSlash(rel))))
	}

	handler.RegisterRouters(r, db, redisClient, syncEngine)

	// 5. 启动服务
	addr := fmt.Sprintf(":%d", config.Conf.Server.Port)
	logger.Logger.Info("服务器准备启动", zap.String("addr", addr))

	if err := r.Run(addr); err != nil {
		logger.Logger.Fatal("服务器启动失败", zap.Error(err))
	}
}
