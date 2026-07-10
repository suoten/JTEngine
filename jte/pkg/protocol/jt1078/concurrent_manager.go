package jt1078

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// 验收标准1: 4路并发播放稳定
// 并发播放管理器：管理多路视频流的生命周期，限制并发数，监控资源
// ===================================================================

// ConcurrentStreamInfo 并发流信息快照。
type ConcurrentStreamInfo struct {
	StreamID     string    `json:"stream_id"`
	Phone        string    `json:"phone"`
	LogicChannel byte      `json:"logic_channel"`
	StreamType   byte      `json:"stream_type"`
	StartTime    time.Time `json:"start_time"`
	LastActive   time.Time `json:"last_active"`
	Packets      uint64    `json:"packets"`
	Bytes        uint64    `json:"bytes"`
}

// ResourceStats 系统资源统计。
type ResourceStats struct {
	ActiveStreams   int     `json:"active_streams"`
	MaxConcurrent   int     `json:"max_concurrent"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemAllocMB      float64 `json:"mem_alloc_mb"`
	MemSysMB        float64 `json:"mem_sys_mb"`
	GoroutineCount  int     `json:"goroutine_count"`
	UptimeSeconds   float64 `json:"uptime_seconds"`
}

// ConcurrentPlayManager 并发播放管理器。
// 管理多路视频流并发播放，限制最大并发数（默认4路），监控资源使用。
type ConcurrentPlayManager struct {
	engine         *VideoEngine
	maxConcurrent  int
	mu             sync.RWMutex
	activeStreams  map[string]*streamEntry // key: streamID
	startTime      time.Time
	totalStarted   atomic.Int64
	totalStopped   atomic.Int64
	rejectedCount  atomic.Int64 // 因超限被拒绝的请求数
	logger         *zap.Logger
}

type streamEntry struct {
	info      ConcurrentStreamInfo
	startTime time.Time
}

// NewConcurrentPlayManager 创建并发播放管理器。
// maxConcurrent 为最大并发流数，<=0 时使用默认值 4。
func NewConcurrentPlayManager(engine *VideoEngine, maxConcurrent int, logger *zap.Logger) *ConcurrentPlayManager {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ConcurrentPlayManager{
		engine:        engine,
		maxConcurrent: maxConcurrent,
		activeStreams: make(map[string]*streamEntry),
		startTime:     time.Now(),
		logger:        logger,
	}
}

// CanStart 检查是否可以启动新流（不超并发上限）。
func (m *ConcurrentPlayManager) CanStart() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.activeStreams) < m.maxConcurrent
}

// RegisterStream 注册一路活跃流。
// 返回 error 表示超过并发上限或流已存在。
func (m *ConcurrentPlayManager) RegisterStream(streamID, phone string, logicChannel, streamType byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.activeStreams) >= m.maxConcurrent {
		m.rejectedCount.Add(1)
		m.logger.Warn("concurrent stream limit reached, rejecting new stream",
			zap.String("stream_id", streamID),
			zap.Int("active", len(m.activeStreams)),
			zap.Int("max", m.maxConcurrent))
		return fmt.Errorf("concurrent stream limit reached: active=%d max=%d", len(m.activeStreams), m.maxConcurrent)
	}

	if _, exists := m.activeStreams[streamID]; exists {
		return fmt.Errorf("stream already registered: %s", streamID)
	}

	now := time.Now()
	m.activeStreams[streamID] = &streamEntry{
		info: ConcurrentStreamInfo{
			StreamID:     streamID,
			Phone:        phone,
			LogicChannel: logicChannel,
			StreamType:   streamType,
			StartTime:    now,
			LastActive:   now,
		},
		startTime: now,
	}
	m.totalStarted.Add(1)

	m.logger.Info("stream registered for concurrent playback",
		zap.String("stream_id", streamID),
		zap.String("phone", phone),
		zap.Uint8("channel", logicChannel),
		zap.Int("active_streams", len(m.activeStreams)),
		zap.Int("max_concurrent", m.maxConcurrent))
	return nil
}

// UnregisterStream 注销一路流。
func (m *ConcurrentPlayManager) UnregisterStream(streamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.activeStreams[streamID]; exists {
		delete(m.activeStreams, streamID)
		m.totalStopped.Add(1)
		m.logger.Info("stream unregistered from concurrent playback",
			zap.String("stream_id", streamID),
			zap.Int("active_streams", len(m.activeStreams)))
	}
}

// UpdateStreamActive 更新流的最后活跃时间与统计（每收到 RTP 包时调用）。
func (m *ConcurrentPlayManager) UpdateStreamActive(streamID string, packets, bytes uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.activeStreams[streamID]
	if !ok {
		return
	}
	entry.info.LastActive = time.Now()
	entry.info.Packets = packets
	entry.info.Bytes = bytes
}

// ActiveCount 返回当前活跃流数量。
func (m *ConcurrentPlayManager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.activeStreams)
}

// GetResourceStats 返回系统资源统计快照。
func (m *ConcurrentPlayManager) GetResourceStats() ResourceStats {
	m.mu.RLock()
	activeCount := len(m.activeStreams)
	maxConc := m.maxConcurrent
	m.mu.RUnlock()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return ResourceStats{
		ActiveStreams:  activeCount,
		MaxConcurrent:  maxConc,
		MemAllocMB:     float64(memStats.Alloc) / 1024 / 1024,
		MemSysMB:       float64(memStats.Sys) / 1024 / 1024,
		GoroutineCount: runtime.NumGoroutine(),
		UptimeSeconds:  time.Since(m.startTime).Seconds(),
	}
}

// ListActiveStreams 返回所有活跃流的信息快照。
func (m *ConcurrentPlayManager) ListActiveStreams() []ConcurrentStreamInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ConcurrentStreamInfo, 0, len(m.activeStreams))
	for _, entry := range m.activeStreams {
		result = append(result, entry.info)
	}
	return result
}

// TotalStarted 返回累计启动流数。
func (m *ConcurrentPlayManager) TotalStarted() int64 {
	return m.totalStarted.Load()
}

// TotalStopped 返回累计停止流数。
func (m *ConcurrentPlayManager) TotalStopped() int64 {
	return m.totalStopped.Load()
}

// RejectedCount 返回因超限被拒绝的请求数。
func (m *ConcurrentPlayManager) RejectedCount() int64 {
	return m.rejectedCount.Load()
}

// CleanupStaleStreams 清理超时无活动的流（idle 超过 timeout）。
// 返回被清理的流数量。
func (m *ConcurrentPlayManager) CleanupStaleStreams(timeout time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	cleaned := 0
	for streamID, entry := range m.activeStreams {
		if now.Sub(entry.info.LastActive) > timeout {
			delete(m.activeStreams, streamID)
			m.totalStopped.Add(1)
			cleaned++
			m.logger.Info("stale stream cleaned up",
				zap.String("stream_id", streamID),
				zap.Duration("idle", now.Sub(entry.info.LastActive)))
		}
	}
	return cleaned
}

// SetMaxConcurrent 动态调整最大并发数。
// 如果新值小于当前活跃流数，不会强制断开已有流，但会拒绝新流直到活跃数降至新上限以下。
func (m *ConcurrentPlayManager) SetMaxConcurrent(max int) {
	if max <= 0 {
		return
	}
	m.mu.Lock()
	m.maxConcurrent = max
	m.mu.Unlock()
	m.logger.Info("max concurrent streams updated", zap.Int("new_max", max))
}

// GetMaxConcurrent 返回当前最大并发数。
func (m *ConcurrentPlayManager) GetMaxConcurrent() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maxConcurrent
}
