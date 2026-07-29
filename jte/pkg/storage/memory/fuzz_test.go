package memory

import (
	"context"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

// FuzzMemoryStore_SaveLocation 随机位置数据往返测试
// 确保任意输入的 LocationData 都能正确保存和读取，不 panic
func FuzzMemoryStore_SaveLocation(f *testing.F) {
	// 种子语料
	f.Add("v1", "13800138000", float64(39.9042), float64(116.4074), float64(0), float64(60.5), float64(180))
	f.Add("", "", float64(0), float64(0), float64(0), float64(0), float64(0))
	f.Add("v2", "13900139000", float64(-90), float64(-180), float64(9999), float64(255), float64(359))
	f.Add("v3", "10086", float64(90), float64(180), float64(-100), float64(-1), float64(400))

	f.Fuzz(func(t *testing.T, vehicleID, phone string, lat, lng, alt, speed, dir float64) {
		store := NewMemoryStore(100)
		ctx := context.Background()

		loc := &storage.LocationData{
			VehicleID:  vehicleID,
			Phone:      phone,
			Latitude:   lat,
			Longitude:  lng,
			Altitude:   alt,
			Speed:      speed,
			Direction:  int(dir),
			ReceivedAt: time.Now(),
			Source:     "fuzz",
		}

		// SaveLocation 不应 panic
		if err := store.SaveLocation(ctx, loc); err != nil {
			t.Skipf("SaveLocation returned error (expected for invalid input): %v", err)
		}

		// GetLatestLocation 不应 panic
		got, err := store.GetLatestLocation(ctx, vehicleID)
		if err != nil {
			return
		}
		if got == nil {
			t.Errorf("GetLatestLocation returned nil after SaveLocation for vehicleID=%q", vehicleID)
			return
		}

		// 验证往返一致性
		if got.VehicleID != vehicleID {
			t.Errorf("VehicleID round-trip mismatch: got %q, want %q", got.VehicleID, vehicleID)
		}
		if got.Phone != phone {
			t.Errorf("Phone round-trip mismatch: got %q, want %q", got.Phone, phone)
		}
	})
}

// FuzzMemoryStore_SaveVehicle 随机车辆数据往返测试
func FuzzMemoryStore_SaveVehicle(f *testing.F) {
	f.Add("v1", "13800138000", "京A12345", 1)
	f.Add("", "", "", 0)
	f.Add("v2", "10086", "沪B99999", 5)
	f.Add("v3", "12345678901", "粤Z-00000", 9)

	f.Fuzz(func(t *testing.T, id, phone, plateNo string, plateColor int) {
		store := NewMemoryStore(20)
		ctx := context.Background()

		v := &storage.Vehicle{
			ID:         id,
			Phone:      phone,
			Protocol:   "jt808",
			PlateNo:    plateNo,
			PlateColor: plateColor,
			Online:     true,
		}

		if err := store.SaveVehicle(ctx, v); err != nil {
			t.Skipf("SaveVehicle returned error: %v", err)
		}

		got, err := store.GetVehicle(ctx, id)
		if err != nil {
			t.Errorf("GetVehicle failed after SaveVehicle: %v", err)
			return
		}
		if got.ID != id {
			t.Errorf("ID round-trip mismatch: got %q, want %q", got.ID, id)
		}
		if got.Phone != phone {
			t.Errorf("Phone round-trip mismatch: got %q, want %q", got.Phone, phone)
		}
		if got.PlateNo != plateNo {
			t.Errorf("PlateNo round-trip mismatch: got %q, want %q", got.PlateNo, plateNo)
		}
	})
}

// FuzzMemoryStore_SaveAlarm 随机告警数据往返测试
func FuzzMemoryStore_SaveAlarm(f *testing.F) {
	f.Add("a1", "v1", "13800138000", "overspeed", 1)
	f.Add("", "", "", "", 0)
	f.Add("a2", "v2", "10086", "dsm_fatigue", 5)
	f.Add("a3", "v3", "12345", "geofence", -1)

	f.Fuzz(func(t *testing.T, id, vehicleID, phone, alarmType string, level int) {
		store := NewMemoryStore(20)
		ctx := context.Background()

		alarm := &storage.AlarmData{
			ID:        id,
			VehicleID: vehicleID,
			Phone:     phone,
			Type:      alarmType,
			Level:     level,
			ReceivedAt: time.Now(),
		}

		if err := store.SaveAlarm(ctx, alarm); err != nil {
			t.Skipf("SaveAlarm returned error: %v", err)
		}

		result, err := store.ListAlarms(ctx, storage.ListOptions{Page: 1, PageSize: 10})
		if err != nil {
			t.Errorf("ListAlarms failed: %v", err)
			return
		}
		if result.Total == 0 {
			t.Errorf("ListAlarms returned 0 results after SaveAlarm")
		}
	})
}
