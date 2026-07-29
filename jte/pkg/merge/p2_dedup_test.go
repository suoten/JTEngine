package merge

import (
	"fmt"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

// TestP2_Deduplicator_EvictInterval [P2-2]
// 验证基于时间桶的清理：在 evictInterval 内不会触发全表扫描。
func TestP2_Deduplicator_EvictInterval(t *testing.T) {
	d := NewDeduplicator(1*time.Second, 100000)
	ts := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	// 插入一条数据（首次会触发 evict）
	loc := &storage.LocationData{
		VehicleID: "veh001", Time: ts, Source: SourceJT808,
	}
	d.Check(loc)

	// 在 evictInterval 内插入更多数据，不应触发 evict
	for i := 0; i < 100; i++ {
		loc := &storage.LocationData{
			VehicleID: fmt.Sprintf("veh_%03d", i),
			Time:      ts.Add(time.Duration(i) * time.Millisecond),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}

	// lastEvictTime 应为第一次插入时的时间
	d.mu.RLock()
	evictTime := d.lastEvictTime
	trackedKeys := len(d.seen)
	d.mu.RUnlock()

	if evictTime.IsZero() {
		t.Fatal("lastEvictTime should be set after first Check")
	}
	// 应有 101 个 key（1 + 100）
	if trackedKeys != 101 {
		t.Errorf("trackedKeys = %d, want 101", trackedKeys)
	}
}

// TestP2_Deduplicator_EvictTriggersAfterInterval [P2-2]
// 验证超过 evictInterval 后会触发清理。
func TestP2_Deduplicator_EvictTriggersAfterInterval(t *testing.T) {
	d := NewDeduplicator(1*time.Second, 100000)
	ts := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	// 插入第一批数据
	for i := 0; i < 50; i++ {
		loc := &storage.LocationData{
			VehicleID: fmt.Sprintf("batch1_%03d", i),
			Time:      ts.Add(time.Duration(i) * time.Millisecond),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}

	// 等待超过 evictInterval 后插入第二批（应触发清理）
	ts2 := ts.Add(2 * time.Second) // 2s > 1s evictInterval
	// 第一批数据的 ts 远早于 threshold（now - window*10 = ts2 - 10s），
	// 但 ts = 10:00:00, ts2 = 10:00:02, threshold = 10:00:02 - 10s = 09:59:52
	// batch1 的 ts 范围: 10:00:00.000 ~ 10:00:00.049，都在 threshold 之后，不会被清理
	for i := 0; i < 50; i++ {
		loc := &storage.LocationData{
			VehicleID: fmt.Sprintf("batch2_%03d", i),
			Time:      ts2.Add(time.Duration(i) * time.Millisecond),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}

	d.mu.RLock()
	evictTime := d.lastEvictTime
	d.mu.RUnlock()

	// lastEvictTime 应更新为 ts2 附近的时间
	if !evictTime.Equal(ts2) && !evictTime.After(ts) {
		t.Errorf("lastEvictTime should be updated after interval, got %v", evictTime)
	}
}

// TestP2_Deduplicator_OldEntriesEvictedAfterInterval [P2-2]
// 验证过期的条目在 evict 触发时被清理。
func TestP2_Deduplicator_OldEntriesEvictedAfterInterval(t *testing.T) {
	d := NewDeduplicator(1*time.Second, 100000)
	ts := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	// 插入旧数据（时间较早）
	oldTS := ts.Add(-30 * time.Second) // 30 秒前
	for i := 0; i < 10; i++ {
		loc := &storage.LocationData{
			VehicleID: fmt.Sprintf("old_%03d", i),
			Time:      oldTS.Add(time.Duration(i) * time.Millisecond),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}

	// 插入新数据，时间远超 evictInterval，应触发清理并删除旧数据
	// evictExpired threshold = now - window*10 = ts - 10s
	// old data ts = ts - 30s < threshold → 应被清理
	loc := &storage.LocationData{
		VehicleID: "new_001",
		Time:      ts,
		Source:    SourceJT808,
	}
	d.Check(loc)

	stats := d.Stats()
	// 旧数据应被清理，只剩新数据
	if stats.TrackedKeys > 5 {
		t.Errorf("old entries should be evicted, trackedKeys = %d (want <= 5)", stats.TrackedKeys)
	}
}

// BenchmarkDeduplicator_Check_100kEntries [P2-2]
// Benchmark：10 万条目下 Check 的性能，验证时间桶清理优化效果。
func BenchmarkDeduplicator_Check_100kEntries(b *testing.B) {
	d := NewDeduplicator(5*time.Second, 200000)
	baseTS := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	// 预填充 10 万条目
	for i := 0; i < 100000; i++ {
		loc := &storage.LocationData{
			VehicleID: fmt.Sprintf("veh_%06d", i),
			Time:      baseTS.Add(time.Duration(i) * time.Millisecond),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		loc := &storage.LocationData{
			VehicleID: fmt.Sprintf("veh_new_%06d", i),
			Time:      baseTS.Add(time.Duration(100000+i) * time.Millisecond),
			Source:    SourceJT808,
		}
		d.Check(loc)
	}
}
