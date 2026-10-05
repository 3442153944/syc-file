package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syc-file/config"
	"syc-file/internal/database"
	"syc-file/internal/handler"
	filehandler "syc-file/internal/handler/file"
	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/internal/monitor"
	"syc-file/internal/supervisor"
	"syc-file/internal/sync"
	"syc-file/internal/system"
	"syc-file/internal/ws"
	"syc-file/pkg/device_store"
	"syc-file/pkg/logger"
	"syc-file/pkg/procpriority"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
	"syscall"
	"time"
)

// startupWaitTimeout 启动时等待 MySQL / Redis 就绪的最长时间（开机自启时它们的容器比本服务晚几秒才可用）。
const startupWaitTimeout = 90 * time.Second

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

	logger.Logger.Info("配置与日志初始化成功",
		zap.Int("port", config.Conf.Server.Port), zap.String("mode", config.Conf.Server.Mode))

	if config.IsProd() {
		// 生产不打印路由表 / debug 日志
		gin.SetMode(gin.ReleaseMode)
	}

	// 尽量提高本进程的调度优先级：资源告警/进程采集的价值就在于系统被打满
	// 时还能看清状况，自己跟普通进程一个优先级会在系统最忙的时候反而被
	// 调度器晾在一边，见 pkg/procpriority 的注释
	procpriority.Raise()

	// 3. 初始化 Gin 引擎
	// 以前是 r := gin.Default()，现在改为 gin.New()，并手动挂载我们的 Zap 中间件和默认的恢复中间件
	r := gin.New()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Token", "Device-Id"},
		ExposeHeaders:    []string{"New-Token", "Token-Refreshed"},
		AllowCredentials: false,
		MaxAge:           86400 * time.Second,
	}))
	r.Use(middleware.ZapLogger(), gin.Recovery())

	//建立数据库连接
	// 开机时 systemd 只保证 docker.service 起来了，MySQL / Redis 容器还要再等几秒才就绪。
	// 以前这里连不上只记一条日志、拿着空的 db 继续往下走，随后在 AutoMigrate 处空指针 panic，
	// 靠 Restart=always 重启碰巧成功——每次开机日志里都有一次崩溃。现在等它就绪，真连不上才明确退出。
	var db *gorm.DB
	var err error
	if err = database.Retry("MySQL", startupWaitTimeout, func() error {
		var e error
		db, e = database.InitMySQL(config.Conf.DB)
		return e
	}); err != nil {
		logger.Logger.Fatal("数据库连接失败", zap.Error(err))
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
		&model.MonitorHistory{},
		&model.ProcessHistory{},
		&model.ResourceAlert{},
		&model.ListeningPortHistory{},
		&model.PortConnHistory{},
		&model.AppSetting{},
		&model.Route{},
		&model.UserRoute{},
	); err != nil {
		logger.Logger.Fatal("数据库迁移失败", zap.Error(err))
	}
	logger.Logger.Info("数据库表迁移完成")

	// 升级旧管理员 → 超级管理员、播种路由、判定是否需要初始化（见 internal/system）
	if err := system.Bootstrap(db); err != nil {
		logger.Logger.Fatal("系统初始化检查失败", zap.Error(err))
	}

	// share_link 历来靠 sql/share_link.sql 手工建表、不进 AutoMigrate（避免动已有表的索引）；
	// 全新库（例如容器首次启动）没有这张表，这里只在「不存在」时创建，已有的表一律不碰。
	if !db.Migrator().HasTable(&model.ShareLink{}) {
		if err := db.Migrator().CreateTable(&model.ShareLink{}); err != nil {
			logger.Logger.Fatal("创建 share_link 表失败", zap.Error(err))
		}
		logger.Logger.Info("已创建 share_link 表")
	}

	// JWT 密钥存数据库：首次启动自动生成，老部署会从旧 venv/key.yaml 导入（见 pkg/token/secret.go）
	if err := token.InitSecret(db, !config.IsProd()); err != nil {
		logger.Logger.Fatal("JWT 密钥初始化失败", zap.Error(err))
	}

	//建立缓存连接
	var redisClient *redis.Client
	if err := database.Retry("Redis", startupWaitTimeout, func() error {
		var e error
		redisClient, e = database.InitRedis(config.Conf.Redis)
		return e
	}); err != nil {
		logger.Logger.Fatal("缓存连接失败", zap.Error(err))
	}
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Logger.Fatal("Redis连接测试失败", zap.Error(err))
	}

	logger.Logger.Info("Redis连接成功")

	//初始化ws
	ws.InitWS(db)

	//初始化监控推送器（注册 WS monitor 处理器）
	monitor.InitBroadcaster()

	//监控历史记录：独立于 WS 推送常驻采样，写 Redis（7~8 天热数据）供仪表盘画趋势图；
	//每天再把前一天的数据批量归档进 MySQL，长期保存
	monitor.Init(redisClient, db)
	monitor.StartHistoryRecorder()
	monitor.StartDailyArchiver()

	//进程/端口明细采集：间隔见 config.monitor.sys_detail_interval_seconds（默认30s），
	//Rust 侧 sysinfo+netstat2 采集（见 file_lib/src/sys_info.rs），存储策略同上
	monitor.StartSysDetailRecorder()
	monitor.StartSysDetailArchiver()

	//资源告警巡检：独立于上面的历史采集 ticker，固定 3s 基线扫一次单进程 CPU 占比，
	//越过阈值连续几次才告警，见 internal/monitor/resource_alert.go
	monitor.StartResourceAlertWatcher()

	//监控历史长期表降采样清理：1 个月以上降到按小时、1 年以上降到按天，
	//不然 process_history 这类明细表会无限膨胀，见 internal/monitor/retention.go
	monitor.StartRetentionCleaner()

	//初始化设备状态Redis存储
	device_store.Init(redisClient)

	//初始化分片上传会话Redis存储
	upload_store.Init(redisClient)

	//启动临时文件清理器（清理过期上传遗留的 .part）
	filehandler.StartTempJanitor()

	//启动分享链接清理器（30 分钟兜底扫描；每条链接另有独立协程到期自毁）
	filehandler.StartShareLinkJanitor(db, redisClient)

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

	// 分享链接临时目录：与 nginx /temp/ 的 alias 保持一致。
	// 挂载后任何直达本服务的入口（Tauri 本地、无 nginx 的开发环境）都能取到分享文件。
	if tempPath := config.Conf.Share.TempPath; tempPath != "" {
		if err := os.MkdirAll(tempPath, 0o755); err == nil {
			r.Static("/temp", tempPath)
			logger.Logger.Info("分享临时目录已挂载", zap.String("url", "/temp"), zap.String("dir", tempPath))
		}
	}

	handler.RegisterRouters(r, db, redisClient, syncEngine)

	// 5. 托管外部进程（内网穿透 frpc × 2 + 动态域名 ddns-go）
	//
	// 这条链路是外网访问的唯一入口：frpc 一断，两个域名就全都打不进来，
	// 而现象只是"服务好好的但访问不了"。以前靠 start.bat 手动拉起、死了没人管，
	// 现在跟着后端一起起、一起停，并且死了会自动重启（见 internal/supervisor）。
	svCfg := config.Conf.Supervisor
	if config.IsProd() && svCfg.Enabled {
		// 容器里没有宿主机的 frpc / ddns-go 二进制，托管只会反复拉起失败；内网穿透放宿主机或另起容器
		logger.Logger.Warn("生产模式不托管外部进程，已忽略 supervisor 配置")
		svCfg.Enabled = false
	}
	sv := supervisor.New(svCfg, logger.Logger)
	sv.Start()

	// 6. 启动服务
	//
	// 用 http.Server + Shutdown 而不是 r.Run()：必须能接住 Ctrl+C / 服务停止信号，
	// 否则托管的子进程会变成孤儿，下次启动 frpc 就会撞上 frps 的 proxy 名冲突
	// （`proxy [xxx] already exists`），新旧客户端谁都用不上。
	addr := fmt.Sprintf(":%d", config.Conf.Server.Port)
	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		logger.Logger.Info("服务器准备启动", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Logger.Fatal("服务器启动失败", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	logger.Logger.Info("收到退出信号，正在关闭…")

	// 先停 HTTP（不再接新请求），再停子进程：反过来的话，正在处理的请求
	// 可能因为隧道被掐断而失败。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Logger.Warn("HTTP 服务关闭超时", zap.Error(err))
	}
	sv.Stop(10 * time.Second)
	logger.Logger.Info("已退出")
}
