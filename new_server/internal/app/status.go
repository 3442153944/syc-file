package app

import (
	"context"
	"os"
	"runtime"
	"time"

	"syc-file/config"
	"syc-file/internal/desktopapi"
	"syc-file/internal/ws"
)

// statusPingTimeout DB/Redis 健康检查的超时：概览页 2s 刷新一次，不能因为依赖无响应把界面拖住。
const statusPingTimeout = 500 * time.Millisecond

// Status 采集一次运行状态快照，供桌面控制台的概览页展示。
//
// 实现保持「能拿到就报、拿不到留零值」：GUI 会把零值显示成「—」，
// 单个依赖的超时不能连累整个状态查询。
func (a *App) Status() desktopapi.Status {
	st := desktopapi.Status{
		Version:    config.Version,
		Mode:       config.Conf.Server.Mode,
		NodeName:   config.Conf.Server.Name,
		Platform:   runtime.GOOS,
		Arch:       runtime.GOARCH,
		GoVersion:  runtime.Version(),
		PID:        os.Getpid(),
		StartedAt:  a.startedAt,
		Uptime:     time.Since(a.startedAt),
		Port:       config.Conf.Server.Port,
		ConfigPath: a.configPath,
		LogDir:     a.logDir,
		WorkingDir: a.workingDir,
		Goroutines: runtime.NumGoroutine(),
		Running:    true,
	}
	if st.NodeName == "" {
		if host, err := os.Hostname(); err == nil {
			st.NodeName = host
		}
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	st.MemoryBytes = mem.Sys

	if hub := ws.GetHub(); hub != nil {
		st.OnlineConns = hub.ConnectionCount()
	}

	ctx, cancel := context.WithTimeout(context.Background(), statusPingTimeout)
	defer cancel()
	if a.db != nil {
		if sqlDB, err := a.db.DB(); err == nil && sqlDB != nil {
			st.DBOK = sqlDB.PingContext(ctx) == nil
		}
	}
	if a.redis != nil {
		st.RedisOK = a.redis.Ping(ctx).Err() == nil
	}

	if a.supervisor != nil {
		st.Supervisor = a.supervisor.Snapshot()
	} else if len(config.Conf.Supervisor.Processes) > 0 && !config.Conf.Supervisor.Enabled {
		st.Extra = append(st.Extra, desktopapi.KV{Key: "supervisor", Value: "外部进程托管未启用"})
	}

	return st
}
