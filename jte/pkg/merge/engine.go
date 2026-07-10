package merge

import (
	"context"
	"sync"
	"time"

	"github.com/suoten/jt-engine/internal/registry"
	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

type Engine struct {
	storage             storage.Interface
	logger              *zap.Logger
	dedupWindow         time.Duration
	eventBus            *EventBus
	mu                  sync.RWMutex
	latestData          map[string]*storage.LocationData
	phoneToVehicle      map[string]string
	registry            *registry.FeatureRegistry
	cleanupInterval     time.Duration
	dataExpiry          time.Duration
	stopCleanup         chan struct{}
	stopOnce            sync.Once
	// AUTO-FIX-2026-06-26: 第二轮链路修复 - 批处理写入器（可选启用）
	locationBatchWriter *LocationBatchWriter
	alarmBatchWriter     *AlarmBatchWriter
}

func NewEngine(store storage.Interface, logger *zap.Logger, reg *registry.FeatureRegistry) *Engine {
	e := &Engine{
		storage:         store,
		logger:          logger,
		dedupWindow:     5 * time.Second,
		eventBus:        NewEventBus(logger),
		latestData:      make(map[string]*storage.LocationData),
		phoneToVehicle:  make(map[string]string),
		registry:        reg,
		cleanupInterval: 1 * time.Hour,
		dataExpiry:      24 * time.Hour,
		stopCleanup:     make(chan struct{}),
	}
	util.SafeGo(e.logger, "merge.engine.cleanupLoop", e.cleanupLoop)
	return e
}

func (e *Engine) cleanupLoop() {
	ticker := time.NewTicker(e.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.cleanupExpiredData()
		case <-e.stopCleanup:
			return
		}
	}
}

func (e *Engine) cleanupExpiredData() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for id, loc := range e.latestData {
		if now.Sub(loc.ReceivedAt) > e.dataExpiry {
			delete(e.latestData, id)
			e.logger.Debug("cleaned up expired vehicle data", zap.String("vehicle_id", id))
		}
	}
}

func (e *Engine) RemoveVehicle(vehicleID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.latestData, vehicleID)
}

func (e *Engine) OnVehicleOffline(vehicleID string) {
	e.logger.Debug("vehicle offline, data will be cleaned on next cycle", zap.String("vehicle_id", vehicleID))
}

func (e *Engine) SetCleanupConfig(interval, expiry time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanupInterval = interval
	e.dataExpiry = expiry
}

func (e *Engine) Stop() {
	e.stopOnce.Do(func() { close(e.stopCleanup) })
	// AUTO-FIX-2026-06-26: 第二轮链路修复 - 停止时刷新批处理缓冲区，避免数据丢失
	if e.locationBatchWriter != nil {
		e.locationBatchWriter.Stop()
	}
	if e.alarmBatchWriter != nil {
		e.alarmBatchWriter.Stop()
	}
}

func (e *Engine) Merge(ctx context.Context, loc *storage.LocationData) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	vehicleID := loc.VehicleID

	if loc.Phone != "" {
		if existingVID, ok := e.phoneToVehicle[loc.Phone]; ok && existingVID != vehicleID {
			e.logger.Debug("cross-protocol vehicle association",
				zap.String("phone", loc.Phone),
				zap.String("existing_vid", existingVID),
				zap.String("new_vid", vehicleID),
				zap.String("source", loc.Source))

			if existing, hasExisting := e.latestData[existingVID]; hasExisting {
				latDiff := existing.Latitude - loc.Latitude
				lonDiff := existing.Longitude - loc.Longitude
				if latDiff < 0 {
					latDiff = -latDiff
				}
				if lonDiff < 0 {
					lonDiff = -lonDiff
				}
				if latDiff > 0.01 || lonDiff > 0.01 {
					e.logger.Warn("cross-protocol position conflict",
						zap.String("phone", loc.Phone),
						zap.Float64("lat_diff", latDiff),
						zap.Float64("lon_diff", lonDiff))
				}

				if loc.ReceivedAt.After(existing.ReceivedAt) {
					loc.VehicleID = existingVID
					vehicleID = existingVID
				}
			} else {
				e.phoneToVehicle[loc.Phone] = vehicleID
			}
		} else if !ok {
			e.phoneToVehicle[loc.Phone] = vehicleID
		}
	}

	existing, hasExisting := e.latestData[vehicleID]

	if hasExisting && e.isDuplicate(existing, loc) {
		e.supplement(existing, loc)
		e.logger.Debug("location deduplicated",
			zap.String("vehicle_id", vehicleID),
			zap.String("source", loc.Source))
		return nil
	}

	if hasExisting {
		e.resolveConflict(existing, loc)
	}

	// AUTO-FIX-2026-06-26: 第二轮链路修复 - 启用批处理时走批量写入，否则单条写入
	if e.locationBatchWriter != nil {
		e.locationBatchWriter.Add(loc)
	} else {
		if err := e.storage.SaveLocation(ctx, loc); err != nil {
			return err
		}
	}

	e.latestData[vehicleID] = loc

	e.eventBus.Publish(EventTypeLocationUpdate, loc)

	return nil
}

func (e *Engine) isDuplicate(existing, incoming *storage.LocationData) bool {
	if existing.VehicleID != incoming.VehicleID {
		return false
	}

	timeDiff := incoming.ReceivedAt.Sub(existing.ReceivedAt)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}

	if timeDiff > e.dedupWindow {
		return false
	}

	latDiff := existing.Latitude - incoming.Latitude
	lonDiff := existing.Longitude - incoming.Longitude
	if latDiff < 0 {
		latDiff = -latDiff
	}
	if lonDiff < 0 {
		lonDiff = -lonDiff
	}

	return latDiff < 0.00001 && lonDiff < 0.00001
}

func (e *Engine) resolveConflict(existing, incoming *storage.LocationData) {
	if incoming.Source == "jt808" {
		incoming.Latitude = resolveField(existing.Latitude, incoming.Latitude, existing.Source, incoming.Source, "jt808")
		incoming.Longitude = resolveField(existing.Longitude, incoming.Longitude, existing.Source, incoming.Source, "jt808")
		incoming.Speed = resolveFieldFloat(existing.Speed, incoming.Speed, existing.Source, incoming.Source, "jt808")
	}
}

func (e *Engine) supplement(existing, incoming *storage.LocationData) {
	if existing.Altitude == 0 && incoming.Altitude != 0 {
		existing.Altitude = incoming.Altitude
	}
	if existing.Mileage == 0 && incoming.Mileage != 0 {
		existing.Mileage = incoming.Mileage
	}
	if existing.Fuel == 0 && incoming.Fuel != 0 {
		existing.Fuel = incoming.Fuel
	}
}

func resolveField(existingVal, incomingVal float64, existingSource, incomingSource, preferred string) float64 {
	if incomingSource == preferred {
		return incomingVal
	}
	if existingVal == 0 {
		return incomingVal
	}
	return existingVal
}

func resolveFieldFloat(existingVal, incomingVal float64, existingSource, incomingSource, preferred string) float64 {
	if incomingSource == preferred {
		return incomingVal
	}
	if existingVal == 0 {
		return incomingVal
	}
	return existingVal
}

func (e *Engine) GetLatestLocation(vehicleID string) (*storage.LocationData, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	loc, ok := e.latestData[vehicleID]
	return loc, ok
}

func (e *Engine) GetEventBus() *EventBus {
	return e.eventBus
}

func (e *Engine) SetDedupWindow(d time.Duration) {
	e.dedupWindow = d
}

// MergeAlarm 处理报警数据：启用批处理时走 AlarmBatchWriter 批量写入，
// 否则单条 SaveAlarm 写入。同时发布 EventTypeAlarmEvent 事件到 EventBus。
// 修复：此前 handler.go 直接调用 store.SaveAlarm 绕过 merge.Engine，
// 导致 AlarmBatchWriter 成为死代码。此方法统一报警写入路径。
func (e *Engine) MergeAlarm(ctx context.Context, alarm *storage.AlarmData) error {
	if e.alarmBatchWriter != nil {
		e.alarmBatchWriter.Add(alarm)
	} else {
		if err := e.storage.SaveAlarm(ctx, alarm); err != nil {
			return err
		}
	}
	e.eventBus.Publish(EventTypeAlarmEvent, alarm)
	return nil
}

// AUTO-FIX-2026-06-26: 第二轮链路修复 - 启用存储批量写入
// 启用后，位置数据将聚合到 batchSize 条或 flushTimeout 超时后批量写入，
// 替代默认的单条 SaveLocation 写入，大幅提升高并发场景的写入吞吐。
func (e *Engine) EnableBatchWriters(batchSize int, flushTimeout time.Duration) {
	if e.locationBatchWriter != nil {
		return // 已启用，避免重复创建
	}
	e.locationBatchWriter = NewLocationBatchWriter(e.storage, e.logger, batchSize, flushTimeout)
	e.alarmBatchWriter = NewAlarmBatchWriter(e.storage, e.logger, batchSize, flushTimeout)
	e.logger.Info("batch writers enabled",
		zap.Int("batch_size", batchSize),
		zap.Duration("flush_timeout", flushTimeout))
}

// IsBatchWriterEnabled 返回批处理写入器是否启用。
func (e *Engine) IsBatchWriterEnabled() bool {
	return e.locationBatchWriter != nil
}