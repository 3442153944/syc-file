package database

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"syc-file/pkg/logger"
)

// retryInitialDelay 首次重试间隔，之后每次翻倍，上限 retryMaxDelay。包级变量是为了测试里能调小。
var (
	retryInitialDelay = 500 * time.Millisecond
	retryMaxDelay     = 5 * time.Second
)

// Retry 反复调用 fn 直到成功或超过 timeout，用于启动时等待 MySQL / Redis 就绪。
//
// 开机自启时 systemd 只保证 docker.service 起来了，数据库容器还要再过几秒才可用；
// 不等待的话，第一次连接必然失败。超时后返回带尝试次数的错误，由调用方决定是否退出。
func Retry(name string, timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	delay := retryInitialDelay
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil {
			if attempt > 1 {
				logger.Logger.Info(name+" 已就绪", zap.Int("attempts", attempt))
			}
			return nil
		}
		if time.Now().Add(delay).After(deadline) {
			return fmt.Errorf("%s 在 %s 内仍未就绪（共尝试 %d 次）: %w", name, timeout, attempt, err)
		}
		logger.Logger.Warn(name+" 尚未就绪，稍后重试",
			zap.Int("attempt", attempt), zap.Duration("retry_in", delay), zap.Error(err))
		time.Sleep(delay)
		if delay < retryMaxDelay {
			delay *= 2
			if delay > retryMaxDelay {
				delay = retryMaxDelay
			}
		}
	}
}
