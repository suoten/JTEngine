package jt1078

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// 验收标准1: 4路并发播放稳定
// ===================================================================

func TestConcurrentPlayManager_MaxConcurrent(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 4, logger)

	// 注册4路流应成功
	for i := 0; i < 4; i++ {
		streamID := fmt.Sprintf("dev%d_ch1", i)
		err := mgr.RegisterStream(streamID, fmt.Sprintf("phone%d", i), 1, 0)
		if err != nil {
			t.Fatalf("RegisterStream %d failed: %v", i, err)
		}
	}

	// 第5路应被拒绝
	err := mgr.RegisterStream("dev4_ch1", "phone4", 1, 0)
	if err == nil {
		t.Fatal("expected error when exceeding max concurrent, got nil")
	}

	if mgr.ActiveCount() != 4 {
		t.Fatalf("ActiveCount = %d, want 4", mgr.ActiveCount())
	}
	if mgr.RejectedCount() != 1 {
		t.Fatalf("RejectedCount = %d, want 1", mgr.RejectedCount())
	}

	// 注销一路后应可注册新流
	mgr.UnregisterStream("dev0_ch1")
	if mgr.ActiveCount() != 3 {
		t.Fatalf("ActiveCount after unregister = %d, want 3", mgr.ActiveCount())
	}
	err = mgr.RegisterStream("dev4_ch1", "phone4", 1, 0)
	if err != nil {
		t.Fatalf("RegisterStream after unregister failed: %v", err)
	}
}

func TestConcurrentPlayManager_DuplicateStream(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 4, logger)

	err := mgr.RegisterStream("dev1_ch1", "phone1", 1, 0)
	if err != nil {
		t.Fatalf("first RegisterStream failed: %v", err)
	}

	// 重复注册应失败
	err = mgr.RegisterStream("dev1_ch1", "phone1", 1, 0)
	if err == nil {
		t.Fatal("expected error for duplicate registration, got nil")
	}
}

func TestConcurrentPlayManager_ResourceStats(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 4, logger)

	// 注册几路流
	for i := 0; i < 3; i++ {
		_ = mgr.RegisterStream(fmt.Sprintf("dev%d_ch1", i), fmt.Sprintf("phone%d", i), 1, 0)
	}

	stats := mgr.GetResourceStats()
	if stats.ActiveStreams != 3 {
		t.Errorf("ActiveStreams = %d, want 3", stats.ActiveStreams)
	}
	if stats.MaxConcurrent != 4 {
		t.Errorf("MaxConcurrent = %d, want 4", stats.MaxConcurrent)
	}
	if stats.MemAllocMB <= 0 {
		t.Error("MemAllocMB should be > 0")
	}
	if stats.GoroutineCount <= 0 {
		t.Error("GoroutineCount should be > 0")
	}
	if stats.UptimeSeconds < 0 {
		t.Error("UptimeSeconds should be >= 0")
	}
}

func TestConcurrentPlayManager_CleanupStale(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 4, logger)

	_ = mgr.RegisterStream("dev1_ch1", "phone1", 1, 0)
	_ = mgr.RegisterStream("dev2_ch1", "phone2", 1, 0)

	// 等待超过清理阈值
	time.Sleep(110 * time.Millisecond)
	cleaned := mgr.CleanupStaleStreams(50 * time.Millisecond)
	if cleaned != 2 {
		t.Fatalf("Cleaned = %d, want 2", cleaned)
	}
	if mgr.ActiveCount() != 0 {
		t.Fatalf("ActiveCount after cleanup = %d, want 0", mgr.ActiveCount())
	}
}

func TestConcurrentPlayManager_SetMaxConcurrent(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 2, logger)

	_ = mgr.RegisterStream("dev1_ch1", "p1", 1, 0)
	_ = mgr.RegisterStream("dev2_ch1", "p2", 1, 0)

	// 超限
	err := mgr.RegisterStream("dev3_ch1", "p3", 1, 0)
	if err == nil {
		t.Fatal("expected rejection at max=2")
	}

	// 调大上限后应可注册
	mgr.SetMaxConcurrent(5)
	err = mgr.RegisterStream("dev3_ch1", "p3", 1, 0)
	if err != nil {
		t.Fatalf("RegisterStream after SetMaxConcurrent failed: %v", err)
	}
}

func TestConcurrentPlayManager_ListActiveStreams(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 4, logger)

	_ = mgr.RegisterStream("dev1_ch1", "p1", 1, 0)
	_ = mgr.RegisterStream("dev2_ch3", "p2", 3, 1)

	streams := mgr.ListActiveStreams()
	if len(streams) != 2 {
		t.Fatalf("ListActiveStreams len = %d, want 2", len(streams))
	}
}

// ===================================================================
// 验收标准2: 画面质量监控 - RTP SeqNum gap 检测
// ===================================================================

func TestRTPSeqNumGap_Detection(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	session := eng.CreateSession(sessionID, "testphone", 1, 0)

	// 模拟 RTP 包序列：1,2,3,5,6（缺少4）
	seqs := []uint16{1, 2, 3, 5, 6}
	for _, seq := range seqs {
		rtpData := buildTestRTP(seq, 0, false)
		if err := eng.ProcessRTPData(sessionID, rtpData); err != nil {
			t.Fatalf("ProcessRTPData seq=%d failed: %v", seq, err)
		}
	}

	// 验证丢包统计：5 应该检测到 1 个 gap（期望4，收到5）
	if session.WindowLost != 1 {
		t.Errorf("WindowLost = %d, want 1 (gap at seq 4)", session.WindowLost)
	}
	if session.Packets != 5 {
		t.Errorf("Packets = %d, want 5", session.Packets)
	}
}

func TestRTPSeqNumGap_NoGap(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 连续序列：1,2,3,4,5
	for seq := uint16(1); seq <= 5; seq++ {
		rtpData := buildTestRTP(seq, 0, false)
		_ = eng.ProcessRTPData(sessionID, rtpData)
	}

	session := eng.GetSession(sessionID)
	if session.WindowLost != 0 {
		t.Errorf("WindowLost = %d, want 0 (no gaps)", session.WindowLost)
	}
}

func TestRTPSeqNumGap_Wraparound(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 模拟回绕：65534, 65535, 1（期望0，收到1——回绕后gap=1）
	rtpData := buildTestRTP(65534, 0, false)
	_ = eng.ProcessRTPData(sessionID, rtpData)
	rtpData = buildTestRTP(65535, 0, false)
	_ = eng.ProcessRTPData(sessionID, rtpData)
	rtpData = buildTestRTP(1, 0, false)
	_ = eng.ProcessRTPData(sessionID, rtpData)

	session := eng.GetSession(sessionID)
	// 回绕：期望0，收到1，gap = 65536 + 1 - 65536 = 1（0被跳过）
	if session.WindowLost != 1 {
		t.Errorf("WindowLost = %d, want 1 (wraparound gap at 0)", session.WindowLost)
	}
}

func TestQualityStats_Online(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 发送 RTP 包
	rtpData := buildTestRTP(1, 0, true)
	_ = eng.ProcessRTPData(sessionID, rtpData)

	stats, ok := eng.GetQualityStats(sessionID)
	if !ok {
		t.Fatal("GetQualityStats returned false")
	}
	if !stats.Online {
		t.Error("stream should be online")
	}
	if stats.Packets != 1 {
		t.Errorf("Packets = %d, want 1", stats.Packets)
	}
}

// ===================================================================
// 验收标准3: 关键帧恢复
// ===================================================================

func TestKeyFrameRecovery_Success(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewKeyFrameRecoveryTracker(logger)

	streamID := "dev1_ch1"
	tracker.RecordRequest(streamID, "dev1", 1)

	// 等待一小段时间模拟网络延迟
	time.Sleep(50 * time.Millisecond)

	// 模拟 I 帧到达
	result, ok := tracker.RecordIFrame(streamID)
	if !ok {
		t.Fatal("RecordIFrame returned false, expected match")
	}
	if !result.Success {
		t.Error("Success should be true")
	}
	if result.RecoveryTime <= 0 {
		t.Error("RecoveryTime should be > 0")
	}
	if result.RecoveryTime > 5*time.Second {
		t.Errorf("RecoveryTime = %v, should be < 5s", result.RecoveryTime)
	}
}

func TestKeyFrameRecovery_Timeout(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewKeyFrameRecoveryTracker(logger)

	streamID := "dev1_ch1"
	tracker.RecordRequest(streamID, "dev1", 1)

	// 不发送 I 帧，等待超时
	time.Sleep(10 * time.Millisecond)
	timedOut := tracker.CheckTimeout(5 * time.Millisecond)
	if len(timedOut) != 1 {
		t.Fatalf("timedOut len = %d, want 1", len(timedOut))
	}
	if timedOut[0].Success {
		t.Error("timedOut result should have Success=false")
	}
}

func TestKeyFrameRecovery_HistoryLimit(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewKeyFrameRecoveryTracker(logger)

	// 产生超过100条记录
	for i := 0; i < 120; i++ {
		streamID := fmt.Sprintf("dev%d_ch1", i)
		tracker.RecordRequest(streamID, fmt.Sprintf("dev%d", i), 1)
		tracker.RecordIFrame(streamID)
	}

	history := tracker.GetHistory()
	if len(history) > 100 {
		t.Errorf("history len = %d, should be <= 100", len(history))
	}
}

func TestKeyFrameRecovery_EngineIntegration(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	// 验证引擎有 keyframeTracker
	tracker := eng.GetKeyFrameRecoveryTracker()
	if tracker == nil {
		t.Fatal("keyframeTracker should not be nil")
	}

	// 测试 RecordKeyFrameRequest 方法
	streamID := "dev1_ch1"
	eng.RecordKeyFrameRequest(streamID, "dev1", 1)

	// 验证 pending 存在
	result, ok := tracker.GetPendingRequest(streamID)
	if !ok {
		t.Fatal("pending request should exist after RecordKeyFrameRequest")
	}
	if result.Phone != "dev1" {
		t.Errorf("Phone = %q, want dev1", result.Phone)
	}
}

// ===================================================================
// 验收标准4: 弱网自适应 - 自动切换与自动恢复
// ===================================================================

func TestAutoRecovery_TriggerRecovery(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewAutoRecoveryTracker(logger)

	streamID := "dev1_ch1"
	tracker.OnSwitchToSub(streamID, "dev1", 1, "quality_poor")

	// 前2次良好窗口不应触发恢复
	for i := 0; i < 2; i++ {
		if tracker.CheckRecovery(streamID, 1.0, 500.0) {
			t.Fatal("should not trigger recovery after only 2 good windows")
		}
	}

	// 第3次良好窗口应触发恢复
	if !tracker.CheckRecovery(streamID, 1.0, 500.0) {
		t.Fatal("should trigger recovery after 3 good windows")
	}

	// 恢复后不应再在子码流状态
	if tracker.IsOnSubStream(streamID) {
		t.Fatal("stream should not be on sub stream after recovery")
	}
}

func TestAutoRecovery_NoRecovery_BadQuality(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewAutoRecoveryTracker(logger)

	streamID := "dev1_ch1"
	tracker.OnSwitchToSub(streamID, "dev1", 1, "quality_poor")

	// 持续差质量不应触发恢复
	for i := 0; i < 10; i++ {
		if tracker.CheckRecovery(streamID, 10.0, 50.0) {
			t.Fatal("should not trigger recovery with bad quality")
		}
	}
	if !tracker.IsOnSubStream(streamID) {
		t.Fatal("stream should still be on sub stream")
	}
}

func TestAutoRecovery_ResetOnBadWindow(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewAutoRecoveryTracker(logger)

	streamID := "dev1_ch1"
	tracker.OnSwitchToSub(streamID, "dev1", 1, "quality_poor")

	// 2次良好
	tracker.CheckRecovery(streamID, 1.0, 500.0)
	tracker.CheckRecovery(streamID, 1.0, 500.0)

	// 1次差质量重置计数
	tracker.CheckRecovery(streamID, 10.0, 50.0)

	// 再2次良好不应触发
	if tracker.CheckRecovery(streamID, 1.0, 500.0) {
		t.Fatal("should not trigger - counter was reset")
	}
	if tracker.CheckRecovery(streamID, 1.0, 500.0) {
		t.Fatal("should not trigger - only 2 good after reset")
	}
	// 第3次才触发
	if !tracker.CheckRecovery(streamID, 1.0, 500.0) {
		t.Fatal("should trigger after 3 consecutive good windows")
	}
}

func TestAutoRecovery_ListSubStreams(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewAutoRecoveryTracker(logger)

	tracker.OnSwitchToSub("dev1_ch1", "dev1", 1, "quality_poor")
	tracker.OnSwitchToSub("dev2_ch1", "dev2", 1, "quality_poor")

	subs := tracker.ListSubStreams()
	if len(subs) != 2 {
		t.Fatalf("ListSubStreams len = %d, want 2", len(subs))
	}
}

// ===================================================================
// 验收标准5: 录像回放控制
// ===================================================================

func TestPlaybackControlMessage_Encode(t *testing.T) {
	// 测试 0x9203 回放控制消息编解码
	// Command: 1=暂停, 2=继续, 3=快进, 5=快退
	tests := []struct {
		name    string
		channel byte
		command byte
		speed   byte
	}{
		{"pause", 1, 1, 0},
		{"resume", 1, 2, 0},
		{"fast_forward_2x", 1, 3, 2},
		{"fast_forward_4x", 1, 3, 4},
		{"fast_rewind_2x", 1, 5, 2},
		{"keyframe", 1, 4, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &PlaybackControlMessage{
				LogicChannel: tt.channel,
				Command:      tt.command,
				Speed:        tt.speed,
			}
			data, err := msg.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			if len(data) != 3 {
				t.Errorf("data len = %d, want 3", len(data))
			}

			var decoded PlaybackControlMessage
			if err := decoded.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if decoded.LogicChannel != tt.channel {
				t.Errorf("channel = %d, want %d", decoded.LogicChannel, tt.channel)
			}
			if decoded.Command != tt.command {
				t.Errorf("command = %d, want %d", decoded.Command, tt.command)
			}
			if decoded.Speed != tt.speed {
				t.Errorf("speed = %d, want %d", decoded.Speed, tt.speed)
			}
		})
	}
}

func TestPlaybackRequestMessage_Encode(t *testing.T) {
	// 测试 0x9201 回放请求消息编解码
	msg := &PlaybackRequestMessage{
		LogicChannel: 1,
		StartTime:    "260701120000",
		EndTime:      "260701130000",
		StreamType:   0,
		MediaType:    0,
		PlaybackMode: 0,
		Speed:        1,
		StorageType:  0,
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded PlaybackRequestMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.LogicChannel != 1 {
		t.Errorf("channel = %d, want 1", decoded.LogicChannel)
	}
	if decoded.StartTime != "260701120000" {
		t.Errorf("StartTime = %q, want 260701120000", decoded.StartTime)
	}
}

// ===================================================================
// 验收标准6: 云台控制延迟
// ===================================================================

func TestPTZLatency_Success(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewPTZLatencyTracker(logger)

	seqNum := uint16(1)
	tracker.RecordPTZSent(seqNum, "dev1_ch1", "dev1", 1, 2, 4) // 方向=上, 速度=4

	// 模拟延迟
	time.Sleep(100 * time.Millisecond)

	result, ok := tracker.RecordPTZAck(seqNum)
	if !ok {
		t.Fatal("RecordPTZAck returned false, expected match")
	}
	if !result.Within2s {
		t.Error("Within2s should be true for 100ms latency")
	}
	if result.Latency < 50*time.Millisecond {
		t.Errorf("Latency = %v, should be >= 50ms", result.Latency)
	}
}

func TestPTZLatency_Exceeds2s(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewPTZLatencyTracker(logger)

	seqNum := uint16(1)
	tracker.RecordPTZSent(seqNum, "dev1_ch1", "dev1", 1, 2, 4)

	// 模拟超过2秒延迟
	time.Sleep(2100 * time.Millisecond)

	result, ok := tracker.RecordPTZAck(seqNum)
	if !ok {
		t.Fatal("RecordPTZAck returned false")
	}
	if result.Within2s {
		t.Error("Within2s should be false for >2s latency")
	}
}

func TestPTZLatency_AverageLatency(t *testing.T) {
	logger := zap.NewNop()
	tracker := NewPTZLatencyTracker(logger)

	for i := 0; i < 5; i++ {
		seqNum := uint16(i + 1)
		tracker.RecordPTZSent(seqNum, "dev1_ch1", "dev1", 1, 2, 4)
		time.Sleep(50 * time.Millisecond)
		tracker.RecordPTZAck(seqNum)
	}

	avg := tracker.GetAverageLatency()
	if avg < 40 {
		t.Errorf("AverageLatency = %v, should be >= 40ms", avg)
	}
}

func TestPTZLatency_EngineIntegration(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	tracker := eng.GetPTZLatencyTracker()
	if tracker == nil {
		t.Fatal("ptzTracker should not be nil")
	}

	// 测试 RecordPTZSent
	eng.RecordPTZSent(1, "dev1_ch1", "dev1", 1, 2, 4)

	// 测试 RecordPTZAck
	eng.RecordPTZAck(1)

	history := tracker.GetPTZHistory()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
}

// ===================================================================
// 验收标准1: 并发播放 - 高并发压力测试
// ===================================================================

func TestConcurrentPlayManager_ConcurrentAccess(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	mgr := NewConcurrentPlayManager(eng, 100, logger)

	var wg sync.WaitGroup

	// 并发注册100路流
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			streamID := fmt.Sprintf("dev%d_ch1", idx)
			_ = mgr.RegisterStream(streamID, fmt.Sprintf("phone%d", idx), 1, 0)
		}(i)
	}
	wg.Wait()

	// 由于并发执行，具体哪些 goroutine 成功不确定，
	// 但活跃数应恰好为 100，拒绝数应恰好为 100
	if mgr.ActiveCount() != 100 {
		t.Errorf("ActiveCount = %d, want 100", mgr.ActiveCount())
	}
	if mgr.TotalStarted() != 100 {
		t.Errorf("TotalStarted = %d, want 100", mgr.TotalStarted())
	}
	if mgr.RejectedCount() != 100 {
		t.Errorf("RejectedCount = %d, want 100", mgr.RejectedCount())
	}
}

// ===================================================================
// 验收标准2: 画面质量监控 - RTP SeqNum gap 累计统计
// ===================================================================

func TestRTPSeqNumGap_CumulativeStats(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 模拟 RTP 包序列：1,2,3,  5,6,7,  10,11
	// Gap 1: 缺少4 → gap=1
	// Gap 2: 缺少8,9 → gap=2
	seqs := []uint16{1, 2, 3, 5, 6, 7, 10, 11}
	for _, seq := range seqs {
		rtpData := buildTestRTP(seq, 0, false)
		if err := eng.ProcessRTPData(sessionID, rtpData); err != nil {
			t.Fatalf("ProcessRTPData seq=%d failed: %v", seq, err)
		}
	}

	session := eng.GetSession(sessionID)

	// 累计丢失包数：1 + 2 = 3
	if session.TotalLost != 3 {
		t.Errorf("TotalLost = %d, want 3 (gap=1 at seq4 + gap=2 at seq8,9)", session.TotalLost)
	}

	// 累计期望包数：8 收到的 + 3 丢失的 = 11
	if session.TotalExpected != 11 {
		t.Errorf("TotalExpected = %d, want 11 (8 received + 3 lost)", session.TotalExpected)
	}

	// 最大 gap = 2
	if session.MaxGap != 2 {
		t.Errorf("MaxGap = %d, want 2", session.MaxGap)
	}

	// 累计丢包率 = 3/11 * 100 ≈ 27.27%
	expectedLossRate := float64(3) / float64(11) * 100.0
	report, ok := eng.GetGapReport(sessionID)
	if !ok {
		t.Fatal("GetGapReport returned false")
	}
	if report.CumulativeLossRate < expectedLossRate-0.1 || report.CumulativeLossRate > expectedLossRate+0.1 {
		t.Errorf("CumulativeLossRate = %.2f, want ~%.2f", report.CumulativeLossRate, expectedLossRate)
	}
	if report.TotalLost != 3 {
		t.Errorf("GapReport TotalLost = %d, want 3", report.TotalLost)
	}
	if report.MaxGap != 2 {
		t.Errorf("GapReport MaxGap = %d, want 2", report.MaxGap)
	}
}

func TestRTPSeqNumGap_SurvivesWindowReset(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 第一批：seq 1,2,4（gap=1 at seq3）
	for _, seq := range []uint16{1, 2, 4} {
		rtpData := buildTestRTP(seq, 0, false)
		_ = eng.ProcessRTPData(sessionID, rtpData)
	}

	session := eng.GetSession(sessionID)
	if session.TotalLost != 1 {
		t.Fatalf("after batch 1: TotalLost = %d, want 1", session.TotalLost)
	}
	if session.WindowLost != 1 {
		t.Fatalf("after batch 1: WindowLost = %d, want 1", session.WindowLost)
	}

	// 模拟窗口重置（computeQualityAndCheckAlerts 每秒重置 WindowLost）
	session.WindowLost = 0
	session.WindowPackets = 0
	session.WindowBytes = 0

	// 第二批：seq 5,7（gap=1 at seq6）
	for _, seq := range []uint16{5, 7} {
		rtpData := buildTestRTP(seq, 0, false)
		_ = eng.ProcessRTPData(sessionID, rtpData)
	}

	session = eng.GetSession(sessionID)
	// WindowLost 应只反映当前窗口
	if session.WindowLost != 1 {
		t.Errorf("after batch 2: WindowLost = %d, want 1 (current window only)", session.WindowLost)
	}
	// TotalLost 应累计
	if session.TotalLost != 2 {
		t.Errorf("after batch 2: TotalLost = %d, want 2 (cumulative)", session.TotalLost)
	}
}

func TestRTPSeqNumGap_NoGapCumulative(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 连续序列：1,2,3,4,5
	for seq := uint16(1); seq <= 5; seq++ {
		rtpData := buildTestRTP(seq, 0, false)
		_ = eng.ProcessRTPData(sessionID, rtpData)
	}

	session := eng.GetSession(sessionID)
	if session.TotalLost != 0 {
		t.Errorf("TotalLost = %d, want 0 (no gaps)", session.TotalLost)
	}
	if session.TotalExpected != 5 {
		t.Errorf("TotalExpected = %d, want 5", session.TotalExpected)
	}
	if session.MaxGap != 0 {
		t.Errorf("MaxGap = %d, want 0 (no gaps)", session.MaxGap)
	}

	report, ok := eng.GetGapReport(sessionID)
	if !ok {
		t.Fatal("GetGapReport returned false")
	}
	if report.CumulativeLossRate != 0 {
		t.Errorf("CumulativeLossRate = %.2f, want 0", report.CumulativeLossRate)
	}
}

func TestRTPSeqNumGap_LargeGap(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// seq 1, 101 → gap = 99
	_ = eng.ProcessRTPData(sessionID, buildTestRTP(1, 0, false))
	_ = eng.ProcessRTPData(sessionID, buildTestRTP(101, 0, false))

	session := eng.GetSession(sessionID)
	if session.TotalLost != 99 {
		t.Errorf("TotalLost = %d, want 99", session.TotalLost)
	}
	if session.MaxGap != 99 {
		t.Errorf("MaxGap = %d, want 99", session.MaxGap)
	}
}

func TestRTPSeqNumGap_ListGapReports(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	// 创建两个流，各自有 gap
	for _, sid := range []string{"dev1_ch1", "dev2_ch1"} {
		eng.CreateSession(sid, sid[:4], 1, 0)
		_ = eng.ProcessRTPData(sid, buildTestRTP(1, 0, false))
		_ = eng.ProcessRTPData(sid, buildTestRTP(3, 0, false)) // gap=1
	}

	reports := eng.ListGapReports()
	if len(reports) != 2 {
		t.Fatalf("ListGapReports len = %d, want 2", len(reports))
	}

	for _, r := range reports {
		if r.TotalLost != 1 {
			t.Errorf("report %s: TotalLost = %d, want 1", r.StreamID, r.TotalLost)
		}
	}
}

func TestRTPSeqNumGap_QualityStatsHasCumulative(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// seq 1,3 → gap=1
	_ = eng.ProcessRTPData(sessionID, buildTestRTP(1, 0, false))
	_ = eng.ProcessRTPData(sessionID, buildTestRTP(3, 0, false))

	stats, ok := eng.GetQualityStats(sessionID)
	if !ok {
		t.Fatal("GetQualityStats returned false")
	}
	if stats.TotalLost != 1 {
		t.Errorf("QualityStats TotalLost = %d, want 1", stats.TotalLost)
	}
	if stats.TotalExpected != 3 {
		t.Errorf("QualityStats TotalExpected = %d, want 3 (1 received + 1 gap + 1 received)", stats.TotalExpected)
	}
	if stats.CumulativeLossRate <= 0 {
		t.Errorf("CumulativeLossRate = %.2f, should be > 0", stats.CumulativeLossRate)
	}
	if stats.MaxGap != 1 {
		t.Errorf("MaxGap = %d, want 1", stats.MaxGap)
	}
}

// ===================================================================
// 验收标准2: 画面质量监控 - 码率与帧率统计
// ===================================================================

func TestQualityStats_BitrateFrameRate(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	defer eng.Stop()

	sessionID := "testphone_ch1"
	eng.CreateSession(sessionID, "testphone", 1, 0)

	// 发送 10 个 RTP 包，每包 100 字节 payload + 12 字节头 = 112 字节
	// 其中第 5 和第 10 个包设置 Marker=true（2 帧）
	for i := 1; i <= 10; i++ {
		marker := i == 5 || i == 10
		rtpData := buildTestRTP(uint16(i), uint32(i*3000), marker)
		// 添加 payload
		rtpData = append(rtpData, make([]byte, 100)...)
		_ = eng.ProcessRTPData(sessionID, rtpData)
	}

	session := eng.GetSession(sessionID)
	if session.Packets != 10 {
		t.Errorf("Packets = %d, want 10", session.Packets)
	}
	if session.WindowFrames != 2 {
		t.Errorf("WindowFrames = %d, want 2", session.WindowFrames)
	}
	if session.WindowBytes != 1120 {
		t.Errorf("WindowBytes = %d, want 1120", session.WindowBytes)
	}
}

// ===================================================================
// 辅助函数
// ===================================================================

// buildTestRTP 构建测试用 RTP 包。
// seq: 序列号, ts: 时间戳, marker: 帧边界标记
func buildTestRTP(seq uint16, ts uint32, marker bool) []byte {
	buf := make([]byte, 12) // 最小 RTP 头
	buf[0] = 0x80            // V=2, P=0, X=0, CC=0
	b1 := byte(96)           // PT=96 (H264)
	if marker {
		b1 |= 0x80
	}
	buf[1] = b1
	binary.BigEndian.PutUint16(buf[2:4], seq)
	binary.BigEndian.PutUint32(buf[4:8], ts)
	binary.BigEndian.PutUint32(buf[8:12], 0x12345678) // SSRC
	return buf
}
