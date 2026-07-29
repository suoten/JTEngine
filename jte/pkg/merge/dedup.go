package merge

import (
	"sync"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

// AUTO-FIX-2026-06-30 [P1-3]: 808 + 809 数据融合层去重
// 同一车辆可能同时通过 808（终端直连）和 809（平台级联转发）上报位置，
// 导致同一时间点产生两条记录。Deduplicator 按 device_id + ts 去重，
// 优先保留 808 数据（终端直连更可靠），809 数据仅用于补充缺失字段。
//
// 设计要点：
//   - 按 (vehicleID, time.TruncatedToSecond) 作为去重 key
//   - 维护近期 key 的环形缓冲区（默认 5 分钟窗口，避免内存无限增长）
//   - 808 优先：当 808 和 809 冲突时，以 808 为准，809 仅补充 808 缺失的字段
//   - 线程安全：sync.RWMutex 保护内部状态

// SourceJT808 / SourceJT809 协议来源常量。
const (
	SourceJT808 = "jt808"
	SourceJT809 = "jt809"
)

// DedupResult 去重结果。
type DedupResult struct {
	// IsDuplicate true 表示该条记录与近期记录重复（同 device_id + ts）。
	IsDuplicate bool
	// Merged 去重合并后的记录（若 IsDuplicate=true，则为合并后结果；
	// 若 IsDuplicate=false，则为原始记录的副本）。
	Merged *storage.LocationData
	// SuppressedSource 被抑制的来源（重复的那条来源）。
	SuppressedSource string
}

// Deduplicator 808+809 位置数据去重器。
type Deduplicator struct {
	mu            sync.RWMutex
	window        time.Duration // 去重时间窗口（同 key 在此窗口内视为重复）
	seen          map[string]dedupEntry
	maxSize       int           // seen map 最大条目数（防内存膨胀）
	lastEvictTime time.Time     // [P2-2] 上次执行 evictExpired 的时间，避免每次 Check 都全表扫描
	evictInterval time.Duration // [P2-2] 触发 evictExpired 的最小间隔
}

type dedupEntry struct {
	ts        time.Time
	source    string
	location  *storage.LocationData
}

// NewDeduplicator 创建去重器。
// window: 相同 (device_id, ts) 在此窗口内视为重复（建议 5s-10s）。
// maxSize: seen map 最大条目数，超过后淘汰最旧条目（建议 100000）。
func NewDeduplicator(window time.Duration, maxSize int) *Deduplicator {
	if window <= 0 {
		window = 5 * time.Second
	}
	if maxSize <= 0 {
		maxSize = 100000
	}
	return &Deduplicator{
		window:        window,
		seen:          make(map[string]dedupEntry),
		maxSize:       maxSize,
		evictInterval: window, // [P2-2] 清理间隔等于去重窗口，避免每次 Check 全表扫描
	}
}

// dedupKey 构造去重键：vehicleID + "|" + 时间戳截断到秒。
// 截断到秒是因为 808 和 809 的时间戳可能有毫秒级差异但代表同一时刻。
func dedupKey(vehicleID string, ts time.Time) string {
	return vehicleID + "|" + ts.Truncate(time.Second).Format(time.RFC3339)
}

// Check 检查一条位置数据是否为重复，并返回合并结果。
// 重复时：优先保留 808 数据，809 数据补充缺失字段。
// 非重复时：记录到 seen map 并返回原始数据。
func (d *Deduplicator) Check(loc *storage.LocationData) DedupResult {
	key := dedupKey(loc.VehicleID, loc.Time)

	d.mu.Lock()
	defer d.mu.Unlock()

	// [P2-2] 基于时间桶的清理：仅在距上次清理超过 evictInterval 时触发 evictExpired，
	// 避免每次 Check 都全表扫描 seen map
	now := loc.Time
	if d.lastEvictTime.IsZero() || now.Sub(d.lastEvictTime) >= d.evictInterval {
		d.evictExpired(now)
		d.lastEvictTime = now
	}

	if existing, ok := d.seen[key]; ok {
		// 找到重复：合并
		merged, suppressed := d.merge(existing.location, existing.source, loc, loc.Source)
		// 更新 seen 中的记录为合并后的结果
		d.seen[key] = dedupEntry{
			ts:       loc.Time,
			source:   merged.Source,
			location: merged,
		}
		return DedupResult{
			IsDuplicate:     true,
			Merged:          merged,
			SuppressedSource: suppressed,
		}
	}

	// 非重复：记录并返回
	d.seen[key] = dedupEntry{
		ts:       loc.Time,
		source:   loc.Source,
		location: loc,
	}

	// 容量超限时淘汰最旧
	if len(d.seen) > d.maxSize {
		d.evictOldest()
	}

	cp := *loc
	return DedupResult{
		IsDuplicate: false,
		Merged:      &cp,
	}
}

// merge 合并两条同一时刻的位置记录。
// 优先保留 808 数据（终端直连更可靠），809 数据仅补充 808 缺失的字段。
// 返回合并后的记录和被抑制的来源。
func (d *Deduplicator) merge(existing *storage.LocationData, existingSource string,
	incoming *storage.LocationData, incomingSource string) (*storage.LocationData, string) {

	// 确定主源（preferred）和辅源（supplement）
	// 808 优先：如果两者之一是 808，以 808 为主
	var primary, secondary *storage.LocationData
	var primarySource, suppressedSource string

	if existingSource == SourceJT808 && incomingSource != SourceJT808 {
		primary = existing
		secondary = incoming
		primarySource = existingSource
		suppressedSource = incomingSource
	} else if incomingSource == SourceJT808 && existingSource != SourceJT808 {
		primary = incoming
		secondary = existing
		primarySource = incomingSource
		suppressedSource = existingSource
	} else {
		// 两者同源（都是 808 或都是 809）：保留时间更新的那条
		if !incoming.ReceivedAt.Before(existing.ReceivedAt) {
			primary = incoming
			secondary = existing
			primarySource = incomingSource
			suppressedSource = existingSource
		} else {
			primary = existing
			secondary = incoming
			primarySource = existingSource
			suppressedSource = incomingSource
		}
	}

	// 以 primary 为基础，补充 secondary 中 primary 缺失的字段
	merged := *primary
	merged.Source = primarySource

	// 补充缺失字段（值为零值的字段从 secondary 补充）
	if merged.Phone == "" && secondary.Phone != "" {
		merged.Phone = secondary.Phone
	}
	if merged.Altitude == 0 && secondary.Altitude != 0 {
		merged.Altitude = secondary.Altitude
	}
	if merged.Mileage == 0 && secondary.Mileage != 0 {
		merged.Mileage = secondary.Mileage
	}
	if merged.Fuel == 0 && secondary.Fuel != 0 {
		merged.Fuel = secondary.Fuel
	}
	if merged.Direction == 0 && secondary.Direction != 0 {
		merged.Direction = secondary.Direction
	}
	if merged.AlarmFlag == 0 && secondary.AlarmFlag != 0 {
		merged.AlarmFlag = secondary.AlarmFlag
	}
	if merged.StatusFlag == 0 && secondary.StatusFlag != 0 {
		merged.StatusFlag = secondary.StatusFlag
	}

	return &merged, suppressedSource
}

// evictExpired 清理超过窗口的过期条目。
// 调用方必须持有写锁。
func (d *Deduplicator) evictExpired(now time.Time) {
	threshold := now.Add(-d.window * 10) // 保留 10 倍窗口作为安全余量
	for key, entry := range d.seen {
		if entry.ts.Before(threshold) {
			delete(d.seen, key)
		}
	}
}

// evictOldest 淘汰最旧的条目（容量超限时调用）。
// 调用方必须持有写锁。
func (d *Deduplicator) evictOldest() {
	var oldestKey string
	var oldestTS time.Time
	first := true
	for key, entry := range d.seen {
		if first || entry.ts.Before(oldestTS) {
			oldestKey = key
			oldestTS = entry.ts
			first = false
		}
	}
	if oldestKey != "" {
		delete(d.seen, oldestKey)
	}
}

// Stats 返回去重器统计信息。
type DedupStats struct {
	TrackedKeys int // 当前追踪的 key 数量
	WindowSecs  int // 去重窗口（秒）
}

func (d *Deduplicator) Stats() DedupStats {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return DedupStats{
		TrackedKeys: len(d.seen),
		WindowSecs:  int(d.window.Seconds()),
	}
}

// Reset 清空去重器状态。
func (d *Deduplicator) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = make(map[string]dedupEntry)
}
