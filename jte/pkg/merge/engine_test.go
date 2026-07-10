package merge

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
	"github.com/suoten/jt-engine/pkg/storage/memory"
	"go.uber.org/zap"
)

func newTestEngine() *Engine {
	logger := zap.NewNop()
	store := memory.NewMemoryStore(100)
	return NewEngine(store, logger, nil)
}

func TestEngine_Merge_NewLocation(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	loc := &storage.LocationData{
		VehicleID:  "v1",
		Phone:      "13800000001",
		Latitude:   39.9042,
		Longitude:  116.4074,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}

	if err := e.Merge(context.Background(), loc); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	got, ok := e.GetLatestLocation("v1")
	if !ok {
		t.Fatal("expected location to exist")
	}
	if got.VehicleID != "v1" {
		t.Errorf("expected VehicleID=v1, got %s", got.VehicleID)
	}
}

func TestEngine_Merge_Dedup(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	now := time.Now()
	loc1 := &storage.LocationData{
		VehicleID:  "v1",
		Latitude:   39.9042,
		Longitude:  116.4074,
		ReceivedAt: now,
		Source:     "jt808",
	}
	loc2 := &storage.LocationData{
		VehicleID:  "v1",
		Latitude:   39.9042,
		Longitude:  116.4074,
		ReceivedAt: now.Add(1 * time.Second),
		Source:     "jt808",
	}

	if err := e.Merge(context.Background(), loc1); err != nil {
		t.Fatalf("Merge1 failed: %v", err)
	}
	if err := e.Merge(context.Background(), loc2); err != nil {
		t.Fatalf("Merge2 failed: %v", err)
	}

	got, ok := e.GetLatestLocation("v1")
	if !ok {
		t.Fatal("expected location to exist")
	}
	if got.Latitude != 39.9042 {
		t.Errorf("expected lat=39.9042, got %f", got.Latitude)
	}
}

func TestEngine_RemoveVehicle(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	loc := &storage.LocationData{
		VehicleID:  "v1",
		Latitude:   39.9042,
		Longitude:  116.4074,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	e.Merge(context.Background(), loc)

	e.RemoveVehicle("v1")

	_, ok := e.GetLatestLocation("v1")
	if ok {
		t.Error("expected location to be removed")
	}
}

func TestEngine_CleanupExpiredData(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	old := &storage.LocationData{
		VehicleID:  "v_old",
		Latitude:   39.0,
		Longitude:  116.0,
		ReceivedAt: time.Now().Add(-25 * time.Hour),
		Source:     "jt808",
	}
	recent := &storage.LocationData{
		VehicleID:  "v_recent",
		Latitude:   40.0,
		Longitude:  117.0,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	e.Merge(context.Background(), old)
	e.Merge(context.Background(), recent)

	e.cleanupExpiredData()

	_, okOld := e.GetLatestLocation("v_old")
	if okOld {
		t.Error("expected old location to be cleaned up")
	}
	_, okRecent := e.GetLatestLocation("v_recent")
	if !okRecent {
		t.Error("expected recent location to remain")
	}
}

func TestEngine_SetDedupWindow(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	e.SetDedupWindow(10 * time.Second)
	if e.dedupWindow != 10*time.Second {
		t.Errorf("expected 10s, got %v", e.dedupWindow)
	}
}

func TestEventBus_SubscribePublish(t *testing.T) {
	logger := zap.NewNop()
	eb := NewEventBus(logger)
	defer eb.Stop()

	var received atomic.Int32
	eb.Subscribe(EventTypeLocationUpdate, func(event Event) {
		received.Add(1)
	})

	eb.Publish(EventTypeLocationUpdate, "test")

	time.Sleep(50 * time.Millisecond)
	if received.Load() != 1 {
		t.Errorf("expected 1 event, got %d", received.Load())
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	logger := zap.NewNop()
	eb := NewEventBus(logger)
	defer eb.Stop()

	var received atomic.Int32
	id := eb.Subscribe(EventTypeLocationUpdate, func(event Event) {
		received.Add(1)
	})

	eb.Unsubscribe(id)
	eb.Publish(EventTypeLocationUpdate, "test")

	time.Sleep(50 * time.Millisecond)
	if received.Load() != 0 {
		t.Errorf("expected 0 events after unsubscribe, got %d", received.Load())
	}
}

func TestEventBus_PublishAsync(t *testing.T) {
	logger := zap.NewNop()
	eb := NewEventBus(logger)
	defer eb.Stop()

	var received atomic.Int32
	eb.Subscribe(EventTypeAlarmEvent, func(event Event) {
		received.Add(1)
	})

	eb.PublishAsync(EventTypeAlarmEvent, "test")

	time.Sleep(100 * time.Millisecond)
	if received.Load() != 1 {
		t.Errorf("expected 1 async event, got %d", received.Load())
	}
}

func TestEventBus_MultipleHandlers(t *testing.T) {
	logger := zap.NewNop()
	eb := NewEventBus(logger)
	defer eb.Stop()

	var count atomic.Int32
	eb.Subscribe(EventTypeLocationUpdate, func(event Event) {
		count.Add(1)
	})
	eb.Subscribe(EventTypeLocationUpdate, func(event Event) {
		count.Add(1)
	})

	eb.Publish(EventTypeLocationUpdate, "test")

	time.Sleep(50 * time.Millisecond)
	if count.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", count.Load())
	}
}

func TestEventBus_PanicRecovery(t *testing.T) {
	logger := zap.NewNop()
	eb := NewEventBus(logger)
	defer eb.Stop()

	var received atomic.Int32
	eb.Subscribe(EventTypeLocationUpdate, func(event Event) {
		panic("test panic")
	})
	eb.Subscribe(EventTypeLocationUpdate, func(event Event) {
		received.Add(1)
	})

	eb.Publish(EventTypeLocationUpdate, "test")

	time.Sleep(50 * time.Millisecond)
	if received.Load() != 1 {
		t.Errorf("expected second handler to run after panic, got %d", received.Load())
	}
}

// AUTO-FIX-2026-06-26: 第二轮链路修复 - 批处理写入器测试
func TestEngine_EnableBatchWriters(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	e.EnableBatchWriters(1000, 50*time.Millisecond)
	if !e.IsBatchWriterEnabled() {
		t.Error("expected batch writer to be enabled")
	}
}

func TestLocationBatchWriter_FlushByTimeout(t *testing.T) {
	logger := zap.NewNop()
	store := memory.NewMemoryStore(100)
	w := NewLocationBatchWriter(store, logger, 1000, 50*time.Millisecond)

	loc := &storage.LocationData{
		VehicleID:  "v1",
		Phone:      "13800000001",
		Latitude:   39.9042,
		Longitude:  116.4074,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	w.Add(loc)

	// 等待超时刷新（50ms ticker + 余量）
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	// 验证数据已写入存储（通过存储查询）
	saved, err := store.GetLatestLocation(context.Background(), "v1")
	if err != nil {
		t.Fatalf("GetLatestLocation failed: %v", err)
	}
	if saved == nil {
		t.Fatal("expected location to be saved after flush timeout")
	}
	if saved.VehicleID != "v1" {
		t.Errorf("expected VehicleID=v1, got %s", saved.VehicleID)
	}
}

func TestLocationBatchWriter_FlushByBatchSize(t *testing.T) {
	logger := zap.NewNop()
	store := memory.NewMemoryStore(100)
	w := NewLocationBatchWriter(store, logger, 3, 10*time.Second)

	// 添加 3 条达到批次大小，应立即触发刷新
	for i := 0; i < 3; i++ {
		w.Add(&storage.LocationData{
			VehicleID:  "v_batch",
			Phone:      "13800000001",
			Latitude:   39.9 + float64(i)*0.001,
			Longitude:  116.4,
			ReceivedAt: time.Now(),
			Source:     "jt808",
		})
	}

	time.Sleep(50 * time.Millisecond)
	w.Stop()

	// 验证最新一条已写入
	saved, err := store.GetLatestLocation(context.Background(), "v_batch")
	if err != nil {
		t.Fatalf("GetLatestLocation failed: %v", err)
	}
	if saved == nil {
		t.Fatal("expected location to be saved after batch full")
	}
}

func TestEngine_Merge_WithBatchWriter(t *testing.T) {
	e := newTestEngine()
	defer e.Stop()

	e.EnableBatchWriters(1000, 50*time.Millisecond)

	loc := &storage.LocationData{
		VehicleID:  "v_merge_batch",
		Phone:      "13800000002",
		Latitude:   39.9042,
		Longitude:  116.4074,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}

	if err := e.Merge(context.Background(), loc); err != nil {
		t.Fatalf("Merge with batch writer failed: %v", err)
	}

	// latestData 应立即更新（不等批量写入）
	got, ok := e.GetLatestLocation("v_merge_batch")
	if !ok {
		t.Fatal("expected latest location to be available immediately")
	}
	if got.VehicleID != "v_merge_batch" {
		t.Errorf("expected v_merge_batch, got %s", got.VehicleID)
	}
}