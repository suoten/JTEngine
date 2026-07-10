package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

// waitReloaderCalls 轮询等待 reloader 被调用指定次数（异步通知机制）
func waitReloaderCalls(r *fakeReloader, want int32, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&r.calls) >= want {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return atomic.LoadInt32(&r.calls) >= want
}

// AUTO-FIX-2026-06-29 [P1-6]: ForwardRuleHandler API 测试
// 验证 CRUD 端点和 reloader 热更新通知机制。

// fakeReloader 记录 ReloadForwardRules 调用次数的测试 reloader
type fakeReloader struct {
	calls int32
}

func (f *fakeReloader) ReloadForwardRules() {
	atomic.AddInt32(&f.calls, 1)
}

// forwardRuleMockStore 支持转发规则完整 CRUD 的内存 store
type forwardRuleMockStore struct {
	mockStorage
	rules map[string]*storage.ForwardRule
}

func newForwardRuleMockStore() *forwardRuleMockStore {
	return &forwardRuleMockStore{
		rules: make(map[string]*storage.ForwardRule),
	}
}

func (s *forwardRuleMockStore) SaveForwardRule(_ context.Context, r *storage.ForwardRule) error {
	s.rules[r.ID] = r
	return nil
}
func (s *forwardRuleMockStore) GetForwardRule(_ context.Context, id string) (*storage.ForwardRule, error) {
	if r, ok := s.rules[id]; ok {
		return r, nil
	}
	return nil, errNotFound
}
func (s *forwardRuleMockStore) ListForwardRules(_ context.Context, platformID string) ([]*storage.ForwardRule, error) {
	result := make([]*storage.ForwardRule, 0)
	for _, r := range s.rules {
		if platformID != "" && r.PlatformID != platformID {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}
func (s *forwardRuleMockStore) DeleteForwardRule(_ context.Context, id string) error {
	if _, ok := s.rules[id]; !ok {
		return errNotFound
	}
	delete(s.rules, id)
	return nil
}

func setupForwardRuleTestRouter(t *testing.T) (*gin.Engine, *ForwardRuleHandler, *forwardRuleMockStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := newForwardRuleMockStore()
	h := NewForwardRuleHandler(store, zap.NewNop())
	r := gin.New()
	r.GET("/forward-rules", h.List)
	r.POST("/forward-rules", h.Create)
	r.GET("/forward-rules/:id", h.Get)
	r.PUT("/forward-rules/:id", h.Update)
	r.DELETE("/forward-rules/:id", h.Delete)
	return r, h, store
}

func TestForwardRuleHandler_Create(t *testing.T) {
	r, h, store := setupForwardRuleTestRouter(t)
	reloader := &fakeReloader{}
	h.RegisterReloader("platform-A", reloader)

	body := `{"platform_id":"platform-A","data_type":"alarm","phone":"13800000000","alarm_types":"overspeed","min_level":2,"enabled":true}`
	req := httptest.NewRequest("POST", "/forward-rules", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
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
	data := resp["data"].(map[string]interface{})
	id := data["id"].(string)
	if id == "" {
		t.Fatal("id should be auto-generated")
	}
	if _, ok := store.rules[id]; !ok {
		t.Fatal("rule should be persisted in store")
	}
	// reloader 应被通知（异步）
	if !waitReloaderCalls(reloader, 1, time.Second) {
		t.Errorf("reloader calls = %d, want 1", atomic.LoadInt32(&reloader.calls))
	}
}

func TestForwardRuleHandler_Create_ValidationErrors(t *testing.T) {
	r, _, _ := setupForwardRuleTestRouter(t)

	cases := []struct {
		name string
		body string
	}{
		{"missing platform_id", `{"data_type":"alarm"}`},
		{"invalid data_type", `{"platform_id":"pA","data_type":"invalid"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/forward-rules", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400; body=%s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestForwardRuleHandler_Get(t *testing.T) {
	r, _, store := setupForwardRuleTestRouter(t)
	store.rules["fr1"] = &storage.ForwardRule{ID: "fr1", PlatformID: "pA", DataType: "alarm"}

	req := httptest.NewRequest("GET", "/forward-rules/fr1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestForwardRuleHandler_Get_NotFound(t *testing.T) {
	r, _, _ := setupForwardRuleTestRouter(t)
	req := httptest.NewRequest("GET", "/forward-rules/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestForwardRuleHandler_List(t *testing.T) {
	r, _, store := setupForwardRuleTestRouter(t)
	store.rules["fr1"] = &storage.ForwardRule{ID: "fr1", PlatformID: "pA", DataType: "alarm"}
	store.rules["fr2"] = &storage.ForwardRule{ID: "fr2", PlatformID: "pB", DataType: "location"}

	// 列全部
	req := httptest.NewRequest("GET", "/forward-rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("list all count = %d, want 2", len(data))
	}

	// 按 platformID 过滤
	req = httptest.NewRequest("GET", "/forward-rules?platform_id=pA", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &resp)
	data = resp["data"].([]interface{})
	if len(data) != 1 {
		t.Errorf("list pA count = %d, want 1", len(data))
	}
}

func TestForwardRuleHandler_Update(t *testing.T) {
	r, h, store := setupForwardRuleTestRouter(t)
	reloader := &fakeReloader{}
	h.RegisterReloader("pA", reloader)
	store.rules["fr1"] = &storage.ForwardRule{ID: "fr1", PlatformID: "pA", DataType: "alarm", Enabled: true}

	body := `{"data_type":"location","enabled":false}`
	req := httptest.NewRequest("PUT", "/forward-rules/fr1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	// 验证已更新
	if store.rules["fr1"].DataType != "location" {
		t.Errorf("DataType = %q, want location", store.rules["fr1"].DataType)
	}
	if store.rules["fr1"].Enabled {
		t.Errorf("Enabled should be false")
	}
	// PlatformID 不应被修改
	if store.rules["fr1"].PlatformID != "pA" {
		t.Errorf("PlatformID = %q, want pA", store.rules["fr1"].PlatformID)
	}
	if !waitReloaderCalls(reloader, 1, time.Second) {
		t.Errorf("reloader calls = %d, want 1", atomic.LoadInt32(&reloader.calls))
	}
}

func TestForwardRuleHandler_Update_NotFound(t *testing.T) {
	r, _, _ := setupForwardRuleTestRouter(t)
	body := `{"data_type":"location","enabled":false}`
	req := httptest.NewRequest("PUT", "/forward-rules/nonexistent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestForwardRuleHandler_Delete(t *testing.T) {
	r, h, store := setupForwardRuleTestRouter(t)
	reloader := &fakeReloader{}
	h.RegisterReloader("pA", reloader)
	store.rules["fr1"] = &storage.ForwardRule{ID: "fr1", PlatformID: "pA", DataType: "alarm"}

	req := httptest.NewRequest("DELETE", "/forward-rules/fr1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	if _, ok := store.rules["fr1"]; ok {
		t.Fatal("rule should be deleted")
	}
	if !waitReloaderCalls(reloader, 1, time.Second) {
		t.Errorf("reloader calls = %d, want 1", atomic.LoadInt32(&reloader.calls))
	}
}

func TestForwardRuleHandler_Delete_NotFound(t *testing.T) {
	r, _, _ := setupForwardRuleTestRouter(t)
	req := httptest.NewRequest("DELETE", "/forward-rules/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestForwardRuleHandler_NoReloaderRegistered(t *testing.T) {
	// 未注册 reloader 时 Create 不应 panic，应正常返回
	r, h, _ := setupForwardRuleTestRouter(t)
	_ = h

	body := `{"platform_id":"pB","data_type":"alarm","enabled":true}`
	req := httptest.NewRequest("POST", "/forward-rules", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}
