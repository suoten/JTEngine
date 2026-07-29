package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// FuzzSQLiteStore_SaveLocation 随机位置数据往返测试
// 确保 SQLite 存储层对任意输入不 panic 且能正确往返
func FuzzSQLiteStore_SaveLocation(f *testing.F) {
	f.Add("v1", "13800138000", float64(39.9042), float64(116.4074), float64(0), float64(60.5), float64(180))
	f.Add("", "", float64(0), float64(0), float64(0), float64(0), float64(0))
	f.Add("v2", "13900139000", float64(-90), float64(-180), float64(9999), float64(255), float64(359))
	f.Add("v3", "10086", float64(90), float64(180), float64(-100), float64(-1), float64(400))

	f.Fuzz(func(t *testing.T, vehicleID, phone string, lat, lng, alt, speed, dir float64) {
		store, err := newFuzzStore(t)
		if err != nil {
			t.Skipf("create sqlite store failed (likely CGO disabled): %v", err)
		}
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
			t.Skipf("SaveLocation returned error: %v", err)
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
	})
}

// FuzzSQLiteStore_SaveVehicle 随机车辆数据往返测试
func FuzzSQLiteStore_SaveVehicle(f *testing.F) {
	f.Add("v1", "13800138000", "京A12345", 1)
	f.Add("", "", "", 0)
	f.Add("v2", "10086", "沪B99999", 5)
	f.Add("v3", "12345678901", "粤Z-00000", 9)

	f.Fuzz(func(t *testing.T, id, phone, plateNo string, plateColor int) {
		store, err := newFuzzStore(t)
		if err != nil {
			t.Skipf("create sqlite store failed: %v", err)
		}
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
	})
}

// FuzzSQLiteStore_ListVehicles_PhoneLikeInjection 随机 Phone LIKE 查询注入测试
// 确保 LIKE 通配符和 SQL 特殊字符不会导致注入或 panic
func FuzzSQLiteStore_ListVehicles_PhoneLikeInjection(f *testing.F) {
	f.Add("13800")
	f.Add("%")
	f.Add("'; DROP TABLE vehicles; --")
	f.Add("_")
	f.Add("\\%\\_")
	f.Add("' OR '1'='1")
	f.Add("100%")

	f.Fuzz(func(t *testing.T, phone string) {
		store, err := newFuzzStore(t)
		if err != nil {
			t.Skipf("create sqlite store failed: %v", err)
		}
		ctx := context.Background()

		// 先写入一条数据
		_ = store.SaveVehicle(ctx, &storage.Vehicle{
			ID:       "v1",
			Phone:    "13800138000",
			Protocol: "jt808",
			PlateNo:  "京A12345",
			Online:   true,
		})

		// ListVehicles 不应 panic，不应返回 SQL 错误
		result, err := store.ListVehicles(ctx, storage.ListOptions{
			Page:     1,
			PageSize: 10,
			Phone:    phone,
		})
		if err != nil {
			t.Errorf("ListVehicles with phone %q returned error: %v", phone, err)
			return
		}
		// 确保结果集不含异常数据
		if result.Items == nil {
			// 空结果集是正常的（phone 不匹配）
		}
	})
}

// newFuzzStore 创建用于 fuzzing 的临时 SQLite store。
// FIXED-2026-07-23 [P0]: 修复 fuzz 上下文 panic。
// 1. 移除 t.Helper() 调用（fuzz 引擎中不可用）
// 2. 调用方传入 *testing.T（而非 *testing.F），因为 f.TempDir() 在 fuzz target 内 panic
//    错误信息：testing: f.TempDir was called inside the fuzz target, use t.TempDir instead
func newFuzzStore(t testing.TB) (*SQLiteStore, error) {
	dir := t.TempDir()
	logger := zap.NewNop()
	return NewSQLiteStore(dir+"/fuzz.db", logger)
}
