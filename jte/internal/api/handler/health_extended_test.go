package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/internal/gateway"
	"github.com/jte-engine/jte/internal/maintenance"
	"go.uber.org/zap"
)

// mockChecker 模拟依赖检查器
type mockChecker struct {
	name string
	err  error
}

func (m *mockChecker) Name() string { return m.name }
func (m *mockChecker) Check(ctx context.Context) error {
	return m.err
}

func newTestExtendedHealthHandler(checkers ...DependencyChecker) *ExtendedHealthHandler {
	testLogger := zap.NewNop()
	sessions := gateway.NewSessionManager(testLogger)
	mm := maintenance.NewMode("", testLogger)
	return NewExtendedHealthHandler(
		sessions, mm, time.Now().Add(-10*time.Second),
		"test-1.0.0", testLogger,
		checkers...,
	)
}

func TestExtendedHealth_Health(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newTestExtendedHealthHandler()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if resp["version"] != "test-1.0.0" {
		t.Errorf("expected version=test-1.0.0, got %v", resp["version"])
	}
	if _, ok := resp["uptime"]; !ok {
		t.Error("expected uptime field")
	}
	if _, ok := resp["goroutines"]; !ok {
		t.Error("expected goroutines field")
	}
}

func TestExtendedHealth_Live(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newTestExtendedHealthHandler()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/live", nil)

	h.Live(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "alive" {
		t.Errorf("expected status=alive, got %v", resp["status"])
	}
}

func TestExtendedHealth_Ready_AllHealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newTestExtendedHealthHandler(
		&mockChecker{name: "mysql", err: nil},
		&mockChecker{name: "redis", err: nil},
	)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Ready(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "ready" {
		t.Errorf("expected status=ready, got %v", resp["status"])
	}
	checks, ok := resp["checks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected checks field")
	}
	mysqlCheck, ok := checks["mysql"].(map[string]interface{})
	if !ok {
		t.Fatal("expected mysql check")
	}
	if mysqlCheck["status"] != "ok" {
		t.Errorf("expected mysql status=ok, got %v", mysqlCheck["status"])
	}
}

func TestExtendedHealth_Ready_Degraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newTestExtendedHealthHandler(
		&mockChecker{name: "mysql", err: nil},
		&mockChecker{name: "redis", err: errors.New("connection refused")},
	)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Ready(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", resp["status"])
	}
	checks, ok := resp["checks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected checks field")
	}
	redisCheck, ok := checks["redis"].(map[string]interface{})
	if !ok {
		t.Fatal("expected redis check")
	}
	if redisCheck["status"] != "down" {
		t.Errorf("expected redis status=down, got %v", redisCheck["status"])
	}
	if redisCheck["error"] != "connection refused" {
		t.Errorf("expected error=connection refused, got %v", redisCheck["error"])
	}
}

func TestExtendedHealth_Ready_NoDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newTestExtendedHealthHandler() // 无检查器

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Ready(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (no deps = ok), got %d", w.Code)
	}
}

func TestExtendedHealth_AddChecker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newTestExtendedHealthHandler()

	// 动态添加检查器
	h.AddChecker(&mockChecker{name: "tdengine", err: nil})
	h.AddChecker(&mockChecker{name: "minio", err: nil})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	h.Ready(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	checks, ok := resp["checks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected checks field")
	}
	if _, ok := checks["tdengine"]; !ok {
		t.Error("expected tdengine check after AddChecker")
	}
	if _, ok := checks["minio"]; !ok {
		t.Error("expected minio check after AddChecker")
	}
}

func TestSQLChecker_NilDB(t *testing.T) {
	c := NewSQLChecker("mysql", nil)
	err := c.Check(context.Background())
	if err == nil {
		t.Error("expected error for nil db")
	}
}

func TestHTTPChecker_EmptyURL(t *testing.T) {
	c := NewHTTPChecker("minio", "")
	err := c.Check(context.Background())
	if err == nil {
		t.Error("expected error for empty url")
	}
}

func TestDependencyError_Error(t *testing.T) {
	e := errDependencyNotConfigured("redis")
	if e.Error() != "redis: not configured" {
		t.Errorf("unexpected error message: %s", e.Error())
	}
}
