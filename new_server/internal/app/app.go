// Package app 负责组装并管理 syc-file 后端进程的全部运行时组件。
//
// 为什么把初始化链从 cmd/main.go 搬到这里：main 只保留「装配 → 跑 → 收尾」三步，
// 同一个 App 既服务于 headless 部署（systemd / Docker / Windows 隐藏窗口），
// 也提供给本地管理接口（internal/localapi）查询状态、请求重启/退出。
//
// 行为约束：Init/Start 的执行顺序与原来 main.go 逐字一致（配置→日志→Gin→DB→迁移→
// bootstrap→Redis→WS/监控→同步→路由→STUN→supervisor→HTTP），任何调整都会改变
// 部署行为（尤其是日志缓冲刷新、外部进程托管、关闭顺序），修改前先看 git history。
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/account"
	"syc-file/internal/database"
	"syc-file/internal/desktopapi"
	"syc-file/internal/desktoplaunch"
	"syc-file/internal/handler"
	filehandler "syc-file/internal/handler/file"
	"syc-file/internal/localapi"
	"syc-file/internal/middleware"
	"syc-file/internal/model"
	"syc-file/internal/monitor"
	"syc-file/internal/supervisor"
	syncengine "syc-file/internal/sync"
	"syc-file/internal/system"
	"syc-file/internal/thumb"
	"syc-file/internal/vpn"
	"syc-file/internal/ws"
	"syc-file/pkg/device_store"
	"syc-file/pkg/logger"
	"syc-file/pkg/procpriority"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
)

// startupWaitTimeout 启动时等待 MySQL / Redis 就绪的最长时间（开机自启时它们的容器比本服务晚几秒才可用）。
const startupWaitTimeout = 90 * time.Second

// shutdownTimeout 关闭时给 HTTP 与托管子进程各自留出的时间。
const shutdownTimeout = 10 * time.Second

// App 是一个已装配好后端进程的实例。
//
// 生命周期：New → Init（全部副作用初始化）→ Start（开始监听）→ WaitExit（阻塞）
// → Shutdown（收尾）。Init/Start 失败时调用方负责记录并退出。
type App struct {
	db         *gorm.DB
	redis      *redis.Client
	router     *gin.Engine
	server     *http.Server
	syncEngine *syncengine.Engine
	supervisor *supervisor.Supervisor
	localAPI   *localapi.Server
	// stopConfigWatch 停止 config.yaml 的热重载监听（Stop 时调用）。
	stopConfigWatch func()

	startedAt  time.Time
	configPath string
	logDir     string
	workingDir string

	exitCh   chan desktopapi.ExitReason
	exitOnce sync.Once
}

// New 创建一个尚未初始化的 App。
func New() *App {
	wd, _ := os.Getwd()
	absWD, err := filepath.Abs(wd)
	if err == nil {
		wd = absWD
	}
	cfgPath, _ := filepath.Abs(filepath.Join(configDir(), "config.yaml"))
	logDir, _ := filepath.Abs(configDir())
	return &App{
		startedAt:  time.Now(),
		workingDir: wd,
		configPath: cfgPath,
		logDir:     logDir,
		exitCh:     make(chan desktopapi.ExitReason, 1),
	}
}

// configDir 返回配置文件所在目录：SYC_CONFIG_DIR 优先（容器里指向数据卷），默认 ./config。
func configDir() string {
	if dir := os.Getenv("SYC_CONFIG_DIR"); dir != "" {
		return dir
	}
	return "./config"
}

// Init 完成全部一次性初始化，顺序与旧 cmd/main.go 一致。
func (a *App) Init() error {
	// 1. 初始化配置 (Viper)
	if err := config.Init(); err != nil {
		return fmt.Errorf("配置初始化失败: %w", err)
	}

	// 2. 初始化日志 (Zap + Lumberjack)
	if err := logger.Init(config.Conf.Log); err != nil {
		return fmt.Errorf("日志初始化失败: %w", err)
	}
	if abs, err := filepath.Abs(config.Conf.Log.Path); err == nil {
		a.logDir = abs
	}

	logger.Logger.Info("配置与日志初始化成功",
		zap.Int("port", config.Conf.Server.Port), zap.String("mode", config.Conf.Server.Mode))

	if config.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 尽量提高本进程的调度优先级：资源告警/进程采集的价值就在于系统被打满时还能看清状况。
	procpriority.Raise()

	// 3. 初始化 Gin 引擎（手动挂 Zap 中间件 + 默认恢复中间件）
	r := gin.New()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Token", "Device-Id"},
		ExposeHeaders:    []string{"New-Token", "Token-Refreshed", "Content-Disposition"},
		AllowCredentials: false,
		MaxAge:           86400 * time.Second,
	}))
	r.Use(middleware.ZapLogger(), gin.Recovery())
	a.router = r

	// 建立数据库连接：等 MySQL 就绪，真连不上才明确退出。
	var db *gorm.DB
	if err := database.Retry("MySQL", startupWaitTimeout, func() error {
		var e error
		db, e = database.InitMySQL(config.Conf.DB)
		return e
	}); err != nil {
		return fmt.Errorf("数据库连接失败: %w", err)
	}
	a.db = db
	logger.Logger.Info("数据库连接成功")

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
		&model.StoragePool{},
		&model.UploadSpace{},
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
		&model.DSHToken{},
		&model.VpsNode{},
		&model.VPNOrg{},
		&model.VPNGroup{},
		&model.VPNMember{},
		&model.VPNDevice{},
		&model.VPNEnrollToken{},
		&model.VPNInterop{},
		&model.VPNDeviceHistory{},
	); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	logger.Logger.Info("数据库表迁移完成")

	// 升级旧管理员 → 超级管理员、播种路由、判定是否需要初始化（见 internal/system）
	if err := system.Bootstrap(db); err != nil {
		return fmt.Errorf("系统初始化检查失败: %w", err)
	}

	// share_link 历来靠 sql/share_link.sql 手工建表、不进 AutoMigrate（避免动已有表的索引）；
	// 全新库（例如容器首次启动）没有这张表，这里只在「不存在」时创建，已有的表一律不碰。
	if !db.Migrator().HasTable(&model.ShareLink{}) {
		if err := db.Migrator().CreateTable(&model.ShareLink{}); err != nil {
			return fmt.Errorf("创建 share_link 表失败: %w", err)
		}
		logger.Logger.Info("已创建 share_link 表")
	}

	// JWT 密钥存数据库：首次启动自动生成，老部署会从旧 venv/key.yaml 导入（见 pkg/token/secret.go）
	if err := token.InitSecret(db, !config.IsProd()); err != nil {
		return fmt.Errorf("JWT 密钥初始化失败: %w", err)
	}

	// 建立缓存连接
	var redisClient *redis.Client
	if err := database.Retry("Redis", startupWaitTimeout, func() error {
		var e error
		redisClient, e = database.InitRedis(config.Conf.Redis)
		return e
	}); err != nil {
		return fmt.Errorf("缓存连接失败: %w", err)
	}
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		return fmt.Errorf("Redis连接测试失败: %w", err)
	}
	a.redis = redisClient
	logger.Logger.Info("Redis连接成功")

	// 初始化 ws
	ws.InitWS(db)

	// 初始化监控推送器（注册 WS monitor 处理器）
	monitor.InitBroadcaster()

	// 监控历史记录：独立于 WS 推送常驻采样，写 Redis（7~8 天热数据）供仪表盘画趋势图；
	// 每天再把前一天的数据批量归档进 MySQL，长期保存
	monitor.Init(redisClient, db)
	monitor.StartHistoryRecorder()
	monitor.StartDailyArchiver()

	// 进程/端口明细采集：间隔见 config.monitor.sys_detail_interval_seconds（默认30s）
	monitor.StartSysDetailRecorder()
	monitor.StartSysDetailArchiver()

	// 资源告警巡检：独立于上面的历史采集 ticker，固定 3s 基线扫一次单进程 CPU 占比
	monitor.StartResourceAlertWatcher()

	// 监控历史长期表降采样清理
	monitor.StartRetentionCleaner()

	// 初始化设备状态 Redis 存储
	device_store.Init(redisClient)

	// 初始化分片上传会话 Redis 存储
	upload_store.Init(redisClient)

	// 启动临时文件清理器（清理过期上传遗留的 .part）
	filehandler.StartTempJanitor(db)
	account.StartCleanupWorker(db, func(uid uint) error {
		return filehandler.CleanupAccountShares(db, redisClient, uid)
	})

	// 启动分享链接清理器（30 分钟兜底扫描；每条链接另有独立协程到期自毁）
	filehandler.StartShareLinkJanitor(db, redisClient)

	// 图片缩略图（ffmpeg）：找不到 ffmpeg 或配置关闭时 Init 返回 nil，功能静默关闭，不影响服务启动
	if svc, terr := thumb.Init(config.Conf.Thumbnail, redisClient); terr != nil {
		logger.Logger.Warn("缩略图服务初始化失败，功能不可用", zap.Error(terr))
	} else if svc != nil && !config.Conf.Thumbnail.NoBackfill {
		go svc.Backfill(config.Conf.Sync.SyncCatalogue)
	}

	// 初始化文件同步引擎（Redis队列 + worker）
	a.syncEngine = syncengine.InitSync(db, redisClient, config.Conf.Sync)

	// 4. 注册路由
	r.GET("/ping", func(c *gin.Context) {
		logger.Logger.Info("收到 ping 请求")
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})
	// 操作日志：记录写操作（谁、做了什么、成没成功）到 operation_log 表，供管理端查询。
	// 必须在 RegisterRouters 之前 Use，且要在 db 就绪之后。
	r.Use(middleware.OperationLogger(db))

	// 头像等静态资源。DB 里 user.avatar 存的是相对路径（默认 static/avatar/xxx.png），
	// 客户端直接用「服务器根 + 该相对路径」取图，所以这里的挂载点必须和 avatar_path 逐字一致。
	if avatarPath := config.Conf.User.AvatarPath; avatarPath != "" {
		rel := strings.Trim(filepath.ToSlash(avatarPath), "/")
		r.Static("/"+rel, filepath.Join(a.workingDir, filepath.FromSlash(rel)))
		logger.Logger.Info("静态资源已挂载", zap.String("url", "/"+rel),
			zap.String("dir", filepath.Join(a.workingDir, filepath.FromSlash(rel))))
	}

	// 分享文件统一经过下载接口校验，不公开挂载临时目录。

	// 组网 STUN-lite：客户端用它发现自己的公网端点，再用 heartbeat 上报。
	if _, err := vpn.StartSTUN(config.Conf.Vpn.StunPort); err != nil {
		logger.Logger.Warn("STUN-lite 启动失败（组网仍可走中继）", zap.Error(err))
	}
	// 服务器侧的 wg peer 由后端按会话有效性自己维护（见 internal/vpn/reconciler.go）
	vpn.StartServerReconciler(db)

	handler.RegisterRouters(r, db, redisClient, a.syncEngine)

	// 5. 托管外部进程（内网穿透 frpc × 2 + 动态域名 ddns-go）
	svCfg := config.Conf.Supervisor
	if os.Getenv("SYC_SKIP_SUPERVISOR") == "1" {
		logger.Logger.Info("开发启动器：跳过外部进程托管（frpc/ddns-go），避免抢占生产隧道与 DDNS 记录")
		svCfg.Enabled = false
	}
	if config.IsProd() && svCfg.Enabled {
		// 容器里没有宿主机的 frpc / ddns-go 二进制，托管只会反复拉起失败
		logger.Logger.Warn("生产模式不托管外部进程，已忽略 supervisor 配置")
		svCfg.Enabled = false
	}
	a.supervisor = supervisor.New(svCfg, logger.Logger)

	return nil
}

// Start 开始监听 HTTP，并在 loopback 上开放本地管理接口（失败只记警告，不影响服务本身）。
func (a *App) Start() error {
	addr := fmt.Sprintf(":%d", config.Conf.Server.Port)
	a.server = &http.Server{Addr: addr, Handler: a.router}

	go func() {
		logger.Logger.Info("服务器准备启动", zap.String("addr", addr))
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Logger.Error("服务器启动失败", zap.Error(err))
			// 监听失败属于致命错误：让主循环退出，交给部署层重启/人工处理。
			a.RequestExit(desktopapi.ExitQuit)
		}
	}()

	// 托管外部进程（放在 HTTP 之后启动与旧顺序一致：旧 main 里也是先起 srv goroutine 再 Start）
	a.supervisor.Start()

	// 本地管理接口：本机 IPC（Windows 命名管道 / Unix socket），供桌面控制台查询状态、重启、停止。
	// 不走 TCP：代理的 TUN/透明接管可能劫持 loopback 流量，IPC 不经过 IP 栈。
	// 起不来不算致命错误（例如权限受限），headless 部署照常工作。
	srv, err := localapi.Start(filepath.Dir(a.configPath), a)
	if err != nil {
		logger.Logger.Warn("本地管理接口启动失败，桌面控制台将无法连接", zap.Error(err))
	} else {
		a.localAPI = srv
		logger.Logger.Info("本地管理接口已启动",
			zap.String("runtime", srv.RuntimePath()), zap.String("ipc", srv.Endpoint()))

		// 控制台自启动：后端起来了就拉起 UI（gui.enabled）。
		// UI 退出不影响后端；UI 自己单独启动不会反向拉起后端。
		// 服务方式运行时由 desktoplaunch 通过 WTSQueryUserToken 放进登录用户桌面。
		if backendExe, exeErr := os.Executable(); exeErr != nil {
			logger.Logger.Warn("无法定位后端可执行文件，跳过自动拉起桌面控制台", zap.Error(exeErr))
		} else {
			result, launchErr := desktoplaunch.Launch(desktoplaunch.Options{
				Enabled:    config.Conf.GUI.Enabled,
				UIPath:     config.Conf.GUI.UIPath,
				ConfigPath: a.configPath,
				BackendExe: backendExe,
			})
			switch {
			case launchErr != nil:
				logger.Logger.Warn("自动拉起桌面控制台失败", zap.Error(launchErr))
			case result.Launched:
				logger.Logger.Info("桌面控制台已自动拉起", zap.String("exe", result.Executable))
			default:
				logger.Logger.Info("跳过自动拉起桌面控制台", zap.String("reason", result.Reason))
			}
		}
	}

	// 配置热重载：先把启动时生效的配置发布为 Live 快照，再监听文件变化。
	// 热字段由 config 包按清单立即应用，冷字段只写日志、等下次重启生效。
	// 监听放在 Init 之后：初始化期间不接收半成品配置。
	config.PublishLive(config.Clone(config.Conf))
	config.RegisterLoggerHooks(logger.SetLevel, func(format string, args ...any) {
		logger.Logger.Info(fmt.Sprintf(format, args...))
	})
	stopWatch, watchErr := config.StartWatcher(context.Background(), a.configPath)
	if watchErr != nil {
		logger.Logger.Warn("配置文件热重载监听启动失败（保存配置后需手动重启才生效）",
			zap.String("path", a.configPath), zap.Error(watchErr))
	} else {
		a.stopConfigWatch = stopWatch
		logger.Logger.Info("配置文件热重载监听已启动", zap.String("path", a.configPath))
	}

	return nil
}

// WaitExit 阻塞到收到系统信号或本地管理接口的重启/退出请求。
func (a *App) WaitExit() desktopapi.ExitReason {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case <-quit:
		return desktopapi.ExitQuit
	case reason := <-a.exitCh:
		return reason
	}
}

// RequestExit 由任意 goroutine 请求退出（本地管理接口 / HTTP 启动失败）。
// 重复调用只生效一次，不会阻塞。
func (a *App) RequestExit(reason desktopapi.ExitReason) {
	a.exitOnce.Do(func() {
		select {
		case a.exitCh <- reason:
		default:
		}
	})
}

// Restart 实现 localapi.Handlers：请求重启。
// 本进程只负责优雅退出，重新拉起由桌面控制台（独立进程）或 systemd/docker 负责。
func (a *App) Restart() { a.RequestExit(desktopapi.ExitRestart) }

// Shutdown 实现 localapi.Handlers：请求优雅退出。
func (a *App) Shutdown() { a.RequestExit(desktopapi.ExitQuit) }

// ValidateConfig 校验一份待保存的 YAML 文本（只解析 + 校验，不落盘、不应用）。
// 桌面控制台保存配置前会先调它，避免把写坏的配置落盘后导致后端起不来。
func (a *App) ValidateConfig(text string) error {
	cfg, err := config.LoadText(text)
	if err != nil {
		return fmt.Errorf("YAML 解析失败: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}
	return nil
}

// Stop 优雅收尾：先停 HTTP（不再接新请求），再停子进程。
// 反过来的话，正在处理的请求可能因为隧道被掐断而失败。
func (a *App) Stop(ctx context.Context) {
	if a.stopConfigWatch != nil {
		a.stopConfigWatch()
		a.stopConfigWatch = nil
	}

	if a.localAPI != nil {
		if err := a.localAPI.Close(); err != nil {
			logger.Logger.Warn("本地管理接口关闭失败", zap.Error(err))
		}
	}

	if a.server != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			logger.Logger.Warn("HTTP 服务关闭超时", zap.Error(err))
		}
	}

	if a.supervisor != nil {
		a.supervisor.Stop(shutdownTimeout)
	}
	logger.Logger.Info("已退出")
}

// SyncLogger 刷新日志缓冲（等价于旧 main.go 里的 defer logger.Logger.Sync）。
func (a *App) SyncLogger() {
	if logger.Logger == nil {
		return
	}
	if err := logger.Logger.Sync(); err != nil {
		logger.Logger.Error("日志缓冲刷新失败", zap.Error(err))
	}
}

// DB / Redis 暴露给状态采集与本包内部使用。
func (a *App) DB() *gorm.DB { return a.db }

// Redis 返回已连接的 Redis 客户端。
func (a *App) Redis() *redis.Client { return a.redis }

// StartedAt 返回进程启动时间。
func (a *App) StartedAt() time.Time { return a.startedAt }

// ConfigPath 返回当前生效配置文件的绝对路径。
func (a *App) ConfigPath() string { return a.configPath }

// WorkingDir 返回进程工作目录。
func (a *App) WorkingDir() string { return a.workingDir }

// LogDir 返回日志目录绝对路径。
func (a *App) LogDir() string { return a.logDir }
