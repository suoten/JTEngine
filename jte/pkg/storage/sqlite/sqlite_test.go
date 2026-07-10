package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

// getTestValue 返回测试环境值（仅用于单元测试）
func getTestValue() string {
	if v := os.Getenv("JTE_TEST_VAL"); v != "" {
		return v
	}
	return "test-val"
}

// getTestValueV2 返回测试环境值 V2（仅用于单元测试）
func getTestValueV2() string {
	if v := os.Getenv("JTE_TEST_VAL_V2"); v != "" {
		return v
	}
	return "test-val-v2"
}

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	logger := zap.NewNop()
	store, err := NewSQLiteStore(dbPath, logger)
	if err != nil {
		// go-sqlite3 在 CGO_ENABLED=0 时为 stub，无法执行 SQL，跳过测试
		t.Skipf("create sqlite store failed (likely CGO disabled): %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSQLiteStore_SaveAndGetVehicle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v := &storage.Vehicle{
		ID:           "v1",
		Phone:        "13800000001",
		Protocol:     "jt808",
		PlateNo:      "A12345",
		PlateColor:   1,
		TerminalID:   "term001",
		TerminalType: "GT06",
		Manufacturer: "test",
		Online:       false,
		RegisteredAt: time.Now(),
		LastActive:   time.Now(),
	}
	if err := s.SaveVehicle(ctx, v); err != nil {
		t.Fatalf("SaveVehicle: %v", err)
	}
	got, err := s.GetVehicle(ctx, "v1")
	if err != nil {
		t.Fatalf("GetVehicle: %v", err)
	}
	if got.Phone != "13800000001" {
		t.Errorf("Phone = %q, want %q", got.Phone, "13800000001")
	}
	if got.PlateNo != "A12345" {
		t.Errorf("PlateNo = %q, want %q", got.PlateNo, "A12345")
	}
}

func TestSQLiteStore_GetVehicleByPhone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v := &storage.Vehicle{
		ID:           "v2",
		Phone:        "13800000002",
		Protocol:     "jt808",
		Online:       false,
		RegisteredAt: time.Now(),
		LastActive:   time.Now(),
	}
	if err := s.SaveVehicle(ctx, v); err != nil {
		t.Fatalf("SaveVehicle: %v", err)
	}
	got, err := s.GetVehicleByPhone(ctx, "13800000002")
	if err != nil {
		t.Fatalf("GetVehicleByPhone: %v", err)
	}
	if got.ID != "v2" {
		t.Errorf("ID = %q, want %q", got.ID, "v2")
	}
}

func TestSQLiteStore_UpdateVehicleOnline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v := &storage.Vehicle{
		ID:           "v3",
		Phone:        "13800000003",
		Protocol:     "jt808",
		Online:       false,
		RegisteredAt: time.Now(),
		LastActive:   time.Now(),
	}
	if err := s.SaveVehicle(ctx, v); err != nil {
		t.Fatalf("SaveVehicle: %v", err)
	}
	if err := s.UpdateVehicleOnline(ctx, "v3", true); err != nil {
		t.Fatalf("UpdateVehicleOnline: %v", err)
	}
	got, err := s.GetVehicle(ctx, "v3")
	if err != nil {
		t.Fatalf("GetVehicle: %v", err)
	}
	if !got.Online {
		t.Errorf("Online = false, want true")
	}
}

func TestSQLiteStore_DeleteVehicle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	v := &storage.Vehicle{
		ID:           "v4",
		Phone:        "13800000004",
		Protocol:     "jt808",
		Online:       false,
		RegisteredAt: time.Now(),
		LastActive:   time.Now(),
	}
	if err := s.SaveVehicle(ctx, v); err != nil {
		t.Fatalf("SaveVehicle: %v", err)
	}
	if err := s.DeleteVehicle(ctx, "v4"); err != nil {
		t.Fatalf("DeleteVehicle: %v", err)
	}
	_, err := s.GetVehicle(ctx, "v4")
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestSQLiteStore_SaveLocation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	loc := &storage.LocationData{
		VehicleID:  "v5",
		Phone:      "13800000005",
		Latitude:   39.9042,
		Longitude:  116.4074,
		Altitude:   50.0,
		Speed:      60.0,
		Direction:  90,
		AlarmFlag:  0,
		StatusFlag: 0,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	if err := s.SaveLocation(ctx, loc); err != nil {
		t.Fatalf("SaveLocation: %v", err)
	}
}

func TestSQLiteStore_SaveAlarm(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	alarm := &storage.AlarmData{
		ID:         "alarm1",
		VehicleID:  "v6",
		Phone:      "13800000006",
		Type:       "jt808_alarm",
		Level:      1,
		AlarmFlag:  0x00000001,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	if err := s.SaveAlarm(ctx, alarm); err != nil {
		t.Fatalf("SaveAlarm: %v", err)
	}
}

func TestSQLiteStore_ListVehicles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		v := &storage.Vehicle{
			ID:           fmt.Sprintf("lv%d", i),
			Phone:        fmt.Sprintf("1380000%04d", i),
			Protocol:     "jt808",
			Online:       false,
			RegisteredAt: time.Now(),
			LastActive:   time.Now(),
		}
		if err := s.SaveVehicle(ctx, v); err != nil {
			t.Fatalf("SaveVehicle %d: %v", i, err)
		}
	}
	result, err := s.ListVehicles(ctx, storage.ListOptions{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListVehicles: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("total = %d, want 5", result.Total)
	}
}

func TestSQLiteStore_WALMode(t *testing.T) {
	s := newTestStore(t)
	var mode string
	row := s.db.QueryRow("PRAGMA journal_mode")
	if err := row.Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则 SQLite CRUD 测试
// 验证 SaveForwardRule/GetForwardRule/ListForwardRules/DeleteForwardRule 全链路。

func TestSQLiteStore_ForwardRuleCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:         "fr_001",
		PlatformID: "platform-A",
		DataType:   "alarm",
		Phone:      "13800000000",
		AlarmTypes: "overspeed,emergency",
		MinLevel:   2,
		TimeStart:  "08:00:00",
		TimeEnd:    "20:00:00",
		Enabled:    true,
	}
	if err := s.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule: %v", err)
	}

	got, err := s.GetForwardRule(ctx, "fr_001")
	if err != nil {
		t.Fatalf("GetForwardRule: %v", err)
	}
	if got.PlatformID != "platform-A" {
		t.Errorf("PlatformID = %q, want platform-A", got.PlatformID)
	}
	if got.AlarmTypes != "overspeed,emergency" {
		t.Errorf("AlarmTypes = %q", got.AlarmTypes)
	}
	if got.MinLevel != 2 {
		t.Errorf("MinLevel = %d, want 2", got.MinLevel)
	}
	if !got.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set")
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt should be set")
	}
}

func TestSQLiteStore_ForwardRuleUpsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:         "fr_upsert",
		PlatformID: "platform-A",
		DataType:   "location",
		Phone:      "",
		Enabled:    true,
	}
	if err := s.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule (insert): %v", err)
	}

	// 同 ID 再次保存应替换
	rule.Phone = "13900000000"
	rule.MinLevel = 3
	if err := s.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule (update): %v", err)
	}
	got, err := s.GetForwardRule(ctx, "fr_upsert")
	if err != nil {
		t.Fatalf("GetForwardRule after upsert: %v", err)
	}
	if got.Phone != "13900000000" {
		t.Errorf("Phone = %q, want 13900000000", got.Phone)
	}
	if got.MinLevel != 3 {
		t.Errorf("MinLevel = %d, want 3", got.MinLevel)
	}
}

func TestSQLiteStore_ListForwardRulesByPlatform(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 准备 3 条规则，2 条属于 platform-A，1 条属于 platform-B
	rules := []*storage.ForwardRule{
		{ID: "fr_a1", PlatformID: "platform-A", DataType: "location", Enabled: true},
		{ID: "fr_a2", PlatformID: "platform-A", DataType: "alarm", Enabled: false},
		{ID: "fr_b1", PlatformID: "platform-B", DataType: "location", Enabled: true},
	}
	for _, r := range rules {
		if err := s.SaveForwardRule(ctx, r); err != nil {
			t.Fatalf("SaveForwardRule %s: %v", r.ID, err)
		}
	}

	// 按 platform-A 过滤
	aRules, err := s.ListForwardRules(ctx, "platform-A")
	if err != nil {
		t.Fatalf("ListForwardRules platform-A: %v", err)
	}
	if len(aRules) != 2 {
		t.Errorf("platform-A rules count = %d, want 2", len(aRules))
	}

	// 按 platform-B 过滤
	bRules, err := s.ListForwardRules(ctx, "platform-B")
	if err != nil {
		t.Fatalf("ListForwardRules platform-B: %v", err)
	}
	if len(bRules) != 1 {
		t.Errorf("platform-B rules count = %d, want 1", len(bRules))
	}

	// 空 platformID 返回全部
	all, err := s.ListForwardRules(ctx, "")
	if err != nil {
		t.Fatalf("ListForwardRules all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all rules count = %d, want 3", len(all))
	}
}

func TestSQLiteStore_DeleteForwardRule(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:         "fr_del",
		PlatformID: "platform-A",
		DataType:   "location",
		Enabled:    true,
	}
	if err := s.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule: %v", err)
	}
	if err := s.DeleteForwardRule(ctx, "fr_del"); err != nil {
		t.Fatalf("DeleteForwardRule: %v", err)
	}
	// 删除后再查询应报错
	if _, err := s.GetForwardRule(ctx, "fr_del"); err == nil {
		t.Fatalf("GetForwardRule after delete should error")
	}
	// 重复删除应报错
	if err := s.DeleteForwardRule(ctx, "fr_del"); err == nil {
		t.Fatalf("DeleteForwardRule twice should error")
	}
}

// AUTO-FIX-2026-07-02 [P1]: ForwardRule SourcePlatformID 持久化测试
func TestSQLiteStore_ForwardRuleSourcePlatformID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:               "fr_src_001",
		SourcePlatformID: "downstream_plat_A",
		PlatformID:       "upstream_plat_X",
		DataType:         "alarm",
		Phone:            "13800000000",
		Enabled:          true,
	}
	if err := s.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule: %v", err)
	}

	got, err := s.GetForwardRule(ctx, "fr_src_001")
	if err != nil {
		t.Fatalf("GetForwardRule: %v", err)
	}
	if got.SourcePlatformID != "downstream_plat_A" {
		t.Errorf("SourcePlatformID = %q, want downstream_plat_A", got.SourcePlatformID)
	}
}

// AUTO-FIX-2026-07-02 [P1]: Platform CRUD 测试
func TestSQLiteStore_PlatformCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := &storage.Platform{
		ID:         "plat_1001",
		Name:       "省厅监控平台",
		UserID:     "1001",
		Password:   getTestValue(),
		Role:       "upstream",
		Host:       "10.0.1.100",
		Port:       9001,
		LinkType:   0,
		Enabled:    true,
	}
	if err := s.SavePlatform(ctx, p); err != nil {
		t.Fatalf("SavePlatform: %v", err)
	}

	got, err := s.GetPlatform(ctx, "plat_1001")
	if err != nil {
		t.Fatalf("GetPlatform: %v", err)
	}
	if got.Name != "省厅监控平台" {
		t.Errorf("Name = %q, want 省厅监控平台", got.Name)
	}
	if got.UserID != "1001" {
		t.Errorf("UserID = %q, want 1001", got.UserID)
	}
	if got.Role != "upstream" {
		t.Errorf("Role = %q, want upstream", got.Role)
	}
	if got.Host != "10.0.1.100" {
		t.Errorf("Host = %q, want 10.0.1.100", got.Host)
	}
	if got.Port != 9001 {
		t.Errorf("Port = %d, want 9001", got.Port)
	}
	if !got.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set")
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt should be set")
	}
}

// TestSQLiteStore_PlatformUpsert 验证同 ID 保存为更新语义
func TestSQLiteStore_PlatformUpsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := &storage.Platform{
		ID:       "plat_upsert",
		Name:     "原始名称",
		UserID:   "2001",
		Password: getTestValue(),
		Role:     "downstream",
		Enabled:  true,
	}
	if err := s.SavePlatform(ctx, p); err != nil {
		t.Fatalf("SavePlatform (insert): %v", err)
	}

	// 同 ID 再次保存，更新名称和密码
	p.Name = "更新后名称"
	p.Password = getTestValueV2()
	p.Enabled = false
	if err := s.SavePlatform(ctx, p); err != nil {
		t.Fatalf("SavePlatform (update): %v", err)
	}

	got, err := s.GetPlatform(ctx, "plat_upsert")
	if err != nil {
		t.Fatalf("GetPlatform after upsert: %v", err)
	}
	if got.Name != "更新后名称" {
		t.Errorf("Name = %q, want 更新后名称", got.Name)
	}
	expectedV2 := getTestValueV2()
	if got.Password != expectedV2 {
		t.Errorf("Password = %q, want %s", got.Password, expectedV2)
	}
	if got.Enabled {
		t.Errorf("Enabled = true, want false")
	}
}

// TestSQLiteStore_ListPlatformsByRole 验证按角色过滤
func TestSQLiteStore_ListPlatformsByRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	platforms := []*storage.Platform{
		{ID: "plat_d1", Name: "地市A", UserID: "3001", Password: getTestValue(), Role: "downstream", Enabled: true},
		{ID: "plat_d2", Name: "地市B", UserID: "3002", Password: getTestValue(), Role: "downstream", Enabled: true},
		{ID: "plat_u1", Name: "省厅", UserID: "1001", Password: getTestValue(), Role: "upstream", Host: "10.0.0.1", Port: 9001, Enabled: true},
	}
	for _, p := range platforms {
		if err := s.SavePlatform(ctx, p); err != nil {
			t.Fatalf("SavePlatform %s: %v", p.ID, err)
		}
	}

	// 按 downstream 过滤
	downstream, err := s.ListPlatforms(ctx, "downstream")
	if err != nil {
		t.Fatalf("ListPlatforms downstream: %v", err)
	}
	if len(downstream) != 2 {
		t.Errorf("downstream count = %d, want 2", len(downstream))
	}

	// 按 upstream 过滤
	upstream, err := s.ListPlatforms(ctx, "upstream")
	if err != nil {
		t.Fatalf("ListPlatforms upstream: %v", err)
	}
	if len(upstream) != 1 {
		t.Errorf("upstream count = %d, want 1", len(upstream))
	}

	// 空角色返回全部
	all, err := s.ListPlatforms(ctx, "")
	if err != nil {
		t.Fatalf("ListPlatforms all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all count = %d, want 3", len(all))
	}
}

// TestSQLiteStore_DeletePlatform 验证删除和重复删除报错
func TestSQLiteStore_DeletePlatform(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := &storage.Platform{
		ID:       "plat_del",
		Name:     "待删除",
		UserID:   "4001",
		Password: getTestValue(),
		Role:     "downstream",
		Enabled:  true,
	}
	if err := s.SavePlatform(ctx, p); err != nil {
		t.Fatalf("SavePlatform: %v", err)
	}
	if err := s.DeletePlatform(ctx, "plat_del"); err != nil {
		t.Fatalf("DeletePlatform: %v", err)
	}
	// 删除后再查询应报错
	if _, err := s.GetPlatform(ctx, "plat_del"); err == nil {
		t.Fatalf("GetPlatform after delete should error")
	}
	// 重复删除应报错
	if err := s.DeletePlatform(ctx, "plat_del"); err == nil {
		t.Fatalf("DeletePlatform twice should error")
	}
}
