package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jte-engine/jte/pkg/storage"
	"github.com/jte-engine/jte/pkg/storage/mock"
	"go.uber.org/zap"
)

// newTestQueue 创建测试用 WriteQueue（小队列、短超时，便于测试）。
func newTestQueue(t *testing.T, ts storage.TimeSeriesStorage) (*storage.WriteQueue, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := storage.WriteQueueConfig{
		QueueCapacity:       1000,
		BatchSize:           5,
		FlushInterval:       20 * time.Millisecond,
		MaxRetries:          2,
		SpoolDir:            dir,
		SpoolAlertThreshold: 1 << 20, // 1MB
		CompensateInterval:  100 * time.Millisecond,
		Table:               "jte_location",
	}
	wq, err := storage.NewWriteQueue(ts, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWriteQueue failed: %v", err)
	}
	return wq, dir
}

func makeRow(vehicleID string, ts time.Time) storage.TimeSeriesRow {
	return storage.TimeSeriesRow{
		Timestamp: ts,
		Tags: map[string]string{
			"device_id": vehicleID,
		},
		Fields: map[string]interface{}{
			"latitude":  39.9042,
			"longitude": 116.4074,
			"speed":     60.0,
		},
	}
}

// TestWriteQueue_SuccessPath 正常写入路径：写入 → 批量 flush → 成功。
func TestWriteQueue_SuccessPath(t *testing.T) {
	ts := mock.NewTimeSeries()
	wq, _ := newTestQueue(t, ts)
	defer wq.Stop()

	for i := 0; i < 12; i++ {
		if err := wq.Write(context.Background(), "jte_location", makeRow(fmt.Sprintf("v%d", i), time.Now())); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}

	// 等待 flush 完成
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := wq.GetStats()
		if s.TotalWritten >= 12 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := wq.GetStats()
	if s.TotalWritten != 12 {
		t.Errorf("TotalWritten = %d, want 12", s.TotalWritten)
	}
	if s.TotalFailed != 0 {
		t.Errorf("TotalFailed = %d, want 0", s.TotalFailed)
	}
	if s.TotalEnqueued != 12 {
		t.Errorf("TotalEnqueued = %d, want 12", s.TotalEnqueued)
	}
}

// TestWriteQueue_RetryThenSuccess 失败后重试成功。
func TestWriteQueue_RetryThenSuccess(t *testing.T) {
	ts := mock.NewTimeSeries()
	wq, _ := newTestQueue(t, ts)
	defer wq.Stop()

	// 注入一次失败，重试时成功
	ts.FailNext(errors.New("transient error"))

	for i := 0; i < 3; i++ {
		if err := wq.Write(context.Background(), "jte_location", makeRow(fmt.Sprintf("v%d", i), time.Now())); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := wq.GetStats()
		if s.TotalWritten >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := wq.GetStats()
	if s.TotalWritten != 3 {
		t.Errorf("TotalWritten = %d, want 3", s.TotalWritten)
	}
	if s.TotalRetried < 1 {
		t.Errorf("TotalRetried = %d, want >= 1", s.TotalRetried)
	}
}

// TestWriteQueue_SpoolOnPermanentFailure 持续失败 → 落盘 spool。
func TestWriteQueue_SpoolOnPermanentFailure(t *testing.T) {
	ts := mock.NewTimeSeries()
	// 让 BatchWrite 持续返回错误（通过反复注入）
	wq, dir := newTestQueue(t, ts)

	// 用一个自定义 mock 让所有调用都失败
	failingTs := &alwaysFailTS{}
	// 替换底层 ts：重建 wq
	wq.Stop()
	os.RemoveAll(dir)
	dir = t.TempDir()
	cfg := storage.WriteQueueConfig{
		QueueCapacity:       100,
		BatchSize:           2,
		FlushInterval:       10 * time.Millisecond,
		MaxRetries:          1,
		SpoolDir:            dir,
		SpoolAlertThreshold: 1 << 20,
		CompensateInterval:  100 * time.Millisecond,
		Table:               "jte_location",
	}
	wq2, err := storage.NewWriteQueue(failingTs, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWriteQueue failed: %v", err)
	}
	defer wq2.Stop()

	for i := 0; i < 4; i++ {
		_ = wq2.Write(context.Background(), "jte_location", makeRow(fmt.Sprintf("v%d", i), time.Now()))
	}

	// 等待落盘
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := wq2.GetStats()
		if s.TotalFailed >= 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := wq2.GetStats()
	if s.TotalFailed < 4 {
		t.Errorf("TotalFailed = %d, want >= 4", s.TotalFailed)
	}

	// 验证 spool 文件存在且有内容
	spoolPath := filepath.Join(dir, "failed_writes.jsonl")
	info, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatalf("spool file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("spool file is empty")
	}
}

// TestWriteQueue_CompensateRecovers 补偿协程从 spool 恢复数据。
func TestWriteQueue_CompensateRecovers(t *testing.T) {
	// 先用 alwaysFail 写入 spool，然后切换到正常 mock 触发补偿恢复
	ts := mock.NewTimeSeries()
	dir := t.TempDir()
	cfg := storage.WriteQueueConfig{
		QueueCapacity:       100,
		BatchSize:           2,
		FlushInterval:       10 * time.Millisecond,
		MaxRetries:          1,
		SpoolDir:            dir,
		SpoolAlertThreshold: 1 << 20,
		CompensateInterval:  50 * time.Millisecond,
		Table:               "jte_location",
	}

	// 阶段1：用 alwaysFail 写入 spool
	wq1, err := storage.NewWriteQueue(&alwaysFailTS{}, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWriteQueue failed: %v", err)
	}
	for i := 0; i < 4; i++ {
		_ = wq1.Write(context.Background(), "jte_location", makeRow(fmt.Sprintf("v%d", i), time.Now()))
	}
	// 等待落盘
	time.Sleep(300 * time.Millisecond)
	wq1.Stop()

	spoolPath := filepath.Join(dir, "failed_writes.jsonl")
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("spool file not created: %v", err)
	}

	// 阶段2：用正常 mock 重新打开，补偿协程应恢复数据
	cfg2 := cfg
	wq2, err := storage.NewWriteQueue(ts, cfg2, zap.NewNop())
	if err != nil {
		t.Fatalf("NewWriteQueue 2 failed: %v", err)
	}
	defer wq2.Stop()

	// 等待补偿（补偿间隔 50ms，多等几轮）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := wq2.GetStats()
		if s.TotalRecovered >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	s := wq2.GetStats()
	if s.TotalRecovered < 4 {
		t.Errorf("TotalRecovered = %d, want >= 4", s.TotalRecovered)
	}

	// 验证 mock 中确实有数据
	if got := ts.RowCount("jte_location"); got < 4 {
		t.Errorf("mock rows = %d, want >= 4", got)
	}

	// spool 文件应被清空（仍失败的为空）
	info, _ := os.Stat(spoolPath)
	if info != nil && info.Size() > 0 {
		// 补偿成功后 spool 应该被截断为 0
		t.Errorf("spool file size = %d, want 0 after recovery", info.Size())
	}
}

// TestWriteQueue_ConcurrentWrites 并发写入安全。
func TestWriteQueue_ConcurrentWrites(t *testing.T) {
	ts := mock.NewTimeSeries()
	wq, _ := newTestQueue(t, ts)
	defer wq.Stop()

	var wg sync.WaitGroup
	const goroutines = 10
	const perG = 50
	var totalOK int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := wq.Write(context.Background(), "jte_location", makeRow(fmt.Sprintf("v%d_%d", gid, i), time.Now())); err == nil {
					atomic.AddInt64(&totalOK, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := int64(goroutines * perG)
	if totalOK != total {
		t.Errorf("totalOK = %d, want %d", totalOK, total)
	}

	// 等待全部写入完成
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := wq.GetStats()
		if s.TotalWritten >= total {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	s := wq.GetStats()
	if s.TotalWritten != total {
		t.Errorf("TotalWritten = %d, want %d", s.TotalWritten, total)
	}
}

// TestWriteQueue_StatsConsistent GetStats 返回的快照应一致。
func TestWriteQueue_StatsConsistent(t *testing.T) {
	ts := mock.NewTimeSeries()
	wq, _ := newTestQueue(t, ts)
	defer wq.Stop()

	for i := 0; i < 5; i++ {
		_ = wq.Write(context.Background(), "jte_location", makeRow(fmt.Sprintf("v%d", i), time.Now()))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := wq.GetStats()
		if s.TotalWritten >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s1 := wq.GetStats()
	s2 := wq.GetStats()
	// 两次快照应满足不变式：累计值单调不减
	if s2.TotalEnqueued < s1.TotalEnqueued {
		t.Errorf("TotalEnqueued decreased: %d -> %d", s1.TotalEnqueued, s2.TotalEnqueued)
	}
	if s2.TotalWritten < s1.TotalWritten {
		t.Errorf("TotalWritten decreased: %d -> %d", s1.TotalWritten, s2.TotalWritten)
	}
}

// TestWriteQueue_StopIdempotent Stop 多次调用不应 panic。
func TestWriteQueue_StopIdempotent(t *testing.T) {
	ts := mock.NewTimeSeries()
	wq, _ := newTestQueue(t, ts)

	wq.Stop()
	wq.Stop() // 不应 panic
	wq.Stop()
}

// alwaysFailTS 永远失败的 TimeSeriesStorage，用于测试 spool 落盘。
type alwaysFailTS struct{}

func (a *alwaysFailTS) BatchWrite(ctx context.Context, table string, rows []storage.TimeSeriesRow) error {
	return errors.New("permanent failure")
}
func (a *alwaysFailTS) QueryRange(ctx context.Context, table string, tags map[string]string, start, end time.Time) ([]storage.TimeSeriesRow, error) {
	return nil, errors.New("fail")
}
func (a *alwaysFailTS) QueryLast(ctx context.Context, table string, tags map[string]string) (*storage.TimeSeriesRow, error) {
	return nil, errors.New("fail")
}
func (a *alwaysFailTS) QueryAggregate(ctx context.Context, table string, tags map[string]string, start, end time.Time, interval time.Duration, aggFunc string) ([]storage.AggregateRow, error) {
	return nil, errors.New("fail")
}
func (a *alwaysFailTS) QueryDownsample(ctx context.Context, table string, tags map[string]string, start, end time.Time, interval time.Duration) ([]storage.TimeSeriesRow, error) {
	return nil, errors.New("fail")
}
func (a *alwaysFailTS) DeleteRange(ctx context.Context, table string, tags map[string]string, start, end time.Time) error {
	return errors.New("fail")
}
func (a *alwaysFailTS) CreateSubTable(ctx context.Context, stable, subTable string, tags map[string]string) error {
	return errors.New("fail")
}
func (a *alwaysFailTS) GetStats(ctx context.Context) (*storage.StorageStats, error) {
	return nil, errors.New("fail")
}
func (a *alwaysFailTS) HealthCheck(ctx context.Context) error {
	return errors.New("fail")
}
func (a *alwaysFailTS) Close() error { return nil }
