package main

import (
	"context"
	"log"

	"go.uber.org/zap"

	"syc-file/internal/app"
	"syc-file/internal/desktopapi"
	"syc-file/pkg/logger"
)

// main 只保留三段：装配（Init）→ 运行并等待退出（Start + WaitExit）→ 收尾（Stop）。
//
// 全部初始化逻辑已搬到 internal/app（见那边的包注释），这里不再直接接触配置、
// 数据库、路由等组件 —— 后端要能被桌面控制台按状态查询/重启，就必须有一个
// 稳定的 App 对象，而不是 main 里的一串局部变量。
func main() {
	a := app.New()

	if err := a.Init(); err != nil {
		// 日志可能还没初始化（配置阶段就失败），两种情况都要给出可读结论。
		if logger.Logger != nil {
			logger.Logger.Fatal("初始化失败", zap.Error(err))
		}
		log.Fatalf("初始化失败: %v", err)
	}
	// 程序退出前刷新日志缓冲（原 main.go 里的 defer logger.Logger.Sync）。
	defer a.SyncLogger()

	if err := a.Start(); err != nil {
		logger.Logger.Fatal("服务启动失败", zap.Error(err))
	}

	reason := a.WaitExit()
	logger.Logger.Info("收到退出请求，正在关闭…", zap.String("reason", string(reason)))

	a.Stop(context.Background())

	if reason == desktopapi.ExitRestart {
		// 桌面控制台（独立进程）或 systemd/docker 会重新拉起；正常退出的退出码即可。
		logger.Logger.Info("已按要求退出，等待外部重新拉起")
	}
}
