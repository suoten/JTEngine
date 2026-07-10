package jt1078

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// P2-7: 录制断片防护 —— 测试
// ===================================================================

// mockAlertWriter 收集断片告警用于测试断言。
type mockAlertWriter struct {
	mu      sync.Mutex
	alerts  []FragmentAlert
	failErr error
}

func (m *mockAlertWriter) WriteFragmentAlert(alert FragmentAlert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, alert)
	return m.failErr
}

func (m *mockAlertWriter) getAlerts() []FragmentAlert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]FragmentAlert, len(m.alerts))
	copy(out, m.alerts)
	return out
}

func TestRecordSegmentTracker_NoFragment_ContinuousStream(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	// 第一段流开始
	tracker.OnStreamStart("s1", "13800000001", 1, 0, t0)
	// 持续收包
	for i := 0; i < 100; i++ {
		tracker.OnPacket("s1", "13800000001", 1, 1400, t0.Add(time.Duration(i)*time.Second))
	}
	// 流结束
	tracker.OnStreamEnd("s1", "13800000001", 1, t0.Add(100*time.Second))

	// 立即重新开始（间隔 < 5s，不应产生断片）
	tracker.OnStreamStart("s2", "13800000001", 1, 0, t0.Add(101*time.Second))

	alerts := writer.getAlerts()
	if len(alerts) != 0 {
		t.Fatalf("连续流不应产生断片告警，got %d alerts", len(alerts))
	}
}

func TestRecordSegmentTracker_FragmentDetected(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	// 第一段流
	tracker.OnStreamStart("s1", "13800000002", 1, 0, t0)
	tracker.OnPacket("s1", "13800000002", 1, 1400, t0.Add(1*time.Second))
	tracker.OnStreamEnd("s1", "13800000002", 1, t0.Add(2*time.Second))

	// 间隔 10 秒后重新开始（> 5s 阈值，应产生断片告警）
	tracker.OnStreamStart("s2", "13800000002", 1, 0, t0.Add(12*time.Second))

	alerts := writer.getAlerts()
	if len(alerts) != 1 {
		t.Fatalf("应产生 1 条断片告警，got %d", len(alerts))
	}
	alert := alerts[0]
	if alert.Phone != "13800000002" {
		t.Errorf("告警 Phone = %q, want 13800000002", alert.Phone)
	}
	if alert.GapDuration < 5.0 {
		t.Errorf("断片时长 = %.1fs, want > 5s", alert.GapDuration)
	}
	// 断片间隔：上一段 endTime(t0+2s) 到当前段 startTime(t0+12s) = 10s
	if alert.GapDuration < 9.9 || alert.GapDuration > 10.1 {
		t.Errorf("断片时长 = %.2fs, want ~10s", alert.GapDuration)
	}
}

func TestRecordSegmentTracker_SegmentIndexIncrement(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	phone := "13800000003"

	// 第一段
	tracker.OnStreamStart("s1", phone, 1, 0, t0)
	tracker.OnPacket("s1", phone, 1, 1000, t0.Add(1*time.Second))
	seg1 := tracker.OnStreamSwitch("s1", phone, 1, SwitchReasonStreamEnd, t0.Add(2*time.Second))
	if seg1.SegmentIndex != 0 {
		t.Errorf("第一段 index = %d, want 0", seg1.SegmentIndex)
	}
	if seg1.SwitchReason != SwitchReasonStreamEnd {
		t.Errorf("第一段 reason = %q, want stream_end", seg1.SwitchReason)
	}
	if seg1.PacketCount != 1 {
		t.Errorf("第一段 packets = %d, want 1", seg1.PacketCount)
	}

	// 第二段
	tracker.OnStreamStart("s2", phone, 1, 0, t0.Add(3*time.Second))
	seg2, ok2 := tracker.GetActiveSegment(phone, 1)
	if !ok2 {
		t.Fatal("第二段未找到")
	}
	if seg2.SegmentIndex != 1 {
		t.Errorf("第二段 index = %d, want 1", seg2.SegmentIndex)
	}
}

func TestRecordSegmentTracker_RecordingAlwaysMainStream(t *testing.T) {
	// P2-7 要求：录制侧始终录制主码流，即使播放侧切到子码流
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	// 播放侧请求子码流（streamType=1），但录制侧应记录为主码流（0）
	tracker.OnStreamStart("s1", "13800000004", 1, 1, t0)

	seg, ok := tracker.GetActiveSegment("13800000004", 1)
	if !ok {
		t.Fatal("分片未创建")
	}
	if seg.StreamType != 0 {
		t.Errorf("录制码流类型 = %d, want 0 (录制始终为主码流)", seg.StreamType)
	}
}

func TestRecordSegmentTracker_OnPacketAutoCreate(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	// 未调用 OnStreamStart，直接收包应自动创建分片
	tracker.OnPacket("s1", "13800000005", 1, 1400, time.Now())

	seg, ok := tracker.GetActiveSegment("13800000005", 1)
	if !ok {
		t.Fatal("自动创建分片失败")
	}
	if seg.PacketCount != 1 {
		t.Errorf("packets = %d, want 1", seg.PacketCount)
	}
	if seg.ByteCount != 1400 {
		t.Errorf("bytes = %d, want 1400", seg.ByteCount)
	}
}

func TestRecordSegmentTracker_QualityPoorSwitchReason(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	tracker.OnStreamStart("s1", "13800000006", 1, 0, t0)
	tracker.OnPacket("s1", "13800000006", 1, 1000, t0.Add(1*time.Second))

	// 质量差切换（播放侧切子码流，录制侧不结束分片但记录原因）
	seg := tracker.OnStreamSwitch("s1", "13800000006", 1, SwitchReasonQualityPoor, t0.Add(2*time.Second))
	if seg.SwitchReason != SwitchReasonQualityPoor {
		t.Errorf("reason = %q, want quality_poor", seg.SwitchReason)
	}
	if seg.SwitchTime.IsZero() {
		t.Error("switch_time 未设置")
	}
}

func TestRecordSegmentTracker_AlertWriterFailure(t *testing.T) {
	// 告警写入失败不应 panic 或阻塞录制
	writer := &mockAlertWriter{failErr: errMockWriteFail}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	tracker.OnStreamStart("s1", "13800000007", 1, 0, t0)
	tracker.OnStreamEnd("s1", "13800000007", 1, t0.Add(1*time.Second))
	// 间隔 > 5s 重新开始，触发断片告警（写入会失败）
	tracker.OnStreamStart("s2", "13800000007", 1, 0, t0.Add(10*time.Second))

	// 应继续工作不 panic
	seg, ok := tracker.GetActiveSegment("13800000007", 1)
	if !ok {
		t.Fatal("告警写入失败后跟踪器应继续工作")
	}
	if seg.Phone != "13800000007" {
		t.Errorf("phone = %q", seg.Phone)
	}
}

func TestRecordSegmentTracker_MultipleChannels(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	phone := "13800000008"

	// 同设备不同通道应独立跟踪
	tracker.OnStreamStart("s1", phone, 1, 0, t0)
	tracker.OnStreamStart("s2", phone, 2, 0, t0)

	seg1, ok1 := tracker.GetActiveSegment(phone, 1)
	seg2, ok2 := tracker.GetActiveSegment(phone, 2)
	if !ok1 || !ok2 {
		t.Fatal("两通道分片应独立存在")
	}
	if seg1.LogicChannel == seg2.LogicChannel {
		t.Fatal("两通道分片应独立（通道号不同）")
	}
}

func TestRecordSegmentTracker_SetAlertWriter(t *testing.T) {
	tracker := NewRecordSegmentTracker(zap.NewNop(), nil)

	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	tracker.OnStreamStart("s1", "13800000009", 1, 0, t0)
	tracker.OnStreamEnd("s1", "13800000009", 1, t0.Add(1*time.Second))

	// 延迟注入 writer 后再触发断片
	writer := &mockAlertWriter{}
	tracker.SetAlertWriter(writer)
	tracker.OnStreamStart("s2", "13800000009", 1, 0, t0.Add(10*time.Second))

	if len(writer.getAlerts()) != 1 {
		t.Fatalf("延迟注入 writer 后应能收到告警，got %d", len(writer.getAlerts()))
	}
}

// errMockWriteFail 模拟告警写入失败。
var errMockWriteFail = errMock("mock write failure")

type errMock string

func (e errMock) Error() string { return string(e) }

// ===================================================================
// AUTO-FIX-2026-07-02 [P1]: 录制断片 FilePath/Size + 按时间段查询 + 合并接口 测试
// ===================================================================

// TestRecordSegmentTracker_SetFilePath 验证 SetSegmentFilePath 设置活跃分片的文件路径与大小。
func TestRecordSegmentTracker_SetFilePath(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)

	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	tracker.OnStreamStart("s1", "13800000010", 1, 0, t0)
	tracker.OnPacket("s1", "13800000010", 1, 1400, t0.Add(1*time.Second))

	// 设置文件路径与大小
	tracker.SetSegmentFilePath("13800000010", 1, "13800000010/1/2026/07/02/100000_main.mp4", 102400)

	seg, ok := tracker.GetActiveSegment("13800000010", 1)
	if !ok {
		t.Fatal("分片未找到")
	}
	if seg.FilePath != "13800000010/1/2026/07/02/100000_main.mp4" {
		t.Errorf("FilePath = %q, want 13800000010/1/2026/07/02/100000_main.mp4", seg.FilePath)
	}
	if seg.FileSize != 102400 {
		t.Errorf("FileSize = %d, want 102400", seg.FileSize)
	}
}

// TestRecordSegmentTracker_QuerySegments 验证按时间段查询已结束分片列表。
func TestRecordSegmentTracker_QuerySegments(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)
	phone := "13800000011"

	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	// 分片1: 10:00:00 - 10:05:00
	tracker.OnStreamStart("s1", phone, 1, 0, t0)
	tracker.OnPacket("s1", phone, 1, 1000, t0.Add(1*time.Second))
	tracker.SetSegmentFilePath(phone, 1, "seg1.mp4", 1000)
	tracker.OnStreamEnd("s1", phone, 1, t0.Add(5*time.Minute))

	// 分片2: 10:10:00 - 10:15:00（间隔5分钟，>5s → 断片）
	tracker.OnStreamStart("s2", phone, 1, 0, t0.Add(10*time.Minute))
	tracker.OnPacket("s2", phone, 1, 2000, t0.Add(10*time.Minute+1*time.Second))
	tracker.SetSegmentFilePath(phone, 1, "seg2.mp4", 2000)
	tracker.OnStreamEnd("s2", phone, 1, t0.Add(15*time.Minute))

	// 查询 10:00 - 10:20 → 应返回 2 个分片
	results := tracker.QuerySegments(phone, 1, t0, t0.Add(20*time.Minute))
	if len(results) != 2 {
		t.Fatalf("QuerySegments 返回 %d 个分片, want 2", len(results))
	}
	// 验证按 StartTime 升序
	if !results[0].StartTime.Before(results[1].StartTime) {
		t.Error("结果未按 StartTime 升序排列")
	}
	// 验证 FilePath 保留
	if results[0].FilePath != "seg1.mp4" {
		t.Errorf("分片1 FilePath = %q, want seg1.mp4", results[0].FilePath)
	}
	if results[1].FilePath != "seg2.mp4" {
		t.Errorf("分片2 FilePath = %q, want seg2.mp4", results[1].FilePath)
	}
	// 分片2 应标记为断片（与分片1间隔5分钟 > 5s）
	if !results[1].IsFragment {
		t.Error("分片2 应为断片（间隔 > 5s）")
	}
	// 分片1 非断片
	if results[0].IsFragment {
		t.Error("分片1 不应为断片")
	}

	// 查询 10:06 - 10:09 → 应返回 0 个（分片1 在 10:05 结束，分片2 在 10:10 开始）
	results = tracker.QuerySegments(phone, 1, t0.Add(6*time.Minute), t0.Add(9*time.Minute))
	if len(results) != 0 {
		t.Errorf("查询无重叠区间返回 %d 个分片, want 0", len(results))
	}

	// 查询 10:04 - 10:06 → 应返回 1 个（分片1 在 10:05 结束，与区间重叠）
	results = tracker.QuerySegments(phone, 1, t0.Add(4*time.Minute), t0.Add(6*time.Minute))
	if len(results) != 1 {
		t.Errorf("查询部分重叠区间返回 %d 个分片, want 1", len(results))
	}
}

// TestRecordSegmentTracker_QuerySegmentsDifferentChannels 验证不同通道独立查询。
func TestRecordSegmentTracker_QuerySegmentsDifferentChannels(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)
	phone := "13800000012"
	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	// 通道1 分片
	tracker.OnStreamStart("s1", phone, 1, 0, t0)
	tracker.OnStreamEnd("s1", phone, 1, t0.Add(1*time.Minute))

	// 通道2 分片
	tracker.OnStreamStart("s2", phone, 2, 0, t0)
	tracker.OnStreamEnd("s2", phone, 2, t0.Add(1*time.Minute))

	// 查询通道1
	results := tracker.QuerySegments(phone, 1, t0, t0.Add(2*time.Minute))
	if len(results) != 1 {
		t.Fatalf("通道1 查询返回 %d 个分片, want 1", len(results))
	}
	if results[0].LogicChannel != 1 {
		t.Errorf("通道 = %d, want 1", results[0].LogicChannel)
	}

	// 查询通道2
	results = tracker.QuerySegments(phone, 2, t0, t0.Add(2*time.Minute))
	if len(results) != 1 {
		t.Fatalf("通道2 查询返回 %d 个分片, want 1", len(results))
	}
	if results[0].LogicChannel != 2 {
		t.Errorf("通道 = %d, want 2", results[0].LogicChannel)
	}
}

// TestMergeSegments_Success 验证合并多个分片成功。
func TestMergeSegments_Success(t *testing.T) {
	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	phone := "13800000013"

	segs := []RecordSegment{
		{
			StreamID:     "s1",
			Phone:        phone,
			LogicChannel: 1,
			SegmentIndex: 0,
			StartTime:    t0,
			EndTime:      t0.Add(5 * time.Minute),
			PacketCount:  100,
			ByteCount:    100000,
			FileSize:     50000,
			FilePath:     "seg1.mp4",
			IsFragment:   false,
		},
		{
			StreamID:     "s2",
			Phone:        phone,
			LogicChannel: 1,
			SegmentIndex: 1,
			StartTime:    t0.Add(10 * time.Minute),
			EndTime:      t0.Add(15 * time.Minute),
			PacketCount:  200,
			ByteCount:    200000,
			FileSize:     80000,
			FilePath:     "seg2.mp4",
			IsFragment:   true, // 断片
		},
	}

	merged, err := MergeSegments(segs)
	if err != nil {
		t.Fatalf("MergeSegments 失败: %v", err)
	}

	// 验证时间范围
	if !merged.StartTime.Equal(t0) {
		t.Errorf("StartTime = %v, want %v", merged.StartTime, t0)
	}
	if !merged.EndTime.Equal(t0.Add(15 * time.Minute)) {
		t.Errorf("EndTime = %v, want %v", merged.EndTime, t0.Add(15*time.Minute))
	}

	// 验证求和
	if merged.PacketCount != 300 {
		t.Errorf("PacketCount = %d, want 300", merged.PacketCount)
	}
	if merged.ByteCount != 300000 {
		t.Errorf("ByteCount = %d, want 300000", merged.ByteCount)
	}
	if merged.FileSize != 130000 {
		t.Errorf("FileSize = %d, want 130000", merged.FileSize)
	}

	// 验证 FilePath 拼接
	if merged.FilePath != "seg1.mp4,seg2.mp4" {
		t.Errorf("FilePath = %q, want seg1.mp4,seg2.mp4", merged.FilePath)
	}

	// 验证断片标记（合并的分片中存在断片）
	if !merged.IsFragment {
		t.Error("合并结果应标记 IsFragment=true（含断片）")
	}

	// 验证 phone/channel
	if merged.Phone != phone {
		t.Errorf("Phone = %q, want %q", merged.Phone, phone)
	}
	if merged.LogicChannel != 1 {
		t.Errorf("LogicChannel = %d, want 1", merged.LogicChannel)
	}
}

// TestMergeSegments_ContinuousNoFragment 验证连续分片合并不标记断片。
func TestMergeSegments_ContinuousNoFragment(t *testing.T) {
	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	phone := "13800000014"

	segs := []RecordSegment{
		{
			Phone:        phone,
			LogicChannel: 1,
			StartTime:    t0,
			EndTime:      t0.Add(5 * time.Minute),
			IsFragment:   false,
		},
		{
			Phone:        phone,
			LogicChannel: 1,
			StartTime:    t0.Add(5 * time.Minute),
			EndTime:      t0.Add(10 * time.Minute),
			IsFragment:   false,
		},
	}

	merged, err := MergeSegments(segs)
	if err != nil {
		t.Fatalf("MergeSegments 失败: %v", err)
	}
	if merged.IsFragment {
		t.Error("连续分片合并应 IsFragment=false")
	}
}

// TestMergeSegments_DifferentChannels 验证不同通道分片不能合并。
func TestMergeSegments_DifferentChannels(t *testing.T) {
	segs := []RecordSegment{
		{Phone: "13800000015", LogicChannel: 1, StartTime: time.Now()},
		{Phone: "13800000015", LogicChannel: 2, StartTime: time.Now()},
	}
	_, err := MergeSegments(segs)
	if err == nil {
		t.Fatal("不同通道分片合并应返回错误")
	}
}

// TestMergeSegments_Empty 验证空列表合并返回错误。
func TestMergeSegments_Empty(t *testing.T) {
	_, err := MergeSegments([]RecordSegment{})
	if err == nil {
		t.Fatal("空列表合并应返回错误")
	}
}

// TestMergeSegments_Single 验证单分片合并原样返回。
func TestMergeSegments_Single(t *testing.T) {
	seg := RecordSegment{
		Phone:        "13800000016",
		LogicChannel: 1,
		StartTime:    time.Now(),
		EndTime:      time.Now().Add(time.Minute),
		PacketCount:  50,
	}
	merged, err := MergeSegments([]RecordSegment{seg})
	if err != nil {
		t.Fatalf("单分片合并失败: %v", err)
	}
	if merged.PacketCount != 50 {
		t.Errorf("PacketCount = %d, want 50", merged.PacketCount)
	}
}

// TestRecordSegmentTracker_MergeByTimeRange 验证按时间段查询+合并一步到位。
func TestRecordSegmentTracker_MergeByTimeRange(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)
	phone := "13800000017"
	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	// 分片1: 10:00 - 10:05
	tracker.OnStreamStart("s1", phone, 1, 0, t0)
	tracker.SetSegmentFilePath(phone, 1, "seg1.mp4", 50000)
	tracker.OnStreamEnd("s1", phone, 1, t0.Add(5*time.Minute))

	// 分片2: 10:10 - 10:15（断片）
	tracker.OnStreamStart("s2", phone, 1, 0, t0.Add(10*time.Minute))
	tracker.SetSegmentFilePath(phone, 1, "seg2.mp4", 80000)
	tracker.OnStreamEnd("s2", phone, 1, t0.Add(15*time.Minute))

	// 合并 10:00 - 10:20 的所有分片
	merged, err := tracker.MergeByTimeRange(phone, 1, t0, t0.Add(20*time.Minute))
	if err != nil {
		t.Fatalf("MergeByTimeRange 失败: %v", err)
	}
	if !merged.StartTime.Equal(t0) {
		t.Errorf("StartTime = %v, want %v", merged.StartTime, t0)
	}
	if !merged.EndTime.Equal(t0.Add(15 * time.Minute)) {
		t.Errorf("EndTime = %v, want %v", merged.EndTime, t0.Add(15*time.Minute))
	}
	if merged.FileSize != 130000 {
		t.Errorf("FileSize = %d, want 130000", merged.FileSize)
	}
	if merged.FilePath != "seg1.mp4,seg2.mp4" {
		t.Errorf("FilePath = %q, want seg1.mp4,seg2.mp4", merged.FilePath)
	}
}

// TestRecordSegmentTracker_HistoryCount 验证历史分片计数。
func TestRecordSegmentTracker_HistoryCount(t *testing.T) {
	writer := &mockAlertWriter{}
	tracker := NewRecordSegmentTracker(zap.NewNop(), writer)
	phone := "13800000018"
	t0 := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	if tracker.HistoryCount() != 0 {
		t.Errorf("初始 HistoryCount = %d, want 0", tracker.HistoryCount())
	}

	// 流开始+结束 → 1 个历史分片
	tracker.OnStreamStart("s1", phone, 1, 0, t0)
	tracker.OnStreamEnd("s1", phone, 1, t0.Add(1*time.Minute))
	if tracker.HistoryCount() != 1 {
		t.Errorf("流结束后 HistoryCount = %d, want 1", tracker.HistoryCount())
	}

	// 第二段流开始（s1 已落入 history，s2 尚未结束）→ 仍 1 个历史分片
	tracker.OnStreamStart("s2", phone, 1, 0, t0.Add(10*time.Minute))
	if tracker.HistoryCount() != 1 {
		t.Errorf("第二段开始后（未结束）HistoryCount = %d, want 1", tracker.HistoryCount())
	}

	// 第二段流结束 → 2 个历史分片
	tracker.OnStreamEnd("s2", phone, 1, t0.Add(11*time.Minute))
	if tracker.HistoryCount() != 2 {
		t.Errorf("第二段结束后 HistoryCount = %d, want 2", tracker.HistoryCount())
	}
}
