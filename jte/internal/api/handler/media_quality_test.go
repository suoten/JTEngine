package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	jt1078 "github.com/suoten/jt-engine/pkg/protocol/jt1078"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-29 [P0-2]: 视频质量统计 API 测试
// 验证 Quality / QualityByStream handler 的过滤、空引擎、流不存在等边界。
// project_memory: 视频质量统计需实时显示码率、帧率、丢包率

func setupQualityTestEngine(t *testing.T) *jt1078.VideoEngine {
	t.Helper()
	eng := jt1078.NewVideoEngine(zap.NewNop(), "")
	// 注入两条测试流
	s1 := eng.CreateSession("dev1_ch1", "13800138001", 1, 0)
	s1.BitrateKbps = 512.5
	s1.FrameRate = 25.0
	s1.LossRate = 1.2
	s1.Packets = 1000
	s1.Bytes = 200000

	s2 := eng.CreateSession("dev2_ch3", "13800138002", 3, 1)
	s2.BitrateKbps = 128.0
	s2.FrameRate = 15.0
	s2.LossRate = 0.5
	s2.Packets = 500
	s2.Bytes = 50000

	return eng
}

func newQualityTestHandler(eng *jt1078.VideoEngine) *MediaHandler {
	return &MediaHandler{
		logger:      zap.NewNop(),
		videoEngine: eng,
	}
}

func performQualityRequest(h *MediaHandler, path, query string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	target := path
	if query != "" {
		target = path + "?" + query
	}
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.Quality(c)
	return w
}

// TestQuality_ReturnsAllStreams 验证不带过滤参数时返回全部流
func TestQuality_ReturnsAllStreams(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := performQualityRequest(h, "/api/v1/video/quality", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Streams []*jt1078.QualityStats `json:"streams"`
			Total   int                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Data.Total)
	}
	if len(resp.Data.Streams) != 2 {
		t.Fatalf("streams len = %d, want 2", len(resp.Data.Streams))
	}
}

// TestQuality_FilterByDeviceID 验证按 device_id 过滤
func TestQuality_FilterByDeviceID(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := performQualityRequest(h, "/api/v1/video/quality", "device_id=13800138001")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Streams []*jt1078.QualityStats `json:"streams"`
			Total   int                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 1 {
		t.Fatalf("total = %d, want 1", resp.Data.Total)
	}
	if len(resp.Data.Streams) != 1 || resp.Data.Streams[0].Phone != "13800138001" {
		t.Fatalf("expected only dev1 stream, got %+v", resp.Data.Streams)
	}
}

// TestQuality_FilterByChannel 验证按 channel 过滤
func TestQuality_FilterByChannel(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := performQualityRequest(h, "/api/v1/video/quality", "channel=3")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Streams []*jt1078.QualityStats `json:"streams"`
			Total   int                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 1 {
		t.Fatalf("total = %d, want 1", resp.Data.Total)
	}
	if len(resp.Data.Streams) != 1 || resp.Data.Streams[0].LogicChannel != 3 {
		t.Fatalf("expected only channel 3, got %+v", resp.Data.Streams)
	}
}

// TestQuality_FilterByDeviceIDAndChannel 验证同时按 device_id + channel 过滤
func TestQuality_FilterByDeviceIDAndChannel(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := performQualityRequest(h, "/api/v1/video/quality", "device_id=13800138002&channel=3")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Streams []*jt1078.QualityStats `json:"streams"`
			Total   int                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 1 {
		t.Fatalf("total = %d, want 1", resp.Data.Total)
	}
	if len(resp.Data.Streams) != 1 || resp.Data.Streams[0].StreamID != "dev2_ch3" {
		t.Fatalf("expected dev2_ch3, got %+v", resp.Data.Streams)
	}
}

// TestQuality_FilterNoMatch 验证过滤无匹配时返回空列表
func TestQuality_FilterNoMatch(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := performQualityRequest(h, "/api/v1/video/quality", "device_id=not_exist")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Streams []*jt1078.QualityStats `json:"streams"`
			Total   int                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 0 {
		t.Fatalf("total = %d, want 0", resp.Data.Total)
	}
	if len(resp.Data.Streams) != 0 {
		t.Fatalf("streams len = %d, want 0", len(resp.Data.Streams))
	}
}

// TestQuality_NilVideoEngine 验证 videoEngine 为 nil 时返回 400
func TestQuality_NilVideoEngine(t *testing.T) {
	h := &MediaHandler{logger: zap.NewNop()}
	w := performQualityRequest(h, "/api/v1/video/quality", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestQuality_InvalidChannelIgnored 验证非法 channel 参数被忽略（不过滤）
func TestQuality_InvalidChannelIgnored(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	// channel=-1 超出 0-255 范围，应被忽略，返回全部流
	w := performQualityRequest(h, "/api/v1/video/quality", "channel=-1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 2 {
		t.Fatalf("total = %d, want 2 (invalid channel should be ignored)", resp.Data.Total)
	}
}

// TestQualityByStream_Found 验证按 stream_id 查询单流
func TestQualityByStream_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/media/quality/dev1_ch1", nil)
	c.Params = gin.Params{{Key: "stream_id", Value: "dev1_ch1"}}
	h.QualityByStream(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int                   `json:"code"`
		Data *jt1078.QualityStats `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 || resp.Data == nil {
		t.Fatalf("invalid response: code=%d data=%+v", resp.Code, resp.Data)
	}
	if resp.Data.StreamID != "dev1_ch1" {
		t.Fatalf("stream_id = %q, want dev1_ch1", resp.Data.StreamID)
	}
	if resp.Data.BitrateKbps != 512.5 {
		t.Fatalf("bitrate = %v, want 512.5", resp.Data.BitrateKbps)
	}
}

// TestQualityByStream_NotFound 验证流不存在时返回 404
func TestQualityByStream_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/media/quality/nonexistent", nil)
	c.Params = gin.Params{{Key: "stream_id", Value: "nonexistent"}}
	h.QualityByStream(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// TestQualityByStream_NilVideoEngine 验证 videoEngine 为 nil 时返回 400
func TestQualityByStream_NilVideoEngine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &MediaHandler{logger: zap.NewNop()}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/media/quality/any", nil)
	c.Params = gin.Params{{Key: "stream_id", Value: "any"}}
	h.QualityByStream(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestQuality_StatsFieldsComplete 验证返回的质量字段完整（码率/帧率/丢包率/在线状态）
// project_memory: 视频质量统计需实时显示码率、帧率、丢包率
func TestQuality_StatsFieldsComplete(t *testing.T) {
	eng := setupQualityTestEngine(t)
	h := newQualityTestHandler(eng)

	w := performQualityRequest(h, "/api/v1/video/quality", "device_id=13800138001&channel=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Streams []*jt1078.QualityStats `json:"streams"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data.Streams) != 1 {
		t.Fatalf("streams len = %d, want 1", len(resp.Data.Streams))
	}
	s := resp.Data.Streams[0]
	if s.BitrateKbps != 512.5 {
		t.Errorf("bitrate_kbps = %v, want 512.5", s.BitrateKbps)
	}
	if s.FrameRate != 25.0 {
		t.Errorf("frame_rate = %v, want 25.0", s.FrameRate)
	}
	if s.LossRate != 1.2 {
		t.Errorf("loss_rate = %v, want 1.2", s.LossRate)
	}
	if !s.Online {
		t.Errorf("online = false, want true (LastActive just set)")
	}
	if s.Phone != "13800138001" {
		t.Errorf("phone = %q, want 13800138001", s.Phone)
	}
}
