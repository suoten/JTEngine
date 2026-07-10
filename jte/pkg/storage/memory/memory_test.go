package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

func TestMemoryStore_SaveAndGetVehicle(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	vehicle := &storage.Vehicle{
		ID:         "v1",
		Phone:      "13800138000",
		Protocol:   "jt808",
		PlateNo:    "京A12345",
		PlateColor: 1,
		Online:     true,
	}

	if err := store.SaveVehicle(ctx, vehicle); err != nil {
		t.Fatalf("SaveVehicle failed: %v", err)
	}

	got, err := store.GetVehicle(ctx, "v1")
	if err != nil {
		t.Fatalf("GetVehicle failed: %v", err)
	}

	if got.Phone != vehicle.Phone {
		t.Errorf("Phone = %s, want %s", got.Phone, vehicle.Phone)
	}

	if got.PlateNo != vehicle.PlateNo {
		t.Errorf("PlateNo = %s, want %s", got.PlateNo, vehicle.PlateNo)
	}
}

func TestMemoryStore_GetVehicleByPhone(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	vehicle := &storage.Vehicle{
		ID:    "v1",
		Phone: "13800138000",
	}

	if err := store.SaveVehicle(ctx, vehicle); err != nil {
		t.Fatalf("SaveVehicle failed: %v", err)
	}

	got, err := store.GetVehicleByPhone(ctx, "13800138000")
	if err != nil {
		t.Fatalf("GetVehicleByPhone failed: %v", err)
	}

	if got.ID != "v1" {
		t.Errorf("ID = %s, want v1", got.ID)
	}
}

func TestMemoryStore_MaxDevicesLimit(t *testing.T) {
	store := NewMemoryStore(3)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		vehicle := &storage.Vehicle{
			ID:     fmt.Sprintf("v%d", i),
			Phone:  fmt.Sprintf("1380013800%d", i),
			Online: true,
		}
		if err := store.SaveVehicle(ctx, vehicle); err != nil {
			t.Fatalf("SaveVehicle %d failed: %v", i, err)
		}
	}

	vehicle := &storage.Vehicle{
		ID:     "v3",
		Phone:  "13800138003",
		Online: true,
	}
	err := store.SaveVehicle(ctx, vehicle)
	if err == nil {
		t.Error("expected error for exceeding device limit")
	}
}

func TestMemoryStore_DeleteVehicle(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	vehicle := &storage.Vehicle{ID: "v1", Phone: "13800138000"}
	if err := store.SaveVehicle(ctx, vehicle); err != nil {
		t.Fatalf("SaveVehicle failed: %v", err)
	}

	if err := store.DeleteVehicle(ctx, "v1"); err != nil {
		t.Fatalf("DeleteVehicle failed: %v", err)
	}

	_, err := store.GetVehicle(ctx, "v1")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestMemoryStore_SaveAndGetLocation(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	loc := &storage.LocationData{
		VehicleID: "v1",
		Phone:     "13800138000",
		Latitude:  39.9042,
		Longitude: 116.4074,
		Speed:     60.5,
		Direction: 180,
	}

	if err := store.SaveLocation(ctx, loc); err != nil {
		t.Fatalf("SaveLocation failed: %v", err)
	}

	got, err := store.GetLatestLocation(ctx, "v1")
	if err != nil {
		t.Fatalf("GetLatestLocation failed: %v", err)
	}

	if got.Latitude != loc.Latitude {
		t.Errorf("Latitude = %f, want %f", got.Latitude, loc.Latitude)
	}
}

func TestMemoryStore_SaveAndListAlarms(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	alarm := &storage.AlarmData{
		ID:        "a1",
		VehicleID: "v1",
		Phone:     "13800138000",
		Type:      "dsm",
		Level:     1,
	}

	if err := store.SaveAlarm(ctx, alarm); err != nil {
		t.Fatalf("SaveAlarm failed: %v", err)
	}

	result, err := store.ListAlarms(ctx, storage.ListOptions{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListAlarms failed: %v", err)
	}

	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}
}

func TestMemoryStore_OnlineCount(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	store.SaveVehicle(ctx, &storage.Vehicle{ID: "v1", Phone: "p1", Online: true})
	store.SaveVehicle(ctx, &storage.Vehicle{ID: "v2", Phone: "p2", Online: true})
	store.SaveVehicle(ctx, &storage.Vehicle{ID: "v3", Phone: "p3", Online: false})

	count, err := store.GetOnlineCount(ctx)
	if err != nil {
		t.Fatalf("GetOnlineCount failed: %v", err)
	}

	if count != 2 {
		t.Errorf("OnlineCount = %d, want 2", count)
	}
}

func TestMemoryStore_UpdateVehicleOnline(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	vehicle := &storage.Vehicle{ID: "v1", Phone: "p1", Online: true}
	store.SaveVehicle(ctx, vehicle)

	if err := store.UpdateVehicleOnline(ctx, "v1", false); err != nil {
		t.Fatalf("UpdateVehicleOnline failed: %v", err)
	}

	got, _ := store.GetVehicle(ctx, "v1")
	if got.Online != false {
		t.Error("expected Online to be false")
	}
}

// AUTO-FIX-2026-06-26: 第五轮存储修复 - 数据归档清理测试
func TestMemoryStore_CleanupOldLocations(t *testing.T) {
	store := NewMemoryStore(100)
	ctx := context.Background()

	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)

	store.SaveLocation(ctx, &storage.LocationData{VehicleID: "v1", Phone: "p1", ReceivedAt: old, Source: "jt808"})
	store.SaveLocation(ctx, &storage.LocationData{VehicleID: "v1", Phone: "p1", ReceivedAt: recent, Source: "jt808"})

	deleted, err := store.CleanupOldLocations(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CleanupOldLocations failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	track, _ := store.GetLocationTrack(ctx, "v1", time.Time{}, time.Now())
	if len(track) != 1 {
		t.Errorf("expected 1 remaining location, got %d", len(track))
	}
}

func TestMemoryStore_CleanupOldAlarms(t *testing.T) {
	store := NewMemoryStore(100)
	ctx := context.Background()

	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)

	store.SaveAlarm(ctx, &storage.AlarmData{VehicleID: "v1", ReceivedAt: old, Type: "test"})
	store.SaveAlarm(ctx, &storage.AlarmData{VehicleID: "v1", ReceivedAt: recent, Type: "test"})

	deleted, err := store.CleanupOldAlarms(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("CleanupOldAlarms failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
}

// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则 Memory store CRUD 测试

func TestMemoryStore_ForwardRuleCRUD(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:         "fr_mem_001",
		PlatformID: "platform-A",
		DataType:   "alarm",
		Phone:      "13800000000",
		AlarmTypes: "overspeed",
		MinLevel:   2,
		Enabled:    true,
	}
	if err := store.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule: %v", err)
	}
	got, err := store.GetForwardRule(ctx, "fr_mem_001")
	if err != nil {
		t.Fatalf("GetForwardRule: %v", err)
	}
	if got.PlatformID != "platform-A" {
		t.Errorf("PlatformID = %q", got.PlatformID)
	}
	if got.AlarmTypes != "overspeed" {
		t.Errorf("AlarmTypes = %q", got.AlarmTypes)
	}
	if !got.Enabled {
		t.Errorf("Enabled = false")
	}
	// 验证返回的是副本（修改不影响内部状态）
	got.Phone = "modified"
	again, _ := store.GetForwardRule(ctx, "fr_mem_001")
	if again.Phone == "modified" {
		t.Fatal("GetForwardRule should return a copy, not internal pointer")
	}
}

func TestMemoryStore_ListForwardRulesByPlatform(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	rules := []*storage.ForwardRule{
		{ID: "m1", PlatformID: "pA", DataType: "location", Enabled: true},
		{ID: "m2", PlatformID: "pA", DataType: "alarm", Enabled: true},
		{ID: "m3", PlatformID: "pB", DataType: "location", Enabled: true},
	}
	for _, r := range rules {
		if err := store.SaveForwardRule(ctx, r); err != nil {
			t.Fatalf("SaveForwardRule %s: %v", r.ID, err)
		}
	}

	aRules, err := store.ListForwardRules(ctx, "pA")
	if err != nil {
		t.Fatalf("ListForwardRules pA: %v", err)
	}
	if len(aRules) != 2 {
		t.Errorf("pA rules = %d, want 2", len(aRules))
	}

	all, err := store.ListForwardRules(ctx, "")
	if err != nil {
		t.Fatalf("ListForwardRules all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all rules = %d, want 3", len(all))
	}
}

func TestMemoryStore_DeleteForwardRule(t *testing.T) {
	store := NewMemoryStore(20)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:         "fr_del",
		PlatformID: "pA",
		DataType:   "location",
		Enabled:    true,
	}
	if err := store.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule: %v", err)
	}
	if err := store.DeleteForwardRule(ctx, "fr_del"); err != nil {
		t.Fatalf("DeleteForwardRule: %v", err)
	}
	if _, err := store.GetForwardRule(ctx, "fr_del"); err == nil {
		t.Fatal("GetForwardRule after delete should error")
	}
	if err := store.DeleteForwardRule(ctx, "fr_del"); err == nil {
		t.Fatal("DeleteForwardRule twice should error")
	}
}