package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestTokenBucket_AllowAndRecover(t *testing.T) {
	// rate=2：初始 2 个令牌，耗尽后拒绝
	tb := newTokenBucket(2)
	if !tb.allow() {
		t.Error("first allow should succeed")
	}
	if !tb.allow() {
		t.Error("second allow should succeed")
	}
	if tb.allow() {
		t.Error("third allow should fail (tokens exhausted)")
	}
	// 等待令牌恢复
	time.Sleep(1100 * time.Millisecond)
	if !tb.allow() {
		t.Error("allow should succeed after recovery")
	}
}

func TestTokenBucket_DoesNotExceedMax(t *testing.T) {
	tb := newTokenBucket(3)
	// 不消耗，等待超过恢复周期，令牌不应超过 max
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if !tb.allow() {
			t.Errorf("allow %d should succeed", i)
		}
	}
	if tb.allow() {
		t.Error("4th allow should fail (exceeded max)")
	}
}

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	mw := RateLimit(100) // 高速率，测试中不会被限流
	called := false
	router := gin.New()
	router.Use(mw)
	router.GET("/t", func(c *gin.Context) { called = true; c.Status(200) })

	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Error("handler not called")
	}
}

func TestRateLimit_RejectsOverLimit(t *testing.T) {
	mw := RateLimit(1) // 极低速率，1 个令牌
	router := gin.New()
	router.Use(mw)
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	// 第一次通过
	req := httptest.NewRequest("GET", "/t", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request status = %d, want 200", w.Code)
	}

	// 第二次应被限流（令牌已耗尽，恢复需要时间）
	req2 := httptest.NewRequest("GET", "/t", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", w2.Code)
	}
}

func TestAIRateLimit_DefaultWhenZero(t *testing.T) {
	mw := AIRateLimit(0) // 应默认 10
	if mw == nil {
		t.Fatal("AIRateLimit(0) returned nil")
	}
	// 验证默认速率下前若干请求通过
	router := gin.New()
	router.Use(mw)
	router.GET("/t", func(c *gin.Context) { c.Status(200) })

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/t", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d status = %d, want 200", i, w.Code)
		}
	}
}

func TestAIRateLimit_NegativeDefaults(t *testing.T) {
	mw := AIRateLimit(-5)
	if mw == nil {
		t.Fatal("AIRateLimit(-5) returned nil")
	}
}

func TestTokenBucket_ConcurrentSafety(t *testing.T) {
	tb := newTokenBucket(100)
	var wg sync.WaitGroup
	allowed := int32(0)
	goroutines := 50
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tb.allow() {
				allowed++
			}
		}()
	}
	wg.Wait()
	// 并发下不应 panic，且允许数 <= 初始令牌数
	if allowed > 100 {
		t.Errorf("allowed = %d, should be <= 100", allowed)
	}
	if allowed == 0 {
		t.Error("no requests were allowed")
	}
}
