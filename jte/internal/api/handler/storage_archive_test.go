// AUTO-FIX-2026-07-02: 归档 API handler 单元测试
// 验证手动触发归档（POST /archive/trigger）和归档进度查询（GET /archive/progress）
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/internal/config"
	"go.uber.org/zap"
)

// fakeArchiver 记录 RunOnce 调用次数的测试归档器
type fakeArchiver struct {
	calls int32
}

func (f *fakeArchiver) RunOnce(_ context.Context) {
	atomic.AddInt32(&f.calls, 1)
}

// setupArchiveTestRouter 构造测试路由
func setupArchiveTestRouter() (*gin.Engine, *StorageHandler, *fakeArchiver) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	// 模拟 defaults 已填充：归档默认启用
	cfg.Storage.Archive.Enabled = true
	cfg.Storage.Archive.IntervalHours = 24
	cfg.Storage.Archive.KeepDays = 365
	cfg.Storage.Archive.ScheduleHour = 2

	h := NewStorageHandler(cfg, zap.NewNop())
	arch := &fakeArchiver{}
	h.SetArchiver(arch)

	r := gin.New()
	r.POST("/archive/trigger", h.TriggerArchive)
	r.GET("/archive/progress", h.ArchiveProgress)
	return r, h, arch
}

// TestTriggerArchive_Success 手动触发归档 API 测试
// 验证：POST /archive/trigger 返回 200，archiver.RunOnce 被异步调用
func TestTriggerArchive_Success(t *testing.T) {
	r, _, arch := setupArchiveTestRouter()

	req := httptest.NewRequest("POST", "/archive/trigger", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["code"].(float64) != 0 {
		t.Errorf("code = %v, want 0", resp["code"])
	}
	if resp["message"] != "archive triggered" {
		t.Errorf("message = %v, want \"archive triggered\"", resp["message"])
	}

	// RunOnce 异步执行，等待最多 1 秒
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&arch.calls) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&arch.calls) != 1 {
		t.Errorf("RunOnce calls = %d, want 1", atomic.LoadInt32(&arch.calls))
	}
}

// TestTriggerArchive_NoArchiver 未配置归档器时返回 400
func TestTriggerArchive_NoArchiver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	h := NewStorageHandler(cfg, zap.NewNop())
	// 不注入 archiver
	r := gin.New()
	r.POST("/archive/trigger", h.TriggerArchive)

	req := httptest.NewRequest("POST", "/archive/trigger", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// TestTriggerArchive_LicenseDenied 授权校验失败时返回 403
func TestTriggerArchive_LicenseDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	h := NewStorageHandler(cfg, zap.NewNop())
	h.SetArchiver(&fakeArchiver{})
	h.SetLicenseValidator(&deniedLicenseValidator{})

	r := gin.New()
	r.POST("/archive/trigger", h.TriggerArchive)

	req := httptest.NewRequest("POST", "/archive/trigger", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

// deniedLicenseValidator 总是拒绝归档授权
type deniedLicenseValidator struct{}

func (d *deniedLicenseValidator) ValidateArchive() error {
	return errArchiveDenied
}

// errArchiveDenied 模拟授权拒绝错误
var errArchiveDenied = &archiveDeniedError{}

type archiveDeniedError struct{}

func (e *archiveDeniedError) Error() string { return "archive feature not licensed" }

// TestArchiveProgress_NoProvider 未注入进度回调时返回基本状态
func TestArchiveProgress_NoProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	h := NewStorageHandler(cfg, zap.NewNop())
	r := gin.New()
	r.GET("/archive/progress", h.ArchiveProgress)

	req := httptest.NewRequest("GET", "/archive/progress", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data := resp["data"].(map[string]interface{})
	if data["running"] != false {
		t.Errorf("running = %v, want false", data["running"])
	}
	if data["archiver_loaded"] != false {
		t.Errorf("archiver_loaded = %v, want false", data["archiver_loaded"])
	}
}

// TestArchiveProgress_WithProvider 注入进度回调后返回完整进度信息
func TestArchiveProgress_WithProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	h := NewStorageHandler(cfg, zap.NewNop())
	h.SetArchiver(&fakeArchiver{})

	// 注入进度回调，返回模拟进度
	progress := map[string]interface{}{
		"running":        true,
		"total_devices":  10,
		"scanned_count":  5,
		"current_device": "dev5",
		"rows_archived":  int64(500),
		"progress_pct":   50.0,
	}
	lastResult := map[string]interface{}{
		"success":        true,
		"devices_scanned": 10,
		"rows_archived":   1000,
	}
	h.SetArchiveProgressProvider(func() (any, any, bool) {
		return progress, lastResult, true
	})

	r := gin.New()
	r.GET("/archive/progress", h.ArchiveProgress)

	req := httptest.NewRequest("GET", "/archive/progress", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data := resp["data"].(map[string]interface{})

	if data["running"] != true {
		t.Errorf("running = %v, want true", data["running"])
	}
	if data["archiver_loaded"] != true {
		t.Errorf("archiver_loaded = %v, want true", data["archiver_loaded"])
	}
	prog := data["progress"].(map[string]interface{})
	if prog["current_device"] != "dev5" {
		t.Errorf("progress.current_device = %v, want dev5", prog["current_device"])
	}
	if prog["scanned_count"].(float64) != 5 {
		t.Errorf("progress.scanned_count = %v, want 5", prog["scanned_count"])
	}
	lr := data["last_result"].(map[string]interface{})
	if lr["success"] != true {
		t.Errorf("last_result.success = %v, want true", lr["success"])
	}
}

// TestArchiveProgress_ProviderReturnsNil 回调返回 nil 时仅返回基本字段
func TestArchiveProgress_ProviderReturnsNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	h := NewStorageHandler(cfg, zap.NewNop())
	h.SetArchiver(&fakeArchiver{})
	h.SetArchiveProgressProvider(func() (any, any, bool) {
		return nil, nil, false
	})

	r := gin.New()
	r.GET("/archive/progress", h.ArchiveProgress)

	req := httptest.NewRequest("GET", "/archive/progress", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data := resp["data"].(map[string]interface{})
	if data["running"] != false {
		t.Errorf("running = %v, want false", data["running"])
	}
	if _, ok := data["progress"]; ok {
		t.Error("progress should be absent when provider returns nil")
	}
	if _, ok := data["last_result"]; ok {
		t.Error("last_result should be absent when provider returns nil")
	}
}
