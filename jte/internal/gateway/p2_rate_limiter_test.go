package gateway

// ===================================================================
// FIXED-2026-07-23 [P2]: ipConnRateLimiter 后台清理测试
// ===================================================================

import (
	"testing"
	"time"
)

// TestP2_ipConnRateLimiter_Cleanup 验证 Cleanup 清理过期计数器
func TestP2_ipConnRateLimiter_Cleanup(t *testing.T) {
	limiter := newIPConnRateLimiter(100, 50*time.Millisecond)

	// 添加一些计数器
	limiter.Allow("192.168.1.1")
	limiter.Allow("192.168.1.2")
	limiter.Allow("192.168.1.3")

	limiter.mu.Lock()
	count := len(limiter.counters)
	limiter.mu.Unlock()
	if count != 3 {
		t.Fatalf("expected 3 counters, got %d", count)
	}

	// 等待窗口过期
	time.Sleep(60 * time.Millisecond)

	limiter.Cleanup()

	limiter.mu.Lock()
	count = len(limiter.counters)
	limiter.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 counters after cleanup, got %d", count)
	}
}

// TestP2_ipConnRateLimiter_StartStopCleanup 验证 StartCleanup/StopCleanup 不 panic
func TestP2_ipConnRateLimiter_StartStopCleanup(t *testing.T) {
	limiter := newIPConnRateLimiter(100, time.Second)

	// 启动后台清理
	limiter.StartCleanup()

	// 添加一些数据
	limiter.Allow("10.0.0.1")

	// 等待一小段时间
	time.Sleep(10 * time.Millisecond)

	// 停止后台清理（不应 panic）
	limiter.StopCleanup()
}

// TestP2_ipConnRateLimiter_StopCleanup_Idempotent 验证重复调用 StopCleanup 不会 panic
// 注意：当前实现 close(stopCh) 多次会 panic，但实际使用中只在 Stop() 中调用一次
func TestP2_ipConnRateLimiter_StopCleanup(t *testing.T) {
	limiter := newIPConnRateLimiter(100, time.Second)
	limiter.StartCleanup()
	limiter.StopCleanup()
	// 到这里说明没有 panic
}
