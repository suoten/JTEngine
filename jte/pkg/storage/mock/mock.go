// Package mock 提供 v2.0 分层存储接口的内存 mock 实现，用于测试。
// 实现 TimeSeriesStorage / LocationTimeSeries / AlarmTimeSeries / CANTimeSeries /
// CacheStorage / ObjectStorage，全部基于内存 map，无外部依赖。
package mock

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

// ===================================================================
// MockTimeSeries — TimeSeriesStorage 接口的内存实现
// ===================================================================

// MockTimeSeries 内存时序存储，支持按表/标签索引行数据。
type MockTimeSeries struct {
	mu      sync.RWMutex
	tables  map[string][]storage.TimeSeriesRow // table -> rows
	stats   storage.StorageStats
	closed  bool
	failNext bool // 测试用：下一次调用返回错误
	err     error
}

func NewTimeSeries() *MockTimeSeries {
	return &MockTimeSeries{
		tables: make(map[string][]storage.TimeSeriesRow),
	}
}

// FailNext 设置下一次调用返回指定错误（测试注入故障用）。
func (m *MockTimeSeries) FailNext(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = true
	m.err = err
}

func (m *MockTimeSeries) checkFail() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return m.err
	}
	return nil
}

func (m *MockTimeSeries) BatchWrite(ctx context.Context, table string, rows []storage.TimeSeriesRow) error {
	if err := m.checkFail(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("storage closed")
	}
	m.tables[table] = append(m.tables[table], rows...)
	m.stats.TotalRows += int64(len(rows))
	return nil
}

func (m *MockTimeSeries) QueryRange(ctx context.Context, table string, tags map[string]string, start, end time.Time) ([]storage.TimeSeriesRow, error) {
	if err := m.checkFail(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.tables[table]
	result := make([]storage.TimeSeriesRow, 0, len(rows))
	for _, r := range rows {
		if !r.Timestamp.Before(start) && !r.Timestamp.After(end) && tagsMatch(r.Tags, tags) {
			result = append(result, r)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.Before(result[j].Timestamp) })
	return result, nil
}

func (m *MockTimeSeries) QueryLast(ctx context.Context, table string, tags map[string]string) (*storage.TimeSeriesRow, error) {
	if err := m.checkFail(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.tables[table]
	var last *storage.TimeSeriesRow
	for i := range rows {
		if tagsMatch(rows[i].Tags, tags) {
			if last == nil || rows[i].Timestamp.After(last.Timestamp) {
				r := rows[i]
				last = &r
			}
		}
	}
	if last == nil {
		return nil, storage.ErrNotFound
	}
	return last, nil
}

func (m *MockTimeSeries) QueryAggregate(ctx context.Context, table string, tags map[string]string, start, end time.Time, interval time.Duration, aggFunc string) ([]storage.AggregateRow, error) {
	rows, err := m.QueryRange(ctx, table, tags, start, end)
	if err != nil {
		return nil, err
	}
	// 简化：按 interval 分桶聚合，仅支持 avg/sum/max/min/count
	buckets := make(map[int64][]storage.TimeSeriesRow)
	for _, r := range rows {
		bucket := r.Timestamp.UnixNano() / int64(interval)
		buckets[bucket] = append(buckets[bucket], r)
	}
	result := make([]storage.AggregateRow, 0, len(buckets))
	for bucket, rs := range buckets {
		ts := time.Unix(0, bucket*int64(interval))
		agg := storage.AggregateRow{Timestamp: ts, Values: make(map[string]float64)}
		for _, r := range rs {
			for k, v := range r.Fields {
				fv, ok := toFloat64(v)
				if !ok {
					continue
				}
				switch aggFunc {
				case "sum":
					agg.Values[k] += fv
				case "max":
					if cur, ok := agg.Values[k]; !ok || fv > cur {
						agg.Values[k] = fv
					}
				case "min":
					if cur, ok := agg.Values[k]; !ok || fv < cur {
						agg.Values[k] = fv
					}
				case "count":
					agg.Values[k]++
				default: // avg
					agg.Values[k+"_sum"] += fv
					agg.Values[k+"_count"]++
				}
			}
		}
		if aggFunc == "" || aggFunc == "avg" {
			for k := range agg.Values {
				if strings.HasSuffix(k, "_sum") {
					base := strings.TrimSuffix(k, "_sum")
					if cnt, ok := agg.Values[base+"_count"]; ok && cnt > 0 {
						agg.Values[base] = agg.Values[k] / cnt
					}
					delete(agg.Values, k)
					delete(agg.Values, base+"_count")
				}
			}
		}
		result = append(result, agg)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.Before(result[j].Timestamp) })
	return result, nil
}

func (m *MockTimeSeries) QueryDownsample(ctx context.Context, table string, tags map[string]string, start, end time.Time, interval time.Duration) ([]storage.TimeSeriesRow, error) {
	return m.QueryRange(ctx, table, tags, start, end)
}

func (m *MockTimeSeries) DeleteRange(ctx context.Context, table string, tags map[string]string, start, end time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.tables[table]
	kept := rows[:0]
	deleted := 0
	for _, r := range rows {
		if !r.Timestamp.Before(start) && !r.Timestamp.After(end) && tagsMatch(r.Tags, tags) {
			deleted++
			continue
		}
		kept = append(kept, r)
	}
	m.tables[table] = kept
	m.stats.TotalRows -= int64(deleted)
	return nil
}

func (m *MockTimeSeries) CreateSubTable(ctx context.Context, stable, subTable string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.TableCount++
	if _, ok := m.tables[subTable]; !ok {
		m.tables[subTable] = make([]storage.TimeSeriesRow, 0)
	}
	return nil
}

func (m *MockTimeSeries) GetStats(ctx context.Context) (*storage.StorageStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.stats
	return &s, nil
}

func (m *MockTimeSeries) HealthCheck(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return fmt.Errorf("storage closed")
	}
	return nil
}

func (m *MockTimeSeries) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// RowCount 返回指定表行数（测试辅助）。
func (m *MockTimeSeries) RowCount(table string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.tables[table])
}

// ===================================================================
// MockLocationTS — LocationTimeSeries 接口的内存实现
// ===================================================================

type MockLocationTS struct {
	mu       sync.RWMutex
	data     map[string][]*storage.LocationData // deviceID -> locations
	closed   bool
	failNext bool
	err      error
}

func NewLocationTS() *MockLocationTS {
	return &MockLocationTS{data: make(map[string][]*storage.LocationData)}
}

func (m *MockLocationTS) FailNext(err error) {
	m.mu.Lock()
	m.failNext = true
	m.err = err
	m.mu.Unlock()
}

func (m *MockLocationTS) checkFail() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext {
		m.failNext = false
		return m.err
	}
	return nil
}

func (m *MockLocationTS) WriteLocation(ctx context.Context, loc *storage.LocationData) error {
	if err := m.checkFail(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[loc.VehicleID] = append(m.data[loc.VehicleID], loc)
	return nil
}

func (m *MockLocationTS) WriteLocations(ctx context.Context, locs []*storage.LocationData) error {
	if err := m.checkFail(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, loc := range locs {
		m.data[loc.VehicleID] = append(m.data[loc.VehicleID], loc)
	}
	return nil
}

func (m *MockLocationTS) QueryLocation(ctx context.Context, deviceID string, start, end time.Time) ([]*storage.LocationData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.data[deviceID]
	result := make([]*storage.LocationData, 0)
	for _, r := range rows {
		if !r.Time.Before(start) && !r.Time.After(end) {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *MockLocationTS) QueryLocationLatest(ctx context.Context, deviceID string) (*storage.LocationData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.data[deviceID]
	if len(rows) == 0 {
		return nil, storage.ErrNotFound
	}
	return rows[len(rows)-1], nil
}

func (m *MockLocationTS) QueryLocationAggregate(ctx context.Context, deviceID string, start, end time.Time, interval time.Duration) ([]*storage.LocationAgg, error) {
	rows, err := m.QueryLocation(ctx, deviceID, start, end)
	if err != nil {
		return nil, err
	}
	buckets := make(map[int64][]*storage.LocationData)
	for _, r := range rows {
		b := r.Time.UnixNano() / int64(interval)
		buckets[b] = append(buckets[b], r)
	}
	result := make([]*storage.LocationAgg, 0, len(buckets))
	for b, rs := range buckets {
		agg := &storage.LocationAgg{Timestamp: time.Unix(0, b*int64(interval))}
		for _, r := range rs {
			agg.AvgSpeed += r.Speed
			if r.Speed > agg.MaxSpeed {
				agg.MaxSpeed = r.Speed
			}
			if agg.MinSpeed == 0 || (r.Speed < agg.MinSpeed && r.Speed > 0) {
				agg.MinSpeed = r.Speed
			}
		}
		if len(rs) > 0 {
			agg.AvgSpeed /= float64(len(rs))
		}
		result = append(result, agg)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.Before(result[j].Timestamp) })
	return result, nil
}

// ===================================================================
// MockAlarmTS — AlarmTimeSeries 接口的内存实现
// ===================================================================

type MockAlarmTS struct {
	mu   sync.RWMutex
	data []*storage.AlarmData
}

func NewAlarmTS() *MockAlarmTS {
	return &MockAlarmTS{}
}

func (m *MockAlarmTS) WriteAlarm(ctx context.Context, alarm *storage.AlarmData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append(m.data, alarm)
	return nil
}

func (m *MockAlarmTS) QueryAlarm(ctx context.Context, filter storage.AlarmFilter) ([]*storage.AlarmData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*storage.AlarmData, 0)
	for _, a := range m.data {
		if filter.DeviceID != "" && a.VehicleID != filter.DeviceID {
			continue
		}
		if filter.Phone != "" && a.Phone != filter.Phone {
			continue
		}
		if filter.Source != "" && a.Source != filter.Source {
			continue
		}
		if filter.Type != "" && a.Type != filter.Type {
			continue
		}
		if filter.Level > 0 && a.Level != filter.Level {
			continue
		}
		if !filter.Start.IsZero() && a.Time.Before(filter.Start) {
			continue
		}
		if !filter.End.IsZero() && a.Time.After(filter.End) {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}

func (m *MockAlarmTS) QueryAlarmStats(ctx context.Context, filter storage.AlarmFilter) (*storage.AlarmStats, error) {
	alarms, err := m.QueryAlarm(ctx, filter)
	if err != nil {
		return nil, err
	}
	stats := &storage.AlarmStats{
		ByType:   make(map[string]int64),
		ByLevel:  make(map[int]int64),
		BySource: make(map[string]int64),
	}
	for _, a := range alarms {
		stats.Total++
		stats.ByType[a.Type]++
		stats.ByLevel[a.Level]++
		stats.BySource[a.Source]++
	}
	return stats, nil
}

// ===================================================================
// MockCAN — CANTimeSeries 接口的内存实现
// ===================================================================

type MockCAN struct {
	mu   sync.RWMutex
	data map[string][]*storage.CANData // deviceID -> rows
}

func NewCAN() *MockCAN {
	return &MockCAN{data: make(map[string][]*storage.CANData)}
}

func (m *MockCAN) WriteCAN(ctx context.Context, data *storage.CANData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[data.DeviceID] = append(m.data[data.DeviceID], data)
	return nil
}

func (m *MockCAN) WriteCANs(ctx context.Context, datas []*storage.CANData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range datas {
		m.data[d.DeviceID] = append(m.data[d.DeviceID], d)
	}
	return nil
}

func (m *MockCAN) QueryCAN(ctx context.Context, deviceID string, start, end time.Time) ([]*storage.CANData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.data[deviceID]
	result := make([]*storage.CANData, 0)
	for _, r := range rows {
		if !r.Time.Before(start) && !r.Time.After(end) {
			result = append(result, r)
		}
	}
	return result, nil
}

// ===================================================================
// MockCache — CacheStorage 接口的内存实现
// ===================================================================

type MockCache struct {
	mu             sync.RWMutex
	kv             map[string][]byte        // 通用 K/V
	ttls           map[string]time.Time     // key -> 过期时间
	onlineStates   map[string]*storage.DeviceOnlineState
	latestLoc      map[string]*storage.LocationData // vehicleID -> latest
	closed         bool
	setShouldFail  bool // 测试：SetLatestLocation 返回错误（双写一致性测试用）
}

func NewCache() *MockCache {
	return &MockCache{
		kv:           make(map[string][]byte),
		ttls:         make(map[string]time.Time),
		onlineStates: make(map[string]*storage.DeviceOnlineState),
		latestLoc:    make(map[string]*storage.LocationData),
	}
}

// SetSetFail 让后续 SetLatestLocation 返回错误（测试双写一致性）。
func (m *MockCache) SetSetFail(fail bool) {
	m.mu.Lock()
	m.setShouldFail = fail
	m.mu.Unlock()
}

func (m *MockCache) expired(key string) bool {
	if exp, ok := m.ttls[key]; ok && time.Now().After(exp) {
		return true
	}
	return false
}

func (m *MockCache) CacheSet(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := encode(value)
	if err != nil {
		return err
	}
	m.kv[key] = data
	if ttl > 0 {
		m.ttls[key] = time.Now().Add(ttl)
	} else {
		delete(m.ttls, key)
	}
	return nil
}

func (m *MockCache) CacheGet(ctx context.Context, key string, out interface{}) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.expired(key) {
		return storage.ErrNotFound
	}
	data, ok := m.kv[key]
	if !ok {
		return storage.ErrNotFound
	}
	return decode(data, out)
}

func (m *MockCache) CacheDelete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, key)
	delete(m.ttls, key)
	return nil
}

func (m *MockCache) SetOnlineState(ctx context.Context, state *storage.DeviceOnlineState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onlineStates[state.DeviceID] = state
	return nil
}

func (m *MockCache) GetOnlineState(ctx context.Context, deviceID string) (*storage.DeviceOnlineState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.onlineStates[deviceID]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return s, nil
}

func (m *MockCache) DeleteOnlineState(ctx context.Context, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.onlineStates, deviceID)
	return nil
}

func (m *MockCache) ListOnlineStates(ctx context.Context) ([]*storage.DeviceOnlineState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*storage.DeviceOnlineState, 0, len(m.onlineStates))
	for _, s := range m.onlineStates {
		if s.Online {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *MockCache) GetOnlineCount(ctx context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var count int64
	for _, s := range m.onlineStates {
		if s.Online {
			count++
		}
	}
	return count, nil
}

func (m *MockCache) SetLatestLocation(ctx context.Context, loc *storage.LocationData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setShouldFail {
		return fmt.Errorf("mock cache set fail (injected)")
	}
	cp := *loc
	m.latestLoc[loc.VehicleID] = &cp
	return nil
}

func (m *MockCache) GetLatestLocation(ctx context.Context, vehicleID string) (*storage.LocationData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	loc, ok := m.latestLoc[vehicleID]
	if !ok {
		return nil, storage.ErrNotFound
	}
	cp := *loc
	return &cp, nil
}

func (m *MockCache) HealthCheck(ctx context.Context) error {
	return nil
}

func (m *MockCache) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// ===================================================================
// MockObject — ObjectStorage 接口的内存实现
// ===================================================================

type objectEntry struct {
	data []byte
}

type MockObject struct {
	mu      sync.RWMutex
	objects map[string]map[string]*objectEntry // bucket -> key -> entry
}

func NewObject() *MockObject {
	return &MockObject{objects: make(map[string]map[string]*objectEntry)}
}

func bucketKey(bucket, key string) string {
	return bucket + "/" + key
}

func (m *MockObject) ObjectPut(ctx context.Context, bucket, key string, data io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	if _, ok := m.objects[bucket]; !ok {
		m.objects[bucket] = make(map[string]*objectEntry)
	}
	m.objects[bucket][key] = &objectEntry{data: b}
	return nil
}

func (m *MockObject) ObjectGet(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.objects[bucket]
	if !ok {
		return nil, storage.ErrNotFound
	}
	entry, ok := b[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(entry.data)), nil
}

func (m *MockObject) ObjectDelete(ctx context.Context, bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.objects[bucket]; ok {
		delete(b, key)
	}
	return nil
}

func (m *MockObject) ObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if b, ok := m.objects[bucket]; ok {
		_, exists := b[key]
		return exists, nil
	}
	return false, nil
}

func (m *MockObject) EnsureBucket(ctx context.Context, bucket string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[bucket]; !ok {
		m.objects[bucket] = make(map[string]*objectEntry)
	}
	return nil
}

func (m *MockObject) ArchiveLocation(ctx context.Context, deviceID string, start, end time.Time, data io.Reader) (string, error) {
	key := fmt.Sprintf("archive/%s/%s_%s.jsonl", deviceID, start.Format("20060102"), end.Format("20060102"))
	return key, m.ObjectPut(ctx, "jte-archive", key, data)
}

// GetArchivedLocation 按 archive_key 下载已归档的轨迹数据（AUTO-FIX-2026-07-02）
func (m *MockObject) GetArchivedLocation(ctx context.Context, archiveKey string) (io.ReadCloser, error) {
	return m.ObjectGet(ctx, "jte-archive", archiveKey)
}

func (m *MockObject) PutVideo(ctx context.Context, deviceID string, channelID int, key string, data io.Reader) (string, error) {
	fullKey := fmt.Sprintf("video/%s/ch%d/%s", deviceID, channelID, key)
	return fullKey, m.ObjectPut(ctx, "jte-video", fullKey, data)
}

func (m *MockObject) GetVideo(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return m.ObjectGet(ctx, bucket, key)
}

func (m *MockObject) HealthCheck(ctx context.Context) error {
	return nil
}

func (m *MockObject) Close() error {
	return nil
}

// ListArchiveObjects 列出归档对象 key（测试辅助）。
func (m *MockObject) ListArchiveObjects(bucket, prefix string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.objects[bucket]
	if !ok {
		return nil
	}
	result := make([]string, 0)
	for key := range b {
		if strings.HasPrefix(key, prefix) {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

// ===================================================================
// 辅助函数
// ===================================================================

func tagsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	default:
		return 0, false
	}
}
