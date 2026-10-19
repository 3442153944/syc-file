package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"syc-file/pkg/logger"
)

func init() {
	// Retry 会写日志；测试里给一个空实现，免得 logger 未初始化时空指针
	logger.Logger = zap.NewNop()
}

func fast(t *testing.T) {
	t.Helper()
	oi, om := retryInitialDelay, retryMaxDelay
	retryInitialDelay, retryMaxDelay = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { retryInitialDelay, retryMaxDelay = oi, om })
}

func TestRetrySucceedsAfterFailures(t *testing.T) {
	fast(t)
	calls := 0
	err := Retry("MySQL", time.Second, func() error {
		calls++
		if calls < 4 {
			return errors.New("bad connection")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("前几次失败后应当成功，得到 %v", err)
	}
	if calls != 4 {
		t.Fatalf("应当调用 4 次，实际 %d", calls)
	}
}

func TestRetryTimesOut(t *testing.T) {
	fast(t)
	calls := 0
	boom := errors.New("connection refused")
	err := Retry("Redis", 40*time.Millisecond, func() error {
		calls++
		return boom
	})
	if err == nil {
		t.Fatal("一直失败应当超时返回错误")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("错误链里应保留原始错误，得到 %v", err)
	}
	if calls < 2 {
		t.Fatalf("超时前至少应重试过一次，实际调用 %d 次", calls)
	}
}

func TestRetryFirstTrySuccessNoDelay(t *testing.T) {
	start := time.Now()
	if err := Retry("x", time.Second, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("第一次就成功不应有等待")
	}
}

func TestRetryConfigurationErrorsStopImmediately(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		hint string
	}{
		{"Redis", errors.New("NOAUTH Authentication required."), "尚未认证"},
		{"Redis", fmt.Errorf("ping: %w", errors.New("WRONGPASS invalid username-password pair or user is disabled.")), "认证失败"},
		{"Redis", errors.New("NOPERM this user has no permissions to run the 'ping' command"), "权限不足"},
		{"Redis", errors.New("ERR AUTH <password> called without any password configured for the default user."), "不匹配"},
		{"Redis", errors.New("ERR DB index is out of range"), "逻辑库范围"},
		{"MySQL", &mysql.MySQLError{Number: 1045, Message: "Access denied"}, "认证失败"},
		{"MySQL", &mysql.MySQLError{Number: 1044, Message: "Access denied to database"}, "权限"},
		{"MySQL", fmt.Errorf("connect: %w", &mysql.MySQLError{Number: 1049, Message: "Unknown database"}), "数据库不存在"},
	} {
		t.Run(tc.name+"/"+tc.hint, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			previous := logger.Logger
			logger.Logger = zap.New(core)
			t.Cleanup(func() { logger.Logger = previous })
			calls := 0
			err := Retry(tc.name, time.Minute, func() error { calls++; return tc.err })
			if calls != 1 || !errors.Is(err, tc.err) || !strings.Contains(err.Error(), tc.hint) {
				t.Fatalf("calls=%d, err=%v; expected immediate classified error", calls, err)
			}
			entries := logs.All()
			if len(entries) != 1 || entries[0].Level != zap.ErrorLevel || !strings.Contains(entries[0].Message, "停止重试") {
				t.Fatalf("expected one actionable error log, got %v", entries)
			}
		})
	}
}

func TestTemporaryConnectionErrorsStillRetry(t *testing.T) {
	fast(t)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"Redis", errors.New("LOADING Redis is loading the dataset in memory")},
		{"Redis", errors.New("dial tcp: connection refused")},
		{"MySQL", &mysql.MySQLError{Number: 1040, Message: "Too many connections"}},
	} {
		calls := 0
		err := Retry(tc.name, time.Second, func() error {
			calls++
			if calls == 1 {
				return tc.err
			}
			return nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("%s: calls=%d err=%v", tc.name, calls, err)
		}
	}
}
