package jt1078

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// P2-7: 录制断片防护
// ===================================================================
//
// 设计目标：
//   1. 播放侧切换码流（主→子）无感：用户观看不受影响
//   2. 录制侧始终录制主码流：即使播放切到子码流，录制仍走主码流
//   3. 分片标记：每个录制分片记录 switch_reason / switch_time
//   4. 断片检测：相邻分片时间戳间隔 > 5s 标记为断片
//   5. 断片写入 alert 表：供运维排查录制中断原因
//
// 集成点：
//   - qualityMonitorLoop 检测流断开/质量差时调用 RecordSegmentTracker.OnStreamSwitch
//   - VideoEngine 收到 RTP 包时调用 OnPacket 更新最后活跃时间
//   - 录制分片由 ZLMediaKit 生成，本跟踪器记录分片元数据与断片告警

// SwitchReason 码流切换/中断原因。
type SwitchReason string

const (
	SwitchReasonQualityPoor  SwitchReason = "quality_poor"  // 质量差切子码流
	SwitchReasonStreamDown   SwitchReason = "stream_down"   // 流断开
	SwitchReasonReconnect    SwitchReason = "reconnect"     // 重连恢复
	SwitchReasonManual       SwitchReason = "manual"        // 手动切换
	SwitchReasonShutdown     SwitchReason = "shutdown"      // 优雅停机
	SwitchReasonSRTPFail     SwitchReason = "srtp_decrypt"  // SRTP 解密失败
	SwitchReasonStreamStart  SwitchReason = "stream_start"  // 流开始
	SwitchReasonStreamEnd    SwitchReason = "stream_end"    // 流正常结束
)

// FragmentGapThreshold 断片判定阈值：相邻分片时间戳间隔超过此值标记为断片。
const FragmentGapThreshold = 5 * time.Second

// RecordSegment 录制分片元数据。
// AUTO-FIX-2026-07-02 [P1]: 增加 FilePath/FileSize 字段，支持按时间段查询与合并。
type RecordSegment struct {
	StreamID     string       `json:"stream_id"`
	Phone        string       `json:"phone"`
	LogicChannel byte         `json:"logic_channel"`
	StreamType   byte         `json:"stream_type"` // 0=主码流 1=子码流（录制始终为主码流 0）
	SegmentIndex int          `json:"segment_index"`
	StartTime    time.Time    `json:"start_time"`
	EndTime      time.Time    `json:"end_time"`
	SwitchReason SwitchReason `json:"switch_reason"` // 本分片结束原因
	SwitchTime   time.Time    `json:"switch_time"`   // 切换发生时间
	PacketCount  uint64       `json:"packet_count"`
	ByteCount    uint64       `json:"byte_count"`
	IsFragment   bool         `json:"is_fragment"` // 与上一分片间隔>5s
	FilePath     string       `json:"file_path"`   // S3/对象存储对象 key
	FileSize     int64        `json:"file_size"`   // 文件大小（字节）
}

// FragmentAlert 断片告警（写入 alert 表）。
type FragmentAlert struct {
	Phone        string    `json:"phone"`
	LogicChannel byte      `json:"logic_channel"`
	StreamID     string    `json:"stream_id"`
	GapStart     time.Time `json:"gap_start"`     // 上一分片结束时间
	GapEnd       time.Time `json:"gap_end"`       // 当前分片开始时间
	GapDuration  float64   `json:"gap_duration_s"` // 断片时长（秒）
	PrevReason   SwitchReason `json:"prev_reason"` // 上一分片结束原因
	AlertTime    time.Time `json:"alert_time"`
}

// FragmentAlertWriter 断片告警写入接口（由存储层实现，解耦 jt1078 与存储）。
type FragmentAlertWriter interface {
	WriteFragmentAlert(alert FragmentAlert) error
}

// RecordSegmentTracker 录制分片跟踪器（每设备每通道一个）。
// AUTO-FIX-2026-06-30 [P2-7]: 跟踪录制分片，检测断片，写入告警。
// AUTO-FIX-2026-07-02 [P1]: 增加 history 历史分片列表，支持按时间段查询与合并。
// R47-FIX-2026-07-26 [P1]: 增加 maxHistorySize 限制，防止 7x24 运行时 history 切片无限增长导致 OOM。
type RecordSegmentTracker struct {
	mu             sync.Mutex
	segments       map[string]*activeSegment // key: streamID（phone+channel+streamType）
	history        []RecordSegment           // 已结束的分片历史（按时间顺序，供查询/合并）
	maxHistorySize int                       // history 最大条目数，0 表示不限
	alertWriter    FragmentAlertWriter
	logger         *zap.Logger
}

type activeSegment struct {
	streamID     string
	phone        string
	logicChannel byte
	streamType   byte // 录制固定为主码流 0
	index        int
	startTime    time.Time
	lastActive   time.Time
	packets      uint64
	bytes        uint64
	prevEndTime  time.Time    // 上一分片结束时间（用于断片检测）
	prevReason   SwitchReason // 上一分片结束原因
	lastReason   SwitchReason // 本分片最近一次切换原因（用于 finalize）
	finished     bool         // 是否已落入 history（防止重复 finalize）
	isFragment   bool         // 本分片开始前是否存在断片（间隔>5s）
	filePath     string       // 录制文件路径（S3 对象 key）
	fileSize     int64        // 录制文件大小（字节）
}

// NewRecordSegmentTracker 创建录制分片跟踪器。
// maxHistorySizeDefault 默认 history 最大条目数（10000 条）
const maxHistorySizeDefault = 10000

func NewRecordSegmentTracker(logger *zap.Logger, alertWriter FragmentAlertWriter) *RecordSegmentTracker {
	return &RecordSegmentTracker{
		segments:       make(map[string]*activeSegment),
		alertWriter:    alertWriter,
		logger:         logger,
		maxHistorySize: maxHistorySizeDefault,
	}
}

// SetAlertWriter 延迟注入告警写入器（存储层初始化后调用）。
func (t *RecordSegmentTracker) SetAlertWriter(w FragmentAlertWriter) {
	t.mu.Lock()
	t.alertWriter = w
	t.mu.Unlock()
}

// SetMaxHistorySize 设置 history 最大条目数（0=不限）。
// R47-FIX-2026-07-26 [P1]: 防止 7x24 运行时 history 无限增长导致 OOM。
func (t *RecordSegmentTracker) SetMaxHistorySize(max int) {
	t.mu.Lock()
	t.maxHistorySize = max
	if max > 0 && len(t.history) > max {
		t.history = t.history[len(t.history)-max:]
	}
	t.mu.Unlock()
}

// OnStreamStart 流开始：创建新分片，检测与上一分片的间隔是否为断片。
// AUTO-FIX-2026-07-02 [P1]: 同时将上一分片落入 history（如果尚未落入），支持按时间段查询。
// IsFragment 语义：标记在本分片开始前是否存在断片（与上一分片间隔>5s）。
func (t *RecordSegmentTracker) OnStreamStart(streamID, phone string, logicChannel, streamType byte, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := streamKey(phone, logicChannel)
	prev, exists := t.segments[key]

	seg := &activeSegment{
		streamID:     streamID,
		phone:        phone,
		logicChannel: logicChannel,
		streamType:   0, // 录制始终为主码流
		startTime:    now,
		lastActive:   now,
		lastReason:   SwitchReasonStreamStart,
	}
	if exists {
		seg.index = prev.index + 1
		seg.prevEndTime = prev.endTime()
		seg.prevReason = prev.lastSwitchReason()

		// 断片检测：与上一分片间隔 > 5s（使用 prev 原始 endTime，在 finalize 之前）
		gap := now.Sub(seg.prevEndTime)
		seg.isFragment = gap > FragmentGapThreshold
		if seg.isFragment {
			t.emitFragmentAlert(prev, seg, gap, now)
		}

		// 将上一分片落入 history（仅一次）—— prev 的 IsFragment 由 prev 自身决定
		t.finalizeLocked(prev, seg.prevEndTime, prev.lastSwitchReason())
	}
	t.segments[key] = seg
}

// OnPacket 收到 RTP 包：更新当前分片的活跃统计。
func (t *RecordSegmentTracker) OnPacket(streamID, phone string, logicChannel byte, packetBytes int, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := streamKey(phone, logicChannel)
	seg, ok := t.segments[key]
	if !ok {
		// 首次收包自动创建分片
		seg = &activeSegment{
			streamID:     streamID,
			phone:        phone,
			logicChannel: logicChannel,
			streamType:   0,
			index:        0,
			startTime:    now,
			lastActive:   now,
			lastReason:   SwitchReasonStreamStart,
		}
		t.segments[key] = seg
	}
	seg.lastActive = now
	seg.packets++
	seg.bytes += uint64(packetBytes)
}

// OnStreamSwitch 码流切换/中断：结束当前分片并记录切换原因。
// 录制侧不受播放侧码流切换影响——仅当主码流本身断开时才结束录制分片。
// AUTO-FIX-2026-07-02 [P1]: 同时记录 lastReason，供后续 finalize 使用。
func (t *RecordSegmentTracker) OnStreamSwitch(streamID, phone string, logicChannel byte, reason SwitchReason, now time.Time) RecordSegment {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := streamKey(phone, logicChannel)
	seg, ok := t.segments[key]
	if !ok {
		return RecordSegment{}
	}

	finished := RecordSegment{
		StreamID:     seg.streamID,
		Phone:        seg.phone,
		LogicChannel: seg.logicChannel,
		StreamType:   seg.streamType,
		SegmentIndex: seg.index,
		StartTime:    seg.startTime,
		EndTime:      now,
		SwitchReason: reason,
		SwitchTime:   now,
		PacketCount:  seg.packets,
		ByteCount:    seg.bytes,
		FilePath:     seg.filePath,
		FileSize:     seg.fileSize,
	}

	// 保留 endTime / reason 供下一次 OnStreamStart 做断片检测
	seg.lastActive = now
	seg.lastReason = reason

	return finished
}

// OnStreamEnd 流正常结束：关闭分片并落入 history。
// AUTO-FIX-2026-07-02 [P1]: 流结束时立即将分片落入 history，支持按时间段查询。
func (t *RecordSegmentTracker) OnStreamEnd(streamID, phone string, logicChannel byte, now time.Time) RecordSegment {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := streamKey(phone, logicChannel)
	seg, ok := t.segments[key]
	if !ok {
		return RecordSegment{}
	}

	finished := RecordSegment{
		StreamID:     seg.streamID,
		Phone:        seg.phone,
		LogicChannel: seg.logicChannel,
		StreamType:   seg.streamType,
		SegmentIndex: seg.index,
		StartTime:    seg.startTime,
		EndTime:      now,
		SwitchReason: SwitchReasonStreamEnd,
		SwitchTime:   now,
		PacketCount:  seg.packets,
		ByteCount:    seg.bytes,
		FilePath:     seg.filePath,
		FileSize:     seg.fileSize,
	}

	seg.lastActive = now
	seg.lastReason = SwitchReasonStreamEnd
	// 流结束：立即落入 history（IsFragment 由 seg.isFragment 决定）
	t.finalizeLocked(seg, now, SwitchReasonStreamEnd)

	return finished
}

// emitFragmentAlert 生成断片告警并写入 alert 表。
func (t *RecordSegmentTracker) emitFragmentAlert(prev *activeSegment, cur *activeSegment, gap time.Duration, now time.Time) {
	alert := FragmentAlert{
		Phone:        prev.phone,
		LogicChannel: prev.logicChannel,
		StreamID:     prev.streamID,
		GapStart:     prev.endTime(),
		GapEnd:       cur.startTime,
		GapDuration:  gap.Seconds(),
		PrevReason:   prev.lastSwitchReason(),
		AlertTime:    now,
	}

	t.logger.Warn("recording fragment detected",
		zap.String("phone", alert.Phone),
		zap.Uint8("channel", alert.LogicChannel),
		zap.String("stream_id", alert.StreamID),
		zap.Float64("gap_seconds", alert.GapDuration),
		zap.String("prev_reason", string(alert.PrevReason)),
		zap.Time("gap_start", alert.GapStart),
		zap.Time("gap_end", alert.GapEnd))

	if t.alertWriter != nil {
		// best-effort 写入，失败仅记录日志不阻塞录制
		if err := t.alertWriter.WriteFragmentAlert(alert); err != nil {
			t.logger.Error("write fragment alert failed",
				zap.String("phone", alert.Phone),
				zap.Error(err))
		}
	}
}

// GetActiveSegment 返回指定流的当前活跃分片快照（只读）。
// AUTO-FIX-2026-07-02 [P1]: 返回 FilePath/FileSize 字段。
func (t *RecordSegmentTracker) GetActiveSegment(phone string, logicChannel byte) (RecordSegment, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := streamKey(phone, logicChannel)
	seg, ok := t.segments[key]
	if !ok {
		return RecordSegment{}, false
	}
	return RecordSegment{
		StreamID:     seg.streamID,
		Phone:        seg.phone,
		LogicChannel: seg.logicChannel,
		StreamType:   seg.streamType,
		SegmentIndex: seg.index,
		StartTime:    seg.startTime,
		EndTime:      seg.lastActive,
		PacketCount:  seg.packets,
		ByteCount:    seg.bytes,
		FilePath:     seg.filePath,
		FileSize:     seg.fileSize,
	}, true
}

// SetSegmentFilePath 设置当前活跃分片的文件路径与大小（ZLMediaKit 录制完成后回调注入）。
// AUTO-FIX-2026-07-02 [P1]: 断片元数据记录 FilePath/Size，供查询与合并。
func (t *RecordSegmentTracker) SetSegmentFilePath(phone string, logicChannel byte, filePath string, fileSize int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := streamKey(phone, logicChannel)
	if seg, ok := t.segments[key]; ok {
		seg.filePath = filePath
		seg.fileSize = fileSize
	}
}

// finalizeLocked 将活跃分片落入 history（仅一次）。调用方需持锁。
// AUTO-FIX-2026-07-02 [P1]: 支持按时间段查询断片列表。
// IsFragment 由 seg.isFragment 决定（标记本分片开始前是否存在断片）。
//
// 参数：
//   - seg: 活跃分片
//   - endTime: 分片结束时间
//   - reason: 分片结束原因
func (t *RecordSegmentTracker) finalizeLocked(seg *activeSegment, endTime time.Time, reason SwitchReason) {
	if seg.finished {
		return
	}
	seg.finished = true
	t.history = append(t.history, RecordSegment{
		StreamID:     seg.streamID,
		Phone:        seg.phone,
		LogicChannel: seg.logicChannel,
		StreamType:   seg.streamType,
		SegmentIndex: seg.index,
		StartTime:    seg.startTime,
		EndTime:      endTime,
		SwitchReason: reason,
		SwitchTime:   endTime,
		PacketCount:  seg.packets,
		ByteCount:    seg.bytes,
		IsFragment:   seg.isFragment,
		FilePath:     seg.filePath,
		FileSize:     seg.fileSize,
	})
	// R47-FIX-2026-07-26 [P1]: 裁剪 history，防止无限增长导致 OOM
	if t.maxHistorySize > 0 && len(t.history) > t.maxHistorySize {
		t.history = t.history[len(t.history)-t.maxHistorySize:]
	}
}

// QuerySegments 按时间段查询已结束的录制分片列表。
// AUTO-FIX-2026-07-02 [P1]: 支持按时间段查询断片列表。
//
// 参数：
//   - phone: 设备手机号（空串表示不过滤）
//   - logicChannel: 逻辑通道号（0 表示不过滤——注意：通道 0 不会被当作过滤条件）
//   - start/end: 查询时间范围 [start, end]，分片与该区间有重叠即返回
//
// 返回按 StartTime 升序排列的分片列表（副本）。
func (t *RecordSegmentTracker) QuerySegments(phone string, logicChannel byte, start, end time.Time) []RecordSegment {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make([]RecordSegment, 0, len(t.history))
	for _, seg := range t.history {
		if phone != "" && seg.Phone != phone {
			continue
		}
		if logicChannel != 0 && seg.LogicChannel != logicChannel {
			continue
		}
		// 分片与查询区间有重叠：seg.EndTime >= start && seg.StartTime <= end
		if seg.EndTime.Before(start) || seg.StartTime.After(end) {
			continue
		}
		result = append(result, seg)
	}
	// 按 StartTime 升序排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartTime.Before(result[j].StartTime)
	})
	return result
}

// MergeSegments 合并相邻的录制分片为一个连续分片。
// AUTO-FIX-2026-07-02 [P1]: 断片合并接口。
//
// 要求：
//   - 分片属于同一 phone + logicChannel
//   - 按时间顺序自动排序后合并
//
// 合并规则：
//   - StartTime = 最早分片的 StartTime
//   - EndTime = 最晚分片的 EndTime
//   - PacketCount/ByteCount/FileSize 求和
//   - FilePath 为各分片路径用逗号拼接（实际文件合并需外部执行，如 FFmpeg concat）
//   - IsFragment = true（合并结果标记为已修复断片）
//
// 返回合并后的分片。空列表返回错误。
func MergeSegments(segments []RecordSegment) (RecordSegment, error) {
	if len(segments) == 0 {
		return RecordSegment{}, errors.New("merge: no segments to merge")
	}
	if len(segments) == 1 {
		return segments[0], nil
	}

	// 校验同一 phone + channel
	phone := segments[0].Phone
	ch := segments[0].LogicChannel
	for _, s := range segments[1:] {
		if s.Phone != phone || s.LogicChannel != ch {
			return RecordSegment{}, fmt.Errorf("merge: segments must belong to same phone/channel, got %s/%d and %s/%d",
				phone, ch, s.Phone, s.LogicChannel)
		}
	}

	// 按 StartTime 升序排序
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].StartTime.Before(segments[j].StartTime)
	})

	merged := RecordSegment{
		StreamID:     segments[0].StreamID,
		Phone:        phone,
		LogicChannel: ch,
		StreamType:   segments[0].StreamType,
		SegmentIndex: segments[0].SegmentIndex,
		StartTime:    segments[0].StartTime,
		EndTime:      segments[len(segments)-1].EndTime,
		SwitchReason: SwitchReasonStreamEnd,
		IsFragment:   true, // 合并结果标记为已修复断片
	}

	var filePaths []string
	var hasGap bool
	for i, s := range segments {
		merged.PacketCount += s.PacketCount
		merged.ByteCount += s.ByteCount
		merged.FileSize += s.FileSize
		if s.FilePath != "" {
			filePaths = append(filePaths, s.FilePath)
		}
		if i > 0 && s.IsFragment {
			hasGap = true
		}
	}
	// 如果合并的分片中存在断片，IsFragment=true；否则为 false（连续分片合并）
	merged.IsFragment = hasGap
	merged.FilePath = strings.Join(filePaths, ",")

	return merged, nil
}

// MergeByTimeRange 查询指定时间段的分片并合并为一个连续分片。
// AUTO-FIX-2026-07-02 [P1]: 便捷方法——查询 + 合并一步到位。
func (t *RecordSegmentTracker) MergeByTimeRange(phone string, logicChannel byte, start, end time.Time) (RecordSegment, error) {
	segs := t.QuerySegments(phone, logicChannel, start, end)
	return MergeSegments(segs)
}

// HistoryCount 返回已结束分片总数（供监控/测试）。
func (t *RecordSegmentTracker) HistoryCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.history)
}

// streamKey 生成设备+通道维度的唯一键。
func streamKey(phone string, logicChannel byte) string {
	return phone + ":" + string([]byte{logicChannel})
}

// endTime 返回活跃分片的结束时间（即 lastActive）。
func (s *activeSegment) endTime() time.Time {
	return s.lastActive
}

// lastSwitchReason 返回最近一次切换原因。
// AUTO-FIX-2026-07-02 [P1]: 返回 lastReason（由 OnStreamSwitch 设置），而非固定 stream_start。
func (s *activeSegment) lastSwitchReason() SwitchReason {
	if s.lastReason == "" {
		return SwitchReasonStreamStart
	}
	return s.lastReason
}
