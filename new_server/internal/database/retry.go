package database

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"go.uber.org/zap"

	"syc-file/pkg/logger"
)

// retryInitialDelay 首次重试间隔，之后每次翻倍，上限 retryMaxDelay。包级变量是为了测试里能调小。
var (
	retryInitialDelay = 500 * time.Millisecond
	retryMaxDelay     = 5 * time.Second
)

// Retry 等待临时连接故障恢复；认证、权限和数据库配置错误立即返回。
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
		if hint := connectionConfigHint(name, err); hint != "" {
			logger.Logger.Error(name+" 连接配置错误，停止重试",
				zap.Int("attempt", attempt), zap.String("hint", hint), zap.Error(err))
			return fmt.Errorf("%s 连接配置错误，停止重试：%s: %w", name, hint, err)
		}
		if time.Now().Add(delay).After(deadline) {
			return fmt.Errorf("%s 连接失败，已达到 %s 重试期限（共尝试 %d 次）；请检查服务状态、地址、端口及网络: %w", name, timeout, attempt, err)
		}
		logger.Logger.Warn(name+" 连接暂时失败，稍后重试",
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

// connectionConfigHint only classifies explicit server errors. Refused
// connections, timeouts and server startup errors retain the retry behavior.
func connectionConfigHint(name string, err error) string {
	if name == "MySQL" {
		var serverErr *mysql.MySQLError
		if errors.As(err, &serverErr) {
			switch serverErr.Number {
			case 1045:
				return "MySQL 认证失败，请检查 db.user、db.password 及账号允许连接的来源主机"
			case 1044, 1130, 1142, 3118:
				return "MySQL 账号权限或状态不允许连接，请检查目标数据库授权、来源主机限制及账号状态"
			case 1049:
				return "目标数据库不存在，请先创建 db.name 指定的数据库；AutoMigrate 只负责表结构"
			}
		}
	}
	if name == "Redis" {
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			message := cause.Error()
			switch {
			case strings.HasPrefix(message, "NOAUTH "):
				return "Redis 已可达但尚未认证，请检查实际加载配置中的 redis.password 是否为空或未生效"
			case strings.HasPrefix(message, "WRONGPASS "):
				return "Redis 认证失败，请检查密码及 ACL 用户状态；当前客户端使用默认用户"
			case strings.HasPrefix(message, "NOPERM "):
				return "Redis 已连接但权限不足，请检查账号的命令权限及键访问权限"
			case strings.HasPrefix(message, "ERR AUTH "):
				return "Redis 认证配置与服务端不匹配，请检查是否配置了多余的密码或认证方式不一致"
			case strings.HasPrefix(message, "ERR DB index is out of range"), strings.HasPrefix(message, "ERR invalid DB index"):
				return "redis.db 超出服务端允许的逻辑库范围，请检查数据库编号"
			}
		}
	}
	return ""
}
