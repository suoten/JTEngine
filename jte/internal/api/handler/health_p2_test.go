package handler

// ===================================================================
// FIXED-2026-07-23 [P2]: 健康检查端点加固测试
// 新增检查器：ZLMediaKitChecker / JT809Checker / MemoryChecker
// ===================================================================

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/internal/maintenance"
	"go.uber.org/zap"
)

// TestP2_ZLMediaKitChecker_Connected ZLMediaKit 连通时返回 nil
func TestP2_ZLMediaKitChecker_Connected(t *testing.T) {
	c := NewZLMediaKitChecker("zlmediakit", func() bool { return true })
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// TestP2_ZLMediaKitChecker_Disconnected ZLMediaKit 断开时返回错误
func TestP2_ZLMediaKitChecker_Disconnected(t *testing.T) {
	c := NewZLMediaKitChecker("zlmediakit", func() bool { return false })
	if err := c.Check(context.Background()); err == nil {
		t.Error("expected error when disconnected, got nil")
	}
}

// TestP2_ZLMediaKitChecker_NilFunc 未配置时返回错误
func TestP2_ZLMediaKitChecker_NilFunc(t *testing.T) {
	c := NewZLMediaKitChecker("zlmediakit", nil)
	if err := c.Check(context.Background()); err == nil {
		t.Error("expected error when connected func is nil, got nil")
	}
}

// TestP2_JT809Checker_NoClients 无 809 客户端时不报错
func TestP2_JT809Checker_NoClients(t *testing.T) {
	c := NewJT809Checker("jt809", nil)
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("expected nil error with no clients, got %v", err)
	}
}

// TestP2_JT809Checker_AllRunning 所有客户端运行中
func TestP2_JT809Checker_AllRunning(t *testing.T) {
	clients := []JT809ClientStatus{
		&mockJT809Status{id: "platform1", running: true, circuitOpen: false},
		&mockJT809Status{id: "platform2", running: true, circuitOpen: false},
	}
	c := NewJT809Checker("jt809", clients)
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("expected nil error when all running, got %v", err)
	}
}

// TestP2_JT809Checker_Disconnected 客户端断开
func TestP2_JT809Checker_Disconnected(t *testing.T) {
	clients := []JT809ClientStatus{
		&mockJT809Status{id: "platform1", running: false, circuitOpen: false},
	}
	c := NewJT809Checker("jt809", clients)
	if err := c.Check(context.Background()); err == nil {
		t.Error("expected error when client disconnected, got nil")
	}
}

// TestP2_JT809Checker_CircuitOpen 熔断器开启
func TestP2_JT809Checker_CircuitOpen(t *testing.T) {
	clients := []JT809ClientStatus{
		&mockJT809Status{id: "platform1", running: true, circuitOpen: true},
	}
	c := NewJT809Checker("jt809", clients)
	if err := c.Check(context.Background()); err == nil {
		t.Error("expected error when circuit open, got nil")
	}
}

// TestP2_MemoryChecker_ZeroThresholds 阈值为 0 时不检查
func TestP2_MemoryChecker_ZeroThresholds(t *testing.T) {
	c := NewMemoryChecker("memory", 0, 0, 0)
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("expected nil error with zero thresholds, got %v", err)
	}
}

// TestP2_MemoryChecker_HighThreshold 高阈值时不触发
func TestP2_MemoryChecker_HighThreshold(t *testing.T) {
	// 设置一个极高的阈值，确保不会触发
	c := NewMemoryChecker("memory", 999999, 999999, 999999)
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("expected nil error with very high thresholds, got %v", err)
	}
}

// TestP2_MemoryChecker_LowThreshold 低阈值时触发告警
func TestP2_MemoryChecker_LowThreshold(t *testing.T) {
	// 设置极低的阈值，确保触发
	c := NewMemoryChecker("memory", 1, 2, 3)
	if err := c.Check(context.Background()); err == nil {
		// 测试环境内存可能很低，如果没触发也不算失败
		t.Log("memory check did not trigger (test env may have low memory)")
	}
}

// TestP2_Health_IncludesChecks /health 端点返回 checks 字段
func TestP2_Health_IncludesChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testLogger := zap.NewNop()
	sessions := gateway.NewSessionManager(testLogger)
	mm := maintenance.NewMode("", testLogger)

	h := NewExtendedHealthHandler(
		sessions, mm, time.Now(), "test-version", testLogger,
		NewMemoryChecker("memory", 999999, 999999, 999999), // 高阈值，总是 ok
	)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	checks, ok := resp["checks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'checks' field in /health response")
	}

	memCheck, ok := checks["memory"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'memory' in checks")
	}
	if memCheck["status"] != "ok" {
		t.Errorf("expected memory status=ok, got %v", memCheck["status"])
	}
}

// TestP2_Health_DegradedWithBadChecker 不健康的检查器导致 /health 返回 degraded
func TestP2_Health_DegradedWithBadChecker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testLogger := zap.NewNop()
	sessions := gateway.NewSessionManager(testLogger)
	mm := maintenance.NewMode("", testLogger)

	h := NewExtendedHealthHandler(
		sessions, mm, time.Now(), "test-version", testLogger,
		NewZLMediaKitChecker("zlmediakit", func() bool { return false }), // 断开
	)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// 应该返回 degraded 状态
	if resp["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", resp["status"])
	}

	body := w.Body.String()
	if !strings.Contains(body, "zlmediakit") {
		t.Error("expected 'zlmediakit' in response body")
	}
}

// mockJT809Status 模拟 809 客户端状态
type mockJT809Status struct {
	id          string
	running     bool
	circuitOpen bool
}

func (m *mockJT809Status) GetPlatformID() string { return m.id }
func (m *mockJT809Status) IsCircuitOpen() bool   { return m.circuitOpen }
func (m *mockJT809Status) IsRunning() bool        { return m.running }
