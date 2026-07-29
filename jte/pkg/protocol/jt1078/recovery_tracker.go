package jt1078

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// 验收标准3: 关键帧恢复
// 追踪 0x9203 关键帧请求下发后的恢复时间
// 验收标准6: 云台控制延迟测量
// 追踪 0x9301 PTZ 控制指令的端到端延迟
// ===================================================================

// KeyFrameRecoveryTracker 关键帧恢复计时器。
// 记录每次关键帧请求的时间戳，当检测到 I 帧到达时计算恢复耗时。
type KeyFrameRecoveryTracker struct {
	mu       sync.Mutex
	pending  map[string]*keyFrameRequest // key: streamID
	history  []KeyFrameRecoveryResult
	logger   *zap.Logger
}

type keyFrameRequest struct {
	streamID  string
	phone     string
	channel   byte
	sentAt    time.Time
}

// KeyFrameRecoveryResult 关键帧恢复结果。
type KeyFrameRecoveryResult struct {
	StreamID     string        `json:"stream_id"`
	Phone        string        `json:"phone"`
	LogicChannel byte          `json:"logic_channel"`
	SentAt       time.Time     `json:"sent_at"`
	RecoveredAt  time.Time     `json:"recovered_at"`
	RecoveryTime time.Duration `json:"recovery_time_ms"` // 毫秒
	Success      bool          `json:"success"`
}

// NewKeyFrameRecoveryTracker 创建关键帧恢复计时器。
func NewKeyFrameRecoveryTracker(logger *zap.Logger) *KeyFrameRecoveryTracker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &KeyFrameRecoveryTracker{
		pending: make(map[string]*keyFrameRequest),
		logger:  logger,
	}
}

// RecordRequest 记录一次 0x9203 关键帧请求下发。
// 如果该流已有未完成的请求，覆盖旧请求（以最新为准）。
func (t *KeyFrameRecoveryTracker) RecordRequest(streamID, phone string, channel byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[streamID] = &keyFrameRequest{
		streamID: streamID,
		phone:    phone,
		channel:  channel,
		sentAt:   time.Now(),
	}
	t.logger.Info("keyframe request recorded, waiting for I-frame",
		zap.String("stream_id", streamID),
		zap.String("phone", phone),
		zap.Uint8("channel", channel))
}

// RecordIFrame 检测到 I 帧到达时调用，计算恢复耗时。
// 返回恢复结果和是否匹配到待处理的请求。
func (t *KeyFrameRecoveryTracker) RecordIFrame(streamID string) (KeyFrameRecoveryResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	req, ok := t.pending[streamID]
	if !ok {
		return KeyFrameRecoveryResult{}, false
	}

	now := time.Now()
	result := KeyFrameRecoveryResult{
		StreamID:     streamID,
		Phone:        req.phone,
		LogicChannel: req.channel,
		SentAt:       req.sentAt,
		RecoveredAt:  now,
		RecoveryTime: now.Sub(req.sentAt),
		Success:      true,
	}

	// 保留最近 100 条历史
	t.history = append(t.history, result)
	if len(t.history) > 100 {
		t.history = t.history[len(t.history)-100:]
	}

	delete(t.pending, streamID)

	t.logger.Info("keyframe recovery completed",
		zap.String("stream_id", streamID),
		zap.Duration("recovery_time", result.RecoveryTime),
		zap.Bool("within_5s", result.RecoveryTime < 5*time.Second))

	return result, true
}

// GetPendingRequest 返回指定流的待处理关键帧请求。
func (t *KeyFrameRecoveryTracker) GetPendingRequest(streamID string) (KeyFrameRecoveryResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	req, ok := t.pending[streamID]
	if !ok {
		return KeyFrameRecoveryResult{}, false
	}
	return KeyFrameRecoveryResult{
		StreamID:     streamID,
		Phone:        req.phone,
		LogicChannel: req.channel,
		SentAt:       req.sentAt,
	}, true
}

// GetHistory 返回历史恢复记录（最近 100 条）。
func (t *KeyFrameRecoveryTracker) GetHistory() []KeyFrameRecoveryResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]KeyFrameRecoveryResult, len(t.history))
	copy(result, t.history)
	return result
}

// CheckTimeout 检查超时未恢复的请求，标记为失败。
// timeout 为超时阈值（默认 5 秒）。
func (t *KeyFrameRecoveryTracker) CheckTimeout(timeout time.Duration) []KeyFrameRecoveryResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	var timedOut []KeyFrameRecoveryResult
	for streamID, req := range t.pending {
		if now.Sub(req.sentAt) >= timeout {
			result := KeyFrameRecoveryResult{
				StreamID:     streamID,
				Phone:        req.phone,
				LogicChannel: req.channel,
				SentAt:       req.sentAt,
				RecoveredAt:  now,
				RecoveryTime: now.Sub(req.sentAt),
				Success:      false,
			}
			timedOut = append(timedOut, result)
			t.history = append(t.history, result)
			if len(t.history) > 100 {
				t.history = t.history[len(t.history)-100:]
			}
			delete(t.pending, streamID)

			t.logger.Warn("keyframe recovery timeout",
				zap.String("stream_id", streamID),
				zap.Duration("elapsed", result.RecoveryTime))
		}
	}
	return timedOut
}

// ===================================================================
// 验收标准6: PTZ 控制延迟测量
// ===================================================================

// PTZLatencyTracker PTZ 控制延迟追踪器。
// 记录每次 PTZ 指令下发的时间戳，当收到终端 0x9302 应答时计算端到端延迟。
type PTZLatencyTracker struct {
	mu      sync.Mutex
	pending map[uint16]*ptzRequest // key: seqNum
	history []PTZLatencyResult
	logger  *zap.Logger
}

type ptzRequest struct {
	streamID  string
	phone     string
	channel   byte
	direction int
	speed     int
	sentAt    time.Time
}

// PTZLatencyResult PTZ 控制延迟结果。
type PTZLatencyResult struct {
	StreamID     string        `json:"stream_id"`
	Phone        string        `json:"phone"`
	LogicChannel byte          `json:"logic_channel"`
	Direction    int           `json:"direction"`
	Speed        int           `json:"speed"`
	SentAt       time.Time     `json:"sent_at"`
	AckAt        time.Time     `json:"ack_at"`
	Latency      time.Duration `json:"latency_ms"` // 毫秒
	Within2s     bool          `json:"within_2s"`
}

// NewPTZLatencyTracker 创建 PTZ 延迟追踪器。
func NewPTZLatencyTracker(logger *zap.Logger) *PTZLatencyTracker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PTZLatencyTracker{
		pending: make(map[uint16]*ptzRequest),
		logger:  logger,
	}
}

// RecordPTZSent 记录 PTZ 指令下发。
func (t *PTZLatencyTracker) RecordPTZSent(seqNum uint16, streamID, phone string, channel byte, direction, speed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[seqNum] = &ptzRequest{
		streamID:  streamID,
		phone:     phone,
		channel:   channel,
		direction: direction,
		speed:     speed,
		sentAt:    time.Now(),
	}
}

// RecordPTZAck 记录 PTZ 应答到达，计算延迟。
// seqNum 为 0x9302 应答中的流水号。
func (t *PTZLatencyTracker) RecordPTZAck(seqNum uint16) (PTZLatencyResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	req, ok := t.pending[seqNum]
	if !ok {
		return PTZLatencyResult{}, false
	}

	now := time.Now()
	latency := now.Sub(req.sentAt)
	result := PTZLatencyResult{
		StreamID:     req.streamID,
		Phone:        req.phone,
		LogicChannel: req.channel,
		Direction:    req.direction,
		Speed:        req.speed,
		SentAt:       req.sentAt,
		AckAt:        now,
		Latency:      latency,
		Within2s:     latency < 2*time.Second,
	}

	t.history = append(t.history, result)
	if len(t.history) > 100 {
		t.history = t.history[len(t.history)-100:]
	}
	delete(t.pending, seqNum)

	t.logger.Info("ptz latency measured",
		zap.String("stream_id", req.streamID),
		zap.Duration("latency", latency),
		zap.Bool("within_2s", result.Within2s))

	return result, true
}

// GetPTZHistory 返回历史 PTZ 延迟记录。
func (t *PTZLatencyTracker) GetPTZHistory() []PTZLatencyResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]PTZLatencyResult, len(t.history))
	copy(result, t.history)
	return result
}

// GetAverageLatency 返回平均 PTZ 延迟（毫秒）。
func (t *PTZLatencyTracker) GetAverageLatency() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.history) == 0 {
		return 0
	}
	var total time.Duration
	for _, r := range t.history {
		total += r.Latency
	}
	return float64(total.Milliseconds()) / float64(len(t.history))
}

// CheckPTZTimeout 检查超时未应答的 PTZ 请求，标记为失败并从 pending 移除。
// INDUSTRIAL-FIX-2026-07-25-R31 [P2]: PTZLatencyTracker 缺少超时清理机制，
// 设备离线或网络异常时 pending 条目永久残留，造成内存泄漏。
// 与 KeyFrameRecoveryTracker.CheckTimeout 保持一致的清理策略。
// timeout 为超时阈值（建议 10 秒，PTZ 应答通常 < 2s）。
func (t *PTZLatencyTracker) CheckPTZTimeout(timeout time.Duration) []PTZLatencyResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	var timedOut []PTZLatencyResult
	for seqNum, req := range t.pending {
		if now.Sub(req.sentAt) >= timeout {
			result := PTZLatencyResult{
				StreamID:     req.streamID,
				Phone:        req.phone,
				LogicChannel: req.channel,
				Direction:    req.direction,
				Speed:        req.speed,
				SentAt:       req.sentAt,
				AckAt:        now,
				Latency:      now.Sub(req.sentAt),
				Within2s:     false,
			}
			timedOut = append(timedOut, result)
			t.history = append(t.history, result)
			if len(t.history) > 100 {
				t.history = t.history[len(t.history)-100:]
			}
			delete(t.pending, seqNum)

			t.logger.Warn("ptz latency timeout, no ack received",
				zap.String("stream_id", req.streamID),
				zap.Uint16("seq_num", seqNum),
				zap.Duration("elapsed", result.Latency))
		}
	}
	return timedOut
}

// ===================================================================
// 验收标准4: 弱网自适应 - 自动恢复
// 当网络恢复后（连续3秒丢包率<2%且码率>200kbps），自动切回主码流
// ===================================================================

// AutoRecoveryTracker 弱网自动恢复追踪器。
// 追踪子码流状态，当网络质量恢复时自动切回主码流。
type AutoRecoveryTracker struct {
	mu               sync.Mutex
	subStreamStreams map[string]*recoveryState // key: streamID
	logger           *zap.Logger
}

type recoveryState struct {
	phone        string
	channel      byte
	switchedAt   time.Time
	goodCount    int // 连续良好窗口数
	switchReason string
}

// NewAutoRecoveryTracker 创建自动恢复追踪器。
func NewAutoRecoveryTracker(logger *zap.Logger) *AutoRecoveryTracker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AutoRecoveryTracker{
		subStreamStreams: make(map[string]*recoveryState),
		logger:           logger,
	}
}

// OnSwitchToSub 记录流切换到子码流。
func (t *AutoRecoveryTracker) OnSwitchToSub(streamID, phone string, channel byte, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subStreamStreams[streamID] = &recoveryState{
		phone:        phone,
		channel:      channel,
		switchedAt:   time.Now(),
		switchReason: reason,
	}
	t.logger.Info("stream switched to sub, monitoring for recovery",
		zap.String("stream_id", streamID),
		zap.String("reason", reason))
}

// CheckRecovery 检查是否应自动切回主码流。
// 当连续3个窗口（3秒）丢包率<2%且码率>200kbps时返回 true。
// 参数为当前流的质量统计。
func (t *AutoRecoveryTracker) CheckRecovery(streamID string, lossRate, bitrateKbps float64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	state, ok := t.subStreamStreams[streamID]
	if !ok {
		return false // 不在子码流状态，无需恢复
	}

	goodLoss := lossRate < 2.0
	goodBitrate := bitrateKbps > 200.0

	if goodLoss && goodBitrate {
		state.goodCount++
	} else {
		state.goodCount = 0
	}

	// 连续3个良好窗口触发恢复
	if state.goodCount >= 3 {
		delete(t.subStreamStreams, streamID)
		t.logger.Info("network recovered, auto switching back to main stream",
			zap.String("stream_id", streamID),
			zap.Float64("loss_rate", lossRate),
			zap.Float64("bitrate_kbps", bitrateKbps),
			zap.Duration("sub_duration", time.Since(state.switchedAt)))
		return true
	}
	return false
}

// OnSwitchToMain 记录流切回主码流（手动或自动）。
func (t *AutoRecoveryTracker) OnSwitchToMain(streamID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subStreamStreams, streamID)
}

// IsOnSubStream 检查流是否在子码流状态。
func (t *AutoRecoveryTracker) IsOnSubStream(streamID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.subStreamStreams[streamID]
	return ok
}

// ListSubStreams 返回当前在子码流的所有流。
func (t *AutoRecoveryTracker) ListSubStreams() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]string, 0, len(t.subStreamStreams))
	for streamID := range t.subStreamStreams {
		result = append(result, streamID)
	}
	return result
}
