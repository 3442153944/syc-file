package database

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

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
