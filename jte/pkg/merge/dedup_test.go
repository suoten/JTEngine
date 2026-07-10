package merge

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

func TestDeduplicator_808PriorityOver809(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 10000)
	ts := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)

	// 809 先到（级联转发）
	loc809 := &storage.LocationData{
		VehicleID: "veh001", Phone: "13800000000",
		Latitude: 39.9, Longitude: 116.4, Speed: 60,
		Direction: 180, Time: ts, ReceivedAt: ts, Source: SourceJT809,
	}
	r1 := d.Check(loc809)
	if r1.IsDuplicate {
		t.Fatal("first 809 record should not be duplicate")
	}

	// 808 后到（终端直连，同一时刻，坐标略有差异）
	loc808 := &storage.LocationData{
		VehicleID: "veh001", Phone: "13800000000",
		Latitude: 39.9001, Longitude: 116.4001, Speed: 62,
		Direction: 0, Time: ts, ReceivedAt: ts.Add(10 * time.Millisecond), Source: SourceJT808,
	}
	r2 := d.Check(loc808)
	if !r2.IsDuplicate {
		t.Fatal("second record with same device_id+ts should be duplicate")
	}
	// 808 优先：合并后应保留 808 的坐标和速度
	if r2.Merged.Source != SourceJT808 {
		t.Errorf("merged source = %s, want %s (808 priority)", r2.Merged.Source, SourceJT808)
	}
	if r2.Merged.Latitude != 39.9001 {
		t.Errorf("merged latitude = %f, want 39.9001 (808 value)", r2.Merged.Latitude)
	}
	if r2.Merged.Speed != 62 {
		t.Errorf("merged speed = %f, want 62 (808 value)", r2.Merged.Speed)
	}
	if r2.SuppressedSource != SourceJT809 {
		t.Errorf("suppressed source = %s, want %s", r2.SuppressedSource, SourceJT809)
	}
}

func TestDeduplicator_809SupplementsMissingFields(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 10000)
	ts := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)

	// 808 先到，缺失 mileage 和 fuel
	loc808 := &storage.LocationData{
		VehicleID: "veh001", Latitude: 39.9, Longitude: 116.4,
		Speed: 60, Time: ts, ReceivedAt: ts, Source: SourceJT808,
	}
	d.Check(loc808)

	// 809 后到，携带 mileage 和 fuel
	loc809 := &storage.LocationData{
		VehicleID: "veh001", Latitude: 39.91, Longitude: 116.41,
		Speed: 50, Mileage: 12345.6, Fuel: 75.3,
		Time: ts, ReceivedAt: ts.Add(5 * time.Millisecond), Source: SourceJT809,
	}
	r := d.Check(loc809)
	if !r.IsDuplicate {
		t.Fatal("should be duplicate")
	}
	// 808 优先：坐标和速度保留 808
	if r.Merged.Latitude != 39.9 {
		t.Errorf("latitude = %f, want 39.9 (808)", r.Merged.Latitude)
	}
	if r.Merged.Speed != 60 {
		t.Errorf("speed = %f, want 60 (808)", r.Merged.Speed)
	}
	// 809 补充缺失字段
	if r.Merged.Mileage != 12345.6 {
		t.Errorf("mileage = %f, want 12345.6 (supplemented from 809)", r.Merged.Mileage)
	}
	if r.Merged.Fuel != 75.3 {
		t.Errorf("fuel = %f, want 75.3 (supplemented from 809)", r.Merged.Fuel)
	}
}

func TestDeduplicator_DifferentDevicesNotDuplicate(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 10000)
	ts := time.Now()

	loc1 := &storage.LocationData{VehicleID: "veh001", Time: ts, Source: SourceJT808}
	loc2 := &storage.LocationData{VehicleID: "veh002", Time: ts, Source: SourceJT808}

	r1 := d.Check(loc1)
	r2 := d.Check(loc2)
	if r1.IsDuplicate || r2.IsDuplicate {
		t.Fatal("different devices should not be duplicate")
	}
}

func TestDeduplicator_DifferentTimeNotDuplicate(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 10000)
	ts := time.Now()

	loc1 := &storage.LocationData{VehicleID: "veh001", Time: ts, Source: SourceJT808}
	loc2 := &storage.LocationData{VehicleID: "veh001", Time: ts.Add(2 * time.Second), Source: SourceJT808}

	r1 := d.Check(loc1)
	r2 := d.Check(loc2)
	if r1.IsDuplicate || r2.IsDuplicate {
		t.Fatal("different timestamps (>=1s apart) should not be duplicate")
	}
}

func TestDeduplicator_SameSecondDifferentMsDuplicate(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 10000)
	ts := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)

	// 同一秒内，毫秒不同 → 视为重复（截断到秒）
	loc1 := &storage.LocationData{VehicleID: "veh001", Time: ts, Source: SourceJT808}
	loc2 := &storage.LocationData{VehicleID: "veh001", Time: ts.Add(500 * time.Millisecond), Source: SourceJT808}

	d.Check(loc1)
	r := d.Check(loc2)
	if !r.IsDuplicate {
		t.Fatal("same second (different ms) should be duplicate")
	}
}

func TestDeduplicator_Concurrent(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 100000)
	ts := time.Now()

	var wg sync.WaitGroup
	var dupCount int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loc := &storage.LocationData{
				VehicleID: "veh001", Time: ts, Source: SourceJT808,
			}
			r := d.Check(loc)
			if r.IsDuplicate {
				atomic.AddInt32(&dupCount, 1)
			}
		}()
	}
	wg.Wait()
	// 100 个并发，同 key，第一个不重复，其余 99 个重复
	if got := atomic.LoadInt32(&dupCount); got != 99 {
		t.Errorf("dupCount = %d, want 99", got)
	}
}

func TestDeduplicator_Eviction(t *testing.T) {
	d := NewDeduplicator(1*time.Second, 3) // maxSize=3
	ts := time.Now()

	// 插入 4 个不同 key，触发淘汰
	for i := 0; i < 4; i++ {
		loc := &storage.LocationData{
			VehicleID: "veh00" + string(rune('1'+i)),
			Time:      ts.Add(time.Duration(i) * time.Second),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}
	stats := d.Stats()
	if stats.TrackedKeys > 3 {
		t.Errorf("tracked keys = %d, want <= 3 (maxSize)", stats.TrackedKeys)
	}
}

func TestDeduplicator_Both808_NewerReceivedAtWins(t *testing.T) {
	d := NewDeduplicator(5*time.Second, 10000)
	ts := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)

	// 两条都是 808，第二条 ReceivedAt 更新 → 第二条为主
	loc1 := &storage.LocationData{
		VehicleID: "veh001", Latitude: 39.9, Speed: 50,
		Time: ts, ReceivedAt: ts, Source: SourceJT808,
	}
	loc2 := &storage.LocationData{
		VehicleID: "veh001", Latitude: 39.91, Speed: 60,
		Time: ts, ReceivedAt: ts.Add(1 * time.Second), Source: SourceJT808,
	}
	d.Check(loc1)
	r := d.Check(loc2)
	if !r.IsDuplicate {
		t.Fatal("should be duplicate")
	}
	// 同源时，ReceivedAt 更新的为主
	if r.Merged.Latitude != 39.91 {
		t.Errorf("latitude = %f, want 39.91 (newer received_at)", r.Merged.Latitude)
	}
}
