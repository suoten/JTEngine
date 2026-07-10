package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// ===================================================================
// mockStorage 内存版 storage.Interface，供 api_extended 单元测试使用
// 仅实现测试涉及的方法，其他方法返回 nil/zero 不报错
// ===================================================================

type mockStorage struct {
	vehicles  map[string]*storage.Vehicle
	locations map[string][]*storage.LocationData
	alarms    map[string]*storage.AlarmData
	sessions  map[string]*storage.SessionData

	onlineCount  int64
	offlineCount int64
	alarmCount   int64

	updateAlarmErr error
	saveVehicleErr error
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		vehicles:  make(map[string]*storage.Vehicle),
		locations: make(map[string][]*storage.LocationData),
		alarms:    make(map[string]*storage.AlarmData),
		sessions:  make(map[string]*storage.SessionData),
	}
}

func (m *mockStorage) SaveVehicle(_ context.Context, v *storage.Vehicle) error {
	if m.saveVehicleErr != nil {
		return m.saveVehicleErr
	}
	m.vehicles[v.ID] = v
	return nil
}
func (m *mockStorage) GetVehicle(_ context.Context, id string) (*storage.Vehicle, error) {
	if v, ok := m.vehicles[id]; ok {
		return v, nil
	}
	return nil, errNotFound
}
func (m *mockStorage) GetVehicleByPhone(_ context.Context, phone string) (*storage.Vehicle, error) {
	for _, v := range m.vehicles {
		if v.Phone == phone {
			return v, nil
		}
	}
	return nil, errNotFound
}
func (m *mockStorage) ListVehicles(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	items := make([]*storage.Vehicle, 0, len(m.vehicles))
	for _, v := range m.vehicles {
		if opts.Phone != "" && v.Phone != opts.Phone {
			continue
		}
		if opts.Online != nil && v.Online != *opts.Online {
			continue
		}
		items = append(items, v)
	}
	total := int64(len(items))
	start := (opts.Page - 1) * opts.PageSize
	if start >= len(items) {
		start = len(items)
	}
	end := start + opts.PageSize
	if end > len(items) {
		end = len(items)
	}
	return &storage.ListResult{Items: items[start:end], Total: total, Page: opts.Page, Size: opts.PageSize}, nil
}
func (m *mockStorage) DeleteVehicle(_ context.Context, id string) error {
	delete(m.vehicles, id)
	return nil
}
func (m *mockStorage) UpdateVehicleOnline(_ context.Context, id string, online bool) error {
	if v, ok := m.vehicles[id]; ok {
		v.Online = online
	}
	return nil
}

func (m *mockStorage) SaveLocation(_ context.Context, loc *storage.LocationData) error {
	m.locations[loc.VehicleID] = append(m.locations[loc.VehicleID], loc)
	return nil
}
func (m *mockStorage) GetLatestLocation(_ context.Context, vehicleID string) (*storage.LocationData, error) {
	if locs, ok := m.locations[vehicleID]; ok && len(locs) > 0 {
		return locs[len(locs)-1], nil
	}
	return nil, errNotFound
}
func (m *mockStorage) GetLocationTrack(_ context.Context, vehicleID string, start, end time.Time) ([]*storage.LocationData, error) {
	locs, ok := m.locations[vehicleID]
	if !ok {
		return []*storage.LocationData{}, nil
	}
	out := make([]*storage.LocationData, 0, len(locs))
	for _, l := range locs {
		if !l.Time.Before(start) && !l.Time.After(end) {
			out = append(out, l)
		}
	}
	return out, nil
}
func (m *mockStorage) ListOnlineLocations(_ context.Context) ([]*storage.LocationData, error) {
	out := make([]*storage.LocationData, 0)
	for _, v := range m.vehicles {
		if v.Online {
			if locs, ok := m.locations[v.ID]; ok && len(locs) > 0 {
				out = append(out, locs[len(locs)-1])
			}
		}
	}
	return out, nil
}

func (m *mockStorage) SaveAlarm(_ context.Context, a *storage.AlarmData) error {
	m.alarms[a.ID] = a
	return nil
}
func (m *mockStorage) UpdateAlarm(_ context.Context, a *storage.AlarmData) error {
	if m.updateAlarmErr != nil {
		return m.updateAlarmErr
	}
	m.alarms[a.ID] = a
	return nil
}
func (m *mockStorage) ListAlarms(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	items := make([]*storage.AlarmData, 0, len(m.alarms))
	for _, a := range m.alarms {
		// 测试中用 Phone 字段携带 id 作为查询键（沿用 alarm.go 现有惯例）
		if opts.Phone != "" && a.ID != opts.Phone {
			continue
		}
		items = append(items, a)
	}
	total := int64(len(items))
	start := (opts.Page - 1) * opts.PageSize
	if start >= len(items) {
		start = len(items)
	}
	end := start + opts.PageSize
	if end > len(items) {
		end = len(items)
	}
	return &storage.ListResult{Items: items[start:end], Total: total, Page: opts.Page, Size: opts.PageSize}, nil
}

func (m *mockStorage) SaveSession(_ context.Context, s *storage.SessionData) error {
	m.sessions[s.ID] = s
	return nil
}
func (m *mockStorage) GetSession(_ context.Context, id string) (*storage.SessionData, error) {
	if s, ok := m.sessions[id]; ok {
		return s, nil
	}
	return nil, errNotFound
}
func (m *mockStorage) ListSessions(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	items := make([]*storage.SessionData, 0, len(m.sessions))
	for _, s := range m.sessions {
		items = append(items, s)
	}
	return &storage.ListResult{Items: items, Total: int64(len(items)), Page: opts.Page, Size: opts.PageSize}, nil
}
func (m *mockStorage) DeleteSession(_ context.Context, id string) error { delete(m.sessions, id); return nil }

func (m *mockStorage) SaveProtocolLog(_ context.Context, _ *storage.ProtocolLog) error { return nil }
func (m *mockStorage) ListProtocolLogs(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	return &storage.ListResult{Items: []*storage.ProtocolLog{}, Total: 0, Page: opts.Page, Size: opts.PageSize}, nil
}

func (m *mockStorage) GetOnlineCount(_ context.Context) (int64, error)  { return m.onlineCount, nil }
func (m *mockStorage) GetOfflineCount(_ context.Context) (int64, error) { return m.offlineCount, nil }
func (m *mockStorage) GetAlarmCount(_ context.Context, _, _ time.Time) (int64, error) {
	return m.alarmCount, nil
}
func (m *mockStorage) GetAlarmCountBySource(_ context.Context, _ string, _, _ time.Time) (int64, error) {
	return 0, nil
}

func (m *mockStorage) BatchSaveLocations(_ context.Context, _ []*storage.LocationData) error { return nil }
func (m *mockStorage) BatchSaveAlarms(_ context.Context, _ []*storage.AlarmData) error      { return nil }
func (m *mockStorage) BatchSaveProtocolLogs(_ context.Context, _ []*storage.ProtocolLog) error {
	return nil
}

func (m *mockStorage) SaveDriverInfo(_ context.Context, _ *storage.DriverInfoData) error { return nil }
func (m *mockStorage) QueryDrivers(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	return &storage.ListResult{Items: []*storage.DriverInfoData{}, Page: opts.Page, Size: opts.PageSize}, nil
}
func (m *mockStorage) DeleteDriver(_ context.Context, _ string) error { return nil }

func (m *mockStorage) SaveGeofence(_ context.Context, _ *storage.Geofence) error        { return nil }
func (m *mockStorage) GetGeofence(_ context.Context, _ string) (*storage.Geofence, error) { return nil, errNotFound }
func (m *mockStorage) ListGeofences(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	return &storage.ListResult{Items: []*storage.Geofence{}, Page: opts.Page, Size: opts.PageSize}, nil
}
func (m *mockStorage) DeleteGeofence(_ context.Context, _ string) error { return nil }

// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则 mock 实现
func (m *mockStorage) SaveForwardRule(_ context.Context, _ *storage.ForwardRule) error {
	return nil
}
func (m *mockStorage) GetForwardRule(_ context.Context, _ string) (*storage.ForwardRule, error) {
	return nil, errNotFound
}
func (m *mockStorage) ListForwardRules(_ context.Context, _ string) ([]*storage.ForwardRule, error) {
	return []*storage.ForwardRule{}, nil
}
func (m *mockStorage) DeleteForwardRule(_ context.Context, _ string) error { return nil }

// AUTO-FIX-2026-07-02 [P1]: 809 级联平台配置 mock 实现
func (m *mockStorage) SavePlatform(_ context.Context, _ *storage.Platform) error {
	return nil
}
func (m *mockStorage) GetPlatform(_ context.Context, _ string) (*storage.Platform, error) {
	return nil, errNotFound
}
func (m *mockStorage) ListPlatforms(_ context.Context, _ string) ([]*storage.Platform, error) {
	return []*storage.Platform{}, nil
}
func (m *mockStorage) DeletePlatform(_ context.Context, _ string) error { return nil }

func (m *mockStorage) SaveMultimedia(_ context.Context, _ *storage.MultimediaData) error { return nil }
func (m *mockStorage) QueryMultimedia(_ context.Context, _ string, _ int, _, _ time.Time, _ int) ([]*storage.MultimediaData, error) {
	return nil, nil
}
func (m *mockStorage) SaveCanData(_ context.Context, _ *storage.CanBusData) error      { return nil }
func (m *mockStorage) SaveBDNavData(_ context.Context, _ *storage.BDNavData) error     { return nil }
func (m *mockStorage) SaveMeterData(_ context.Context, _ *storage.MeterData) error     { return nil }
func (m *mockStorage) SaveDispatch(_ context.Context, _ *storage.DispatchData) error   { return nil }
func (m *mockStorage) SaveElectronicWaybill(_ context.Context, _ *storage.ElectronicWaybillData) error {
	return nil
}
func (m *mockStorage) SaveCommandResp(_ context.Context, _ *storage.CommandRespData) error { return nil }
func (m *mockStorage) SaveTerminalProp(_ context.Context, _ *storage.TerminalPropData) error {
	return nil
}
func (m *mockStorage) SaveAVParam(_ context.Context, _ *storage.AVParamData) error { return nil }
func (m *mockStorage) ListTerminalProps(_ context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	return &storage.ListResult{Items: []*storage.TerminalPropData{}, Page: opts.Page, Size: opts.PageSize}, nil
}
func (m *mockStorage) SaveInfoMenuResp(_ context.Context, _ *storage.InfoMenuRespData) error {
	return nil
}
func (m *mockStorage) SaveSMSForwardResp(_ context.Context, _ *storage.SMSForwardRespData) error {
	return nil
}
func (m *mockStorage) SaveEventResp(_ context.Context, _ *storage.EventRespData) error { return nil }
func (m *mockStorage) SaveEVData(_ context.Context, _ *storage.EVData) error { return nil }
func (m *mockStorage) QueryEVData(_ context.Context, _ string, _ string, _, _ time.Time, _ int) ([]*storage.EVData, error) {
	return nil, nil
}

func (m *mockStorage) CleanupOldLocations(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStorage) CleanupOldAlarms(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStorage) CleanupOldProtocolLogs(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStorage) CleanupOldEVData(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStorage) Close() error { return nil }

// errNotFound 测试用 sentinel 错误
var errNotFound = bytesErr("not found")

type bytesErr string

func (e bytesErr) Error() string { return string(e) }

// ===================================================================
// 测试辅助
// ===================================================================

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestRouter() *gin.Engine {
	r := gin.New()
	return r
}

func doRequest(r *gin.Engine, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func parseJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON response: %v\nbody=%s", err, w.Body.String())
	}
	return m
}

// makeTrack 构造轨迹测试数据
func makeTrack(vehicleID string, n int, startTime time.Time) []*storage.LocationData {
	locs := make([]*storage.LocationData, n)
	for i := 0; i < n; i++ {
		locs[i] = &storage.LocationData{
			VehicleID: vehicleID,
			Phone:     "13800000000",
			Latitude:  39.9 + float64(i)*0.001,
			Longitude: 116.4 + float64(i)*0.001,
			Altitude:  50 + float64(i),
			Speed:     60,
			Direction: 180,
			Time:      startTime.Add(time.Duration(i) * time.Second),
			Mileage:   float64(i) * 0.1,
			Source:    "jt808",
		}
	}
	return locs
}

// ===================================================================
// 1. 轨迹数据模块测试
// ===================================================================

// TestGetTrackHistory 测试历史轨迹分页查询
func TestGetTrackHistory(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 100, time.Now().Add(-1*time.Hour))

	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/history", h.GetTrackHistory)

	start := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	end := time.Now().Format(time.RFC3339)
	w := doRequest(r, "GET", "/tracks/history?vehicle_id=v1&start_time="+start+"&end_time="+end+"&page=1&page_size=10", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["total"].(float64) != 100 {
		t.Errorf("total=%v want 100", m["total"])
	}
	track := m["track"].([]interface{})
	if len(track) != 10 {
		t.Errorf("track len=%d want 10", len(track))
	}
}

// TestGetTrackHistory_NoVehicleID 缺少 vehicle_id 应返回 400
func TestGetTrackHistory_NoVehicleID(t *testing.T) {
	h := NewTrackHandler(newMockStorage(), zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/history", h.GetTrackHistory)
	w := doRequest(r, "GET", "/tracks/history", nil, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// TestGetTrackHistory_Pagination 分页边界
func TestGetTrackHistory_Pagination(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 25, time.Now().Add(-1*time.Hour))
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/history", h.GetTrackHistory)

	// 请求 page=3 page_size=10，应返回 5 条
	w := doRequest(r, "GET", "/tracks/history?vehicle_id=v1&page=3&page_size=10", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["total"].(float64) != 25 {
		t.Errorf("total=%v want 25", m["total"])
	}
	track := m["track"].([]interface{})
	if len(track) != 5 {
		t.Errorf("track len=%d want 5", len(track))
	}
}

// TestGetTrackPlayback 测试轨迹回放（含压缩）
func TestGetTrackPlayback(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 600, time.Now().Add(-1*time.Hour))
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/playback", h.GetTrackPlayback)

	w := doRequest(r, "GET", "/tracks/playback?vehicle_id=v1&compress=true", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	// 压缩后点数应远小于 600
	track := m["track"].([]interface{})
	if len(track) >= 600 {
		t.Errorf("compress not applied, track len=%d", len(track))
	}
	stats := m["stats"].(map[string]interface{})
	if stats["total_points"].(float64) == 0 {
		t.Errorf("stats.total_points=0")
	}
}

// TestGetTrackPlayback_NoCompress 不启用压缩应返回原始点
func TestGetTrackPlayback_NoCompress(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 600, time.Now().Add(-1*time.Hour))
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/playback", h.GetTrackPlayback)

	w := doRequest(r, "GET", "/tracks/playback?vehicle_id=v1", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	track := m["track"].([]interface{})
	if len(track) != 600 {
		t.Errorf("track len=%d want 600", len(track))
	}
}

// TestExportTrack_CSV 测试 CSV 导出
func TestExportTrack_CSV(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 5, time.Now().Add(-1*time.Hour))
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/export", h.ExportTrack)

	w := doRequest(r, "GET", "/tracks/export?vehicle_id=v1&format=csv", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("Content-Type=%s", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "经度") {
		t.Errorf("CSV header missing: %s", body[:min(len(body), 200)])
	}
}

// TestExportTrack_GPX 测试 GPX 导出
func TestExportTrack_GPX(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 3, time.Now().Add(-1*time.Hour))
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/export", h.ExportTrack)

	w := doRequest(r, "GET", "/tracks/export?vehicle_id=v1&format=gpx", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<gpx") {
		t.Errorf("GPX root missing")
	}
	if !strings.Contains(body, "<trkpt") {
		t.Errorf("trkpt missing")
	}
}

// TestExportTrack_KML 测试 KML 导出
func TestExportTrack_KML(t *testing.T) {
	store := newMockStorage()
	store.locations["v1"] = makeTrack("v1", 3, time.Now().Add(-1*time.Hour))
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/export", h.ExportTrack)

	w := doRequest(r, "GET", "/tracks/export?vehicle_id=v1&format=kml", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<kml") {
		t.Errorf("KML root missing")
	}
	if !strings.Contains(body, "<LineString") {
		t.Errorf("LineString missing")
	}
}

// TestExportTrack_InvalidFormat 无效格式应返回 400
func TestExportTrack_InvalidFormat(t *testing.T) {
	h := NewTrackHandler(newMockStorage(), zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/export", h.ExportTrack)

	w := doRequest(r, "GET", "/tracks/export?vehicle_id=v1&format=xml", nil, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// TestGetMileageStats 测试里程统计
func TestGetMileageStats(t *testing.T) {
	store := newMockStorage()
	locs := makeTrack("v1", 10, time.Now().Add(-1*time.Hour))
	for _, l := range locs {
		l.Mileage = 100.5
	}
	store.locations["v1"] = locs
	h := NewTrackHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/tracks/mileage", h.GetMileageStats)

	w := doRequest(r, "GET", "/tracks/mileage?vehicle_id=v1&period=daily", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["total"].(float64) != 100.5 {
		t.Errorf("total=%v want 100.5", m["total"])
	}
}

// TestGetMileageStats_Periods 各周期聚合
func TestGetMileageStats_Periods(t *testing.T) {
	for _, period := range []string{"daily", "weekly", "monthly", "yearly"} {
		store := newMockStorage()
		store.locations["v1"] = makeTrack("v1", 5, time.Now().Add(-1*time.Hour))
		h := NewTrackHandler(store, zap.NewNop())
		r := newTestRouter()
		r.GET("/tracks/mileage", h.GetMileageStats)
		w := doRequest(r, "GET", "/tracks/mileage?vehicle_id=v1&period="+period, nil, "")
		if w.Code != http.StatusOK {
			t.Errorf("period=%s status=%d", period, w.Code)
		}
	}
}

// ===================================================================
// 2. 报警处理模块测试
// ===================================================================

// TestAckAlarm 测试报警确认
func TestAckAlarm(t *testing.T) {
	store := newMockStorage()
	store.alarms["a1"] = &storage.AlarmData{ID: "a1", Source: "jt808"}
	h := NewAlarmHandler(store, zap.NewNop())
	r := newTestRouter()
	r.PUT("/alarms/:id/ack", h.AckAlarm)

	body := `{"operator":"admin","remark":"verified"}`
	w := doRequest(r, "PUT", "/alarms/a1/ack", strings.NewReader(body), "application/json")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(store.alarms["a1"].Source, "ack:admin") {
		t.Errorf("alarm source not updated: %s", store.alarms["a1"].Source)
	}
}

// TestAckAlarm_NotFound 报警不存在应返回 404
func TestAckAlarm_NotFound(t *testing.T) {
	h := NewAlarmHandler(newMockStorage(), zap.NewNop())
	r := newTestRouter()
	r.PUT("/alarms/:id/ack", h.AckAlarm)

	body := `{"operator":"admin"}`
	w := doRequest(r, "PUT", "/alarms/notexist/ack", strings.NewReader(body), "application/json")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

// TestProcessAlarm 测试报警处理
func TestProcessAlarm(t *testing.T) {
	store := newMockStorage()
	store.alarms["a1"] = &storage.AlarmData{ID: "a1", Source: "jt808"}
	h := NewAlarmHandler(store, zap.NewNop())
	r := newTestRouter()
	r.PUT("/alarms/:id/process", h.ProcessAlarm)

	body := `{"operator":"admin","action":"dispatch","description":"sent team"}`
	w := doRequest(r, "PUT", "/alarms/a1/process", strings.NewReader(body), "application/json")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(store.alarms["a1"].Source, "process:admin") {
		t.Errorf("alarm source not updated: %s", store.alarms["a1"].Source)
	}
}

// TestCloseAlarm 测试报警关闭
func TestCloseAlarm(t *testing.T) {
	store := newMockStorage()
	store.alarms["a1"] = &storage.AlarmData{ID: "a1", Source: "jt808"}
	h := NewAlarmHandler(store, zap.NewNop())
	r := newTestRouter()
	r.PUT("/alarms/:id/close", h.CloseAlarm)

	body := `{"operator":"admin","reason":"resolved"}`
	w := doRequest(r, "PUT", "/alarms/a1/close", strings.NewReader(body), "application/json")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(store.alarms["a1"].Source, "close:admin") {
		t.Errorf("alarm source not updated: %s", store.alarms["a1"].Source)
	}
}

// TestCloseAlarm_UpdateError UpdateAlarm 失败应返回 500
func TestCloseAlarm_UpdateError(t *testing.T) {
	store := newMockStorage()
	store.alarms["a1"] = &storage.AlarmData{ID: "a1", Source: "jt808"}
	store.updateAlarmErr = errNotFound
	h := NewAlarmHandler(store, zap.NewNop())
	r := newTestRouter()
	r.PUT("/alarms/:id/close", h.CloseAlarm)

	body := `{"operator":"admin"}`
	w := doRequest(r, "PUT", "/alarms/a1/close", strings.NewReader(body), "application/json")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
}

// TestGetAlarmReport 测试报警统计报表
func TestGetAlarmReport(t *testing.T) {
	store := newMockStorage()
	store.alarmCount = 42
	h := NewAlarmHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/alarms/report", h.GetAlarmReport)

	// 使用 UTC 避免 RFC3339 时区中的 + 被解码为空格
	start := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().UTC().Format(time.RFC3339)
	w := doRequest(r, "GET", "/alarms/report?start_time="+start+"&end_time="+end, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["total"].(float64) != 42 {
		t.Errorf("total=%v want 42", m["total"])
	}
	daily := m["daily"].([]interface{})
	if len(daily) != 7 {
		t.Errorf("daily len=%d want 7", len(daily))
	}
}

// TestAlarmRealtimeSSE 测试 SSE 推送（首次推送后立即关闭连接）
func TestAlarmRealtimeSSE(t *testing.T) {
	store := newMockStorage()
	store.alarms["a1"] = &storage.AlarmData{ID: "a1", Type: "overspeed"}
	h := NewAlarmHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/alarms/realtime", h.AlarmRealtimeSSE)

	// 使用带 Cancel 的 request 触发 ctx.Done() 退出循环
	req := httptest.NewRequest("GET", "/alarms/realtime", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("Content-Type=%s want text/event-stream", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: alarms") {
		t.Errorf("SSE event missing in body: %s", body[:min(len(body), 200)])
	}
}

// ===================================================================
// 3. 设备管理模块测试
// ===================================================================

// createMultipartCSV 构造 multipart/form-data 文件上传请求体
func createMultipartCSV(t *testing.T, csvContent string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "devices.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(csvContent)); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

// TestBatchImportDevices 测试批量导入
func TestBatchImportDevices(t *testing.T) {
	store := newMockStorage()
	h := NewDeviceHandler(store, zap.NewNop(), nil)
	r := newTestRouter()
	r.POST("/devices/batch/import", h.BatchImportDevices)

	csv := "phone,vehicle_id,plate_no,terminal_type\n" +
		"13800000001,v1,京A12345,GT710\n" +
		"13800000002,v2,京B67890,GT720\n"
	body, ct := createMultipartCSV(t, csv)
	w := doRequest(r, "POST", "/devices/batch/import", body, ct)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["success"].(float64) != 2 {
		t.Errorf("success=%v want 2", m["success"])
	}
	if store.vehicles["v1"] == nil || store.vehicles["v1"].Phone != "13800000001" {
		t.Errorf("vehicle v1 not imported")
	}
	if store.vehicles["v1"].PlateNo != "京A12345" {
		t.Errorf("plate_no=%q want 京A12345", store.vehicles["v1"].PlateNo)
	}
}

// TestBatchImportDevices_EmptyFile 空文件应返回 400
func TestBatchImportDevices_EmptyFile(t *testing.T) {
	h := NewDeviceHandler(newMockStorage(), zap.NewNop(), nil)
	r := newTestRouter()
	r.POST("/devices/batch/import", h.BatchImportDevices)

	body, ct := createMultipartCSV(t, "")
	w := doRequest(r, "POST", "/devices/batch/import", body, ct)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// TestBatchImportDevices_NoFile 未提供文件应返回 400
func TestBatchImportDevices_NoFile(t *testing.T) {
	h := NewDeviceHandler(newMockStorage(), zap.NewNop(), nil)
	r := newTestRouter()
	r.POST("/devices/batch/import", h.BatchImportDevices)

	w := doRequest(r, "POST", "/devices/batch/import", nil, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// TestBatchImportDevices_InvalidColumns 列数不足应记入失败
func TestBatchImportDevices_InvalidColumns(t *testing.T) {
	store := newMockStorage()
	h := NewDeviceHandler(store, zap.NewNop(), nil)
	r := newTestRouter()
	r.POST("/devices/batch/import", h.BatchImportDevices)

	csv := "phone,vehicle_id\n" +
		"13800000001\n" + // 列数不足
		"13800000002,v2\n"
	body, ct := createMultipartCSV(t, csv)
	w := doRequest(r, "POST", "/devices/batch/import", body, ct)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["success"].(float64) != 1 {
		t.Errorf("success=%v want 1", m["success"])
	}
	if m["failed"].(float64) != 1 {
		t.Errorf("failed=%v want 1", m["failed"])
	}
}

// TestBatchExportDevices 测试批量导出
func TestBatchExportDevices(t *testing.T) {
	store := newMockStorage()
	store.vehicles["v1"] = &storage.Vehicle{ID: "v1", Phone: "13800000001", PlateNo: "京A12345", TerminalType: "GT710"}
	store.vehicles["v2"] = &storage.Vehicle{ID: "v2", Phone: "13800000002", PlateNo: "京B67890", TerminalType: "GT720"}
	h := NewDeviceHandler(store, zap.NewNop(), nil)
	r := newTestRouter()
	r.GET("/devices/batch/export", h.BatchExportDevices)

	w := doRequest(r, "GET", "/devices/batch/export", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("Content-Type=%s", w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "京A12345") {
		t.Errorf("v1 plate_no missing in export")
	}
	if !strings.Contains(body, "GT710") {
		t.Errorf("v1 terminal_type missing in export")
	}
}

// TestGetDeviceStatus 测试设备状态监控
func TestGetDeviceStatus(t *testing.T) {
	store := newMockStorage()
	store.onlineCount = 5
	store.offlineCount = 3
	store.vehicles["v1"] = &storage.Vehicle{ID: "v1", Phone: "13800000001", Online: true}
	store.locations["v1"] = []*storage.LocationData{
		{VehicleID: "v1", Phone: "13800000001", Latitude: 39.9, Longitude: 116.4, Speed: 60},
	}
	h := NewDeviceHandler(store, zap.NewNop(), nil)
	r := newTestRouter()
	r.GET("/devices/status", h.GetDeviceStatus)

	w := doRequest(r, "GET", "/devices/status", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["online_count"].(float64) != 5 {
		t.Errorf("online_count=%v want 5", m["online_count"])
	}
	if m["total"].(float64) != 8 {
		t.Errorf("total=%v want 8", m["total"])
	}
	rate := m["online_rate"].(float64)
	if rate < 62.0 || rate > 63.0 {
		t.Errorf("online_rate=%v want ~62.5", rate)
	}
}

// ===================================================================
// 4. 报表统计模块测试
// ===================================================================

// TestGetOnlineRateReport 测试在线率报表
func TestGetOnlineRateReport(t *testing.T) {
	store := newMockStorage()
	store.onlineCount = 8
	store.offlineCount = 2
	h := NewReportHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/reports/online-rate", h.GetOnlineRateReport)

	// 使用 UTC 避免 RFC3339 时区中的 + 被解码为空格
	start := time.Now().Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().UTC().Format(time.RFC3339)
	w := doRequest(r, "GET", "/reports/online-rate?start_time="+start+"&end_time="+end, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	current := m["current"].(map[string]interface{})
	if current["online"].(float64) != 8 {
		t.Errorf("online=%v want 8", current["online"])
	}
	daily := m["daily"].([]interface{})
	if len(daily) != 3 {
		t.Errorf("daily len=%d want 3", len(daily))
	}
}

// TestGetMileageReport 测试里程报表
func TestGetMileageReport(t *testing.T) {
	store := newMockStorage()
	locs := makeTrack("v1", 10, time.Now().Add(-1*time.Hour))
	for i, l := range locs {
		l.Mileage = float64(i) * 10
	}
	store.locations["v1"] = locs
	h := NewReportHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/reports/mileage", h.GetMileageReport)

	w := doRequest(r, "GET", "/reports/mileage?vehicle_id=v1", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["total_mileage"].(float64) != 90 { // 最后一点 mileage=9*10=90
		t.Errorf("total_mileage=%v want 90", m["total_mileage"])
	}
}

// TestGetMileageReport_NoVehicle 缺少 vehicle_id 应返回 400
func TestGetMileageReport_NoVehicle(t *testing.T) {
	h := NewReportHandler(newMockStorage(), zap.NewNop())
	r := newTestRouter()
	r.GET("/reports/mileage", h.GetMileageReport)

	w := doRequest(r, "GET", "/reports/mileage", nil, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// TestGetAlarmReport_ReportHandler 报表 handler 的报警统计
func TestGetAlarmReport_ReportHandler(t *testing.T) {
	store := newMockStorage()
	store.alarmCount = 15
	h := NewReportHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/reports/alarm", h.GetAlarmReport)

	start := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	end := time.Now().Format(time.RFC3339)
	w := doRequest(r, "GET", "/reports/alarm?start_time="+start+"&end_time="+end, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["total"].(float64) != 15 {
		t.Errorf("total=%v want 15", m["total"])
	}
}

// TestGetDrivingBehaviorReport 测试驾驶行为分析
func TestGetDrivingBehaviorReport(t *testing.T) {
	store := newMockStorage()
	locs := make([]*storage.LocationData, 0, 10)
	// 构造一段含超速的轨迹（时间需在过去，落入默认 24h 时间范围）
	for i := 0; i < 10; i++ {
		speed := 80.0
		if i >= 5 && i <= 7 {
			speed = 130 // 超速
		}
		locs = append(locs, &storage.LocationData{
			VehicleID: "v1",
			Speed:     speed,
			Time:      time.Now().Add(-time.Duration(10-i) * time.Second),
		})
	}
	store.locations["v1"] = locs
	h := NewReportHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/reports/driving-behavior", h.GetDrivingBehaviorReport)

	w := doRequest(r, "GET", "/reports/driving-behavior?vehicle_id=v1", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["overspeed_count"].(float64) != 3 {
		t.Errorf("overspeed_count=%v want 3", m["overspeed_count"])
	}
	if m["max_speed"].(float64) != 130 {
		t.Errorf("max_speed=%v want 130", m["max_speed"])
	}
}

// TestGetDrivingBehaviorReport_RapidAccel 急加速/急刹车检测
func TestGetDrivingBehaviorReport_RapidAccel(t *testing.T) {
	store := newMockStorage()
	base := time.Now().Add(-10 * time.Second) // 过去时间，落入默认 24h 范围
	locs := []*storage.LocationData{
		{VehicleID: "v1", Speed: 0, Time: base},
		{VehicleID: "v1", Speed: 50, Time: base.Add(1 * time.Second)},  // +50 km/h/s 急加速
		{VehicleID: "v1", Speed: 50, Time: base.Add(2 * time.Second)},
		{VehicleID: "v1", Speed: 0, Time: base.Add(3 * time.Second)},  // -50 km/h/s 急刹车
	}
	store.locations["v1"] = locs
	h := NewReportHandler(store, zap.NewNop())
	r := newTestRouter()
	r.GET("/reports/driving-behavior", h.GetDrivingBehaviorReport)

	w := doRequest(r, "GET", "/reports/driving-behavior?vehicle_id=v1", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	m := parseJSON(t, w)
	if m["rapid_accel"].(float64) != 1 {
		t.Errorf("rapid_accel=%v want 1", m["rapid_accel"])
	}
	if m["rapid_decel"].(float64) != 1 {
		t.Errorf("rapid_decel=%v want 1", m["rapid_decel"])
	}
}

// ===================================================================
// 5. 辅助函数测试
// ===================================================================

// TestParseTimeRange 测试时间范围解析
func TestParseTimeRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		s, e := parseTimeRange(c)
		c.JSON(http.StatusOK, gin.H{"start": s.Unix(), "end": e.Unix()})
	})

	start := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	end := time.Now().Format(time.RFC3339)
	w := doRequest(r, "GET", "/test?start_time="+start+"&end_time="+end, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
}

// TestParsePagination 测试分页参数解析
func TestParsePagination(t *testing.T) {
	r := gin.New()
	r.GET("/test", func(c *gin.Context) {
		p, ps := parsePagination(c)
		c.JSON(http.StatusOK, gin.H{"page": p, "page_size": ps})
	})

	cases := []struct {
		query        string
		wantPage     int
		wantPageSize int
	}{
		{"", 1, 50},                       // 默认值
		{"?page=2&page_size=20", 2, 20},   // 正常
		{"?page=0&page_size=0", 1, 50},    // 零值回退默认
		{"?page=-1&page_size=-1", 1, 50},  // 负值回退默认
		{"?page_size=99999", 1, 1000},     // 超上限裁剪
	}
	for _, c := range cases {
		w := doRequest(r, "GET", "/test"+c.query, nil, "")
		if w.Code != http.StatusOK {
			t.Errorf("query=%s status=%d", c.query, w.Code)
			continue
		}
		m := parseJSON(t, w)
		if m["page"].(float64) != float64(c.wantPage) {
			t.Errorf("query=%s page=%v want %d", c.query, m["page"], c.wantPage)
		}
		if m["page_size"].(float64) != float64(c.wantPageSize) {
			t.Errorf("query=%s page_size=%v want %d", c.query, m["page_size"], c.wantPageSize)
		}
	}
}

// TestComputeOnlineRate 测试在线率计算
func TestComputeOnlineRate(t *testing.T) {
	cases := []struct {
		online int64
		total  int64
		want   float64
	}{
		{8, 10, 80.0},
		{0, 10, 0.0},
		{10, 0, 0.0}, // 避免除零
		{0, 0, 0.0},
	}
	for _, c := range cases {
		got := computeOnlineRate(c.online, c.total)
		if got != c.want {
			t.Errorf("computeOnlineRate(%d,%d)=%v want %v", c.online, c.total, got, c.want)
		}
	}
}

// TestDouglasPeuckerCompress 测试轨迹压缩
func TestDouglasPeuckerCompress(t *testing.T) {
	// 直线上的点应被压缩为两端点
	locs := make([]*storage.LocationData, 0, 10)
	for i := 0; i < 10; i++ {
		locs = append(locs, &storage.LocationData{
			Latitude:  39.9 + float64(i)*0.001, // 共线
			Longitude: 116.4 + float64(i)*0.001,
			Time:      time.Now().Add(time.Duration(i) * time.Second),
		})
	}
	compressed := douglasPeuckerCompress(locs, 0.0001)
	if len(compressed) != 2 {
		t.Errorf("collinear compress len=%d want 2", len(compressed))
	}
}

// TestDouglasPeuckerCompress_Short 短轨迹不压缩
func TestDouglasPeuckerCompress_Short(t *testing.T) {
	locs := makeTrack("v1", 2, time.Now())
	compressed := douglasPeuckerCompress(locs, 0.0001)
	if len(compressed) != 2 {
		t.Errorf("short track compress len=%d want 2", len(compressed))
	}
}

// TestAnalyzeDrivingBehavior 空轨迹不应 panic
func TestAnalyzeDrivingBehavior_Empty(t *testing.T) {
	behavior := analyzeDrivingBehavior(nil)
	if behavior.maxSpeed != 0 {
		t.Errorf("empty behavior maxSpeed=%v want 0", behavior.maxSpeed)
	}
	// 单点轨迹：handler 在 len<2 时返回零值（无法计算加速度）
	behavior = analyzeDrivingBehavior(makeTrack("v1", 1, time.Now()))
	if behavior.maxSpeed != 0 {
		t.Errorf("single point maxSpeed=%v want 0 (early return for len<2)", behavior.maxSpeed)
	}
	// 两点轨迹：可以计算最高速度
	behavior = analyzeDrivingBehavior(makeTrack("v1", 2, time.Now()))
	if behavior.maxSpeed != 60 {
		t.Errorf("two points maxSpeed=%v want 60", behavior.maxSpeed)
	}
}

// TestComputeTrackStats 空轨迹统计
func TestComputeTrackStats_Empty(t *testing.T) {
	stats := computeTrackStats(nil)
	if stats.TotalPoints != 0 {
		t.Errorf("empty stats TotalPoints=%d want 0", stats.TotalPoints)
	}
}

// TestAggregateMileage 测试里程聚合
func TestAggregateMileage(t *testing.T) {
	now := time.Now()
	locs := []*storage.LocationData{
		{Mileage: 100, Time: now},
		{Mileage: 200, Time: now.Add(1 * time.Hour)},
		{Mileage: 300, Time: now.Add(2 * time.Hour)},
	}
	for _, period := range []string{"daily", "weekly", "monthly", "yearly"} {
		result := aggregateMileage(locs, period, now, now.Add(24*time.Hour))
		if len(result) == 0 {
			t.Errorf("period=%s result empty", period)
		}
		// 最后一点 mileage 最大
		maxMileage := 0.0
		for _, r := range result {
			if r["mileage"].(float64) > maxMileage {
				maxMileage = r["mileage"].(float64)
			}
		}
		if maxMileage != 300 {
			t.Errorf("period=%s maxMileage=%v want 300", period, maxMileage)
		}
	}
}

// TestAggregateMileage_Empty 空轨迹应返回空切片
func TestAggregateMileage_Empty(t *testing.T) {
	result := aggregateMileage(nil, "daily", time.Now(), time.Now().Add(24*time.Hour))
	if len(result) != 0 {
		t.Errorf("empty aggregate result len=%d want 0", len(result))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
