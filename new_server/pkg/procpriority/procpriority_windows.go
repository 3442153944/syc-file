//go:build windows

// Package procpriority 提高本进程在操作系统调度器里的优先级。
//
// 动机：资源告警巡检(internal/monitor/resource_alert.go)和进程采集
// (sys_detail.go)存在的意义就是在系统被打满的时候还能看清是谁打满的——
// 但如果本进程自己也只是 NORMAL 优先级，跟一堆抢 CPU 的进程站在同一起跑线，
// 系统越忙，我们的采集 goroutine 反而越可能被调度器晾在一边：该 3s/0.5s
// 跑一次的巡检被拖到几秒甚至更久才轮到，正是最需要它准时的时候掉链子。
// Windows 上没有 Linux OOM killer 那种因为吃 CPU 就被杀掉的机制，但调度
// 延迟本身就足够破坏告警的时效性，所以只处理"调度优先级"这一层。
//
// 用 ABOVE_NORMAL 而不是 HIGH/REALTIME：后两者是 Task 管理器自己都会弹
// 警告的档位——真把这个进程顶到 HIGH，在它出 bug 死循环的时候反而会把
// 整台机器拖垮，得不偿失。ABOVE_NORMAL 只是让调度器在跟同优先级的普通
// 进程竞争时更偏向我们一点，不会抢占关键系统进程，出问题时影响面也可控。
package procpriority

import (
	"syc-file/pkg/logger"

	"go.uber.org/zap"
	"golang.org/x/sys/windows"
)

// Raise 把当前进程的优先级类提到 ABOVE_NORMAL。失败只记日志，不影响启动——
// 优先级只是锦上添花，没有它也能跑，没必要因为这个阻塞整个服务起不来
// （比如某些受限的容器/沙箱环境里 SetPriorityClass 可能没权限）。
func Raise() {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		logger.Logger.Warn("获取进程句柄失败，跳过优先级提升", zap.Error(err))
		return
	}
	if err := windows.SetPriorityClass(handle, windows.ABOVE_NORMAL_PRIORITY_CLASS); err != nil {
		logger.Logger.Warn("提升进程优先级失败，继续以默认优先级运行", zap.Error(err))
		return
	}
	logger.Logger.Info("进程优先级已提升至 ABOVE_NORMAL，降低系统繁忙时监控采集被调度器延迟的概率")
}
