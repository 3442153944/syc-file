package logger

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

// TestSetLevel 级别热改：AtomicLevel 立即生效，非法级别不改变当前值。
func TestSetLevel(t *testing.T) {
	// 用完恢复 Info，避免影响同包其它用例
	t.Cleanup(func() { _ = SetLevel("info") })

	if err := SetLevel("DEBUG"); err != nil { // 大小写不敏感
		t.Fatalf("SetLevel(DEBUG): %v", err)
	}
	if !IsDebug() {
		t.Fatal("设置 debug 后 IsDebug 应为 true")
	}
	if !IsLevel(zapcore.InfoLevel) {
		t.Fatal("debug 级别下 info 日志应可用")
	}
	if Level() != zapcore.DebugLevel {
		t.Fatalf("Level() = %v, want debug", Level())
	}

	if err := SetLevel("warn"); err != nil {
		t.Fatalf("SetLevel(warn): %v", err)
	}
	if IsDebug() {
		t.Fatal("warn 级别下 IsDebug 应为 false")
	}
	if IsLevel(zapcore.InfoLevel) {
		t.Fatal("warn 级别下 info 日志不应输出")
	}
	if !IsLevel(zapcore.ErrorLevel) {
		t.Fatal("warn 级别下 error 日志应可用")
	}

	// 非法级别：返回错误且不改变当前级别
	if err := SetLevel("verbose"); err == nil {
		t.Fatal("非法级别应返回错误")
	}
	if Level() != zapcore.WarnLevel {
		t.Fatalf("非法级别不应改变当前级别，得到 %v", Level())
	}
	if err := SetLevel(""); err == nil {
		t.Fatal("空级别应返回错误")
	}
}
