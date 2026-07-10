package jt1078

import (
	"net"
	"testing"
	"time"

	"github.com/jte-engine/jte/internal/metrics"
	"go.uber.org/zap"
)

// TestRealtimeRequestTransportMode 验证 0x9101 TCP/UDP 标识位的 Marshal/Unmarshal（P1-7）。
// TransportMode=0 时 21B 标准格式；TransportMode>0 时 22B 扩展格式。
func TestRealtimeRequestTransportMode(t *testing.T) {
	// 1. TransportMode=0 (UDP) → 21B 标准格式
	udpReq := &RealtimeRequestMessage{
		IPAddress:     "192.168.1.100",
		Port:          10000,
		LogicChannel:  1,
		MediaType:     0,
		StreamType:    0,
		TransportMode: 0,
	}
	data, err := udpReq.Marshal()
	if err != nil {
		t.Fatalf("Marshal UDP: %v", err)
	}
	if len(data) != 21 {
		t.Errorf("UDP Marshal len=%d, want 21", len(data))
	}
	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal UDP: %v", err)
	}
	if parsed.TransportMode != 0 {
		t.Errorf("UDP TransportMode=%d, want 0", parsed.TransportMode)
	}
	if parsed.IPAddress != "192.168.1.100" {
		t.Errorf("IPAddress=%q, want 192.168.1.100", parsed.IPAddress)
	}
	if parsed.Port != 10000 {
		t.Errorf("Port=%d, want 10000", parsed.Port)
	}

	// 2. TransportMode=1 (TCP) → 22B 扩展格式
	tcpReq := &RealtimeRequestMessage{
		IPAddress:     "10.0.0.1",
		Port:          20000,
		LogicChannel:  2,
		MediaType:     1,
		StreamType:    1,
		TransportMode: 1,
	}
	data, err = tcpReq.Marshal()
	if err != nil {
		t.Fatalf("Marshal TCP: %v", err)
	}
	if len(data) != 22 {
		t.Errorf("TCP Marshal len=%d, want 22", len(data))
	}
	if data[21] != 1 {
		t.Errorf("byte[21]=%d, want 1 (TCP)", data[21])
	}
	parsed = &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal TCP: %v", err)
	}
	if parsed.TransportMode != 1 {
		t.Errorf("TCP TransportMode=%d, want 1", parsed.TransportMode)
	}
}

// TestRealtimeRequestBackwardCompat 验证 21B 标准报文仍能被正确解析（向后兼容）。
func TestRealtimeRequestBackwardCompat(t *testing.T) {
	stdData := make([]byte, 21)
	copy(stdData[0:16], []byte("192.168.1.1"))
	stdData[16] = 0x27 // port 10000 = 0x2710
	stdData[17] = 0x10
	stdData[18] = 1 // LogicChannel
	stdData[19] = 0 // MediaType
	stdData[20] = 0 // StreamType

	parsed := &RealtimeRequestMessage{}
	if err := parsed.Unmarshal(stdData); err != nil {
		t.Fatalf("Unmarshal 21B: %v", err)
	}
	if parsed.TransportMode != 0 {
		t.Errorf("21B TransportMode=%d, want 0 (UDP default)", parsed.TransportMode)
	}
	if parsed.Port != 10000 {
		t.Errorf("Port=%d, want 10000", parsed.Port)
	}
}

// TestAutoFallbackThreshold 验证 auto 模式 UDP 失败计数达阈值后 shouldFallbackTCP 返回 true。
// UDP 是无连接的，写入不可达端口不会报错，因此直接测试计数逻辑而非真实 UDP 失败。
func TestAutoFallbackThreshold(t *testing.T) {
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	eng.udpFailThreshold = 3
	defer eng.Stop()

	streamID := "test_threshold"

	// 初始状态：不应 fallback
	if eng.shouldFallbackTCP(streamID) {
		t.Error("initial: shouldFallbackTCP should be false")
	}

	// 2 次失败：不达阈值
	eng.recordUDPFailure(streamID)
	if eng.shouldFallbackTCP(streamID) {
		t.Error("after 1 failure: shouldFallbackTCP should be false")
	}
	eng.recordUDPFailure(streamID)
	if eng.shouldFallbackTCP(streamID) {
		t.Error("after 2 failures: shouldFallbackTCP should be false")
	}

	// 第 3 次：达阈值，recordUDPFailure 返回 true
	reached := eng.recordUDPFailure(streamID)
	if !reached {
		t.Error("after 3 failures: recordUDPFailure should return true (threshold reached)")
	}
	if !eng.shouldFallbackTCP(streamID) {
		t.Error("after 3 failures: shouldFallbackTCP should be true")
	}
}

// TestAutoFallbackForwardUsesTCP 验证 shouldFallbackTCP=true 时 ForwardRTP 走 TCP 路径。
func TestAutoFallbackForwardUsesTCP(t *testing.T) {
	// 启动 TCP 监听器
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer tcpLn.Close()
	tcpPort := tcpLn.Addr().(*net.TCPAddr).Port

	// 持续接收 TCP 连接并读数据
	go func() {
		for {
			conn, err := tcpLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						c.Close()
						return
					}
				}
			}(conn)
		}
	}()

	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "test_tcp_forward"
	eng.RegisterStreamPort(streamID, tcpPort)
	eng.SetStreamMode(streamID, "auto")

	// 人为将失败计数设为阈值，触发 shouldFallbackTCP=true
	eng.udpFailMu.Lock()
	eng.udpFailCount[streamID] = eng.udpFailThreshold
	eng.udpFailMu.Unlock()

	// ForwardRTP 应走 TCP（因 shouldFallbackTCP=true）
	rtpData := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01}
	if err := eng.ForwardRTP(streamID, rtpData); err != nil {
		t.Errorf("ForwardRTP with TCP fallback should succeed: %v", err)
	}
}

// TestAutoFallbackResetOnSuccess 验证 UDP 成功后重置失败计数。
func TestAutoFallbackResetOnSuccess(t *testing.T) {
	udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpLn.Close()
	udpPort := udpLn.LocalAddr().(*net.UDPAddr).Port

	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "test_reset"
	eng.RegisterStreamPort(streamID, udpPort)
	eng.SetStreamMode(streamID, "auto")

	// 人为设置失败计数=2
	eng.udpFailMu.Lock()
	eng.udpFailCount[streamID] = 2
	eng.udpFailMu.Unlock()

	rtpData := []byte{0x80, 0x60, 0x00, 0x01}
	if err := eng.ForwardRTP(streamID, rtpData); err != nil {
		t.Fatalf("ForwardRTP should succeed: %v", err)
	}

	// 失败计数应被重置为 0
	eng.udpFailMu.Lock()
	count := eng.udpFailCount[streamID]
	eng.udpFailMu.Unlock()
	if count != 0 {
		t.Errorf("after success: count=%d, want 0", count)
	}
}

// TestSetStreamModeResetsFailCount 验证手动切换模式时重置失败计数。
func TestSetStreamModeResetsFailCount(t *testing.T) {
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "test_mode_reset"
	eng.udpFailMu.Lock()
	eng.udpFailCount[streamID] = 5
	eng.udpFailMu.Unlock()

	eng.SetStreamMode(streamID, "tcp")

	eng.udpFailMu.Lock()
	count := eng.udpFailCount[streamID]
	eng.udpFailMu.Unlock()
	if count != 0 {
		t.Errorf("after SetStreamMode: count=%d, want 0", count)
	}
}

// TestFallback_PreservesPlaybackState 验证 UDP→TCP fallback 时播放状态完整保留
// （session / SSRC / 时间戳 / StartTime 不受传输层切换影响）。
// AUTO-FIX-2026-07-02 [P1]: project_memory 工程约定——网络中断时需自动重连并保留播放状态。
// ForwardRTP 仅操作传输层连接池，不触碰 StreamSession，因此切换天然保留状态。
func TestFallback_PreservesPlaybackState(t *testing.T) {
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer tcpLn.Close()
	tcpPort := tcpLn.Addr().(*net.TCPAddr).Port

	// 后台接收 TCP 连接并读数据
	go func() {
		for {
			conn, err := tcpLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				for {
					if _, err := c.Read(buf); err != nil {
						c.Close()
						return
					}
				}
			}(conn)
		}
	}()

	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "13800000001_ch1"
	eng.RegisterStreamPort(streamID, tcpPort)
	eng.SetStreamMode(streamID, "auto")

	// 1) 创建 session 并填充播放状态（SSRC/时间戳/StartTime/SeqNum）
	sess := eng.CreateSession(streamID, "13800000001", 1, 0)
	sess.LastSeqNum = 0x1234
	sess.LastTimestamp = 0xDEADBEEF
	sess.Packets = 42
	originalStartTime := sess.StartTime
	originalLastSeq := sess.LastSeqNum
	originalLastTs := sess.LastTimestamp
	originalPackets := sess.Packets

	// 2) 将 UDP 失败计数推到阈值，使下次 ForwardRTP 走 TCP 路径（shouldFallbackTCP=true）
	eng.udpFailMu.Lock()
	eng.udpFailCount[streamID] = eng.udpFailThreshold
	eng.udpFailMu.Unlock()

	// 3) ForwardRTP 应走 TCP 路径
	rtpData := []byte{0x80, 0x60, 0x12, 0x34, 0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}
	if err := eng.ForwardRTP(streamID, rtpData); err != nil {
		t.Fatalf("ForwardRTP should succeed via TCP fallback: %v", err)
	}

	// 4) 验证 session 状态完整保留（ForwardRTP 不应触碰 StreamSession）
	got := eng.GetSession(streamID)
	if got == nil {
		t.Fatal("session should still exist after fallback (playback state preserved)")
	}
	if got.LastSeqNum != originalLastSeq {
		t.Errorf("LastSeqNum changed: %d -> %d (should be preserved)", originalLastSeq, got.LastSeqNum)
	}
	if got.LastTimestamp != originalLastTs {
		t.Errorf("LastTimestamp changed: %d -> %d (should be preserved)", originalLastTs, got.LastTimestamp)
	}
	if got.Packets != originalPackets {
		t.Errorf("Packets changed: %d -> %d (should be preserved)", originalPackets, got.Packets)
	}
	if !got.StartTime.Equal(originalStartTime) {
		t.Errorf("StartTime changed (should be preserved)")
	}
	if got.StreamType != 0 {
		t.Errorf("StreamType = %d, want 0 (主码流 preserved)", got.StreamType)
	}
}

// TestFallback_EmitFallbackInvokesCallback 验证 emitFallback 触发回调 + 递增指标计数器。
// AUTO-FIX-2026-07-02 [P1]: 添加 fallback 事件日志和指标上报。
func TestFallback_EmitFallbackInvokesCallback(t *testing.T) {
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	fallbackFired := make(chan string, 1)
	eng.SetFallbackHandler(func(sid, phone string, ch byte, reason string) {
		select {
		case fallbackFired <- sid + "|" + phone + "|" + reason:
		default:
		}
	})

	// 记录指标前置值
	before := metrics.RTPFallbackTotal.Value()

	eng.emitFallback("13800000002_ch2", "udp_consecutive_failures")

	// 指标应 +1
	after := metrics.RTPFallbackTotal.Value()
	if after-before != 1 {
		t.Errorf("RTPFallbackTotal delta = %g, want 1", after-before)
	}

	// 回调应异步触发，携带正确的 streamID/phone/reason
	select {
	case msg := <-fallbackFired:
		want := "13800000002_ch2|13800000002|udp_consecutive_failures"
		if msg != want {
			t.Errorf("fallback callback msg = %q, want %q", msg, want)
		}
	case <-time.After(time.Second):
		t.Fatal("fallback callback not fired within 1s")
	}
}

// TestSwitchStreamType_PreservesPlaybackState 验证双码流手动切换时播放状态完整保留。
// AUTO-FIX-2026-07-02 [P1]: 双码流前端切换 UI 缺失（后端 StreamType 已定义）。
// project_memory: 双码流前端切换 UI 缺失（后端 StreamType 已定义）。
// 切换仅更新 StreamType，不触碰 SSRC/时间戳/StartTime/Packets。
func TestSwitchStreamType_PreservesPlaybackState(t *testing.T) {
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "13800000003_ch1"
	sess := eng.CreateSession(streamID, "13800000003", 1, 0) // 主码流
	sess.LastSeqNum = 0x5678
	sess.LastTimestamp = 0xCAFEBABE
	sess.Packets = 100
	originalStartTime := sess.StartTime
	originalLastSeq := sess.LastSeqNum
	originalLastTs := sess.LastTimestamp
	originalPackets := sess.Packets

	// 切换到子码流
	switched := eng.SwitchStreamType(streamID, 1)
	if !switched {
		t.Fatal("SwitchStreamType should return true when switching main→sub")
	}

	got := eng.GetSession(streamID)
	if got == nil {
		t.Fatal("session should still exist after switch")
	}
	if got.StreamType != 1 {
		t.Errorf("StreamType = %d, want 1 (sub)", got.StreamType)
	}
	// 验证播放状态完整保留
	if got.LastSeqNum != originalLastSeq {
		t.Errorf("LastSeqNum changed: %d -> %d", originalLastSeq, got.LastSeqNum)
	}
	if got.LastTimestamp != originalLastTs {
		t.Errorf("LastTimestamp changed: %d -> %d", originalLastTs, got.LastTimestamp)
	}
	if got.Packets != originalPackets {
		t.Errorf("Packets changed: %d -> %d", originalPackets, got.Packets)
	}
	if !got.StartTime.Equal(originalStartTime) {
		t.Error("StartTime changed (should be preserved)")
	}

	// 切换回主码流
	switched = eng.SwitchStreamType(streamID, 0)
	if !switched {
		t.Fatal("SwitchStreamType should return true when switching sub→main")
	}
	got = eng.GetSession(streamID)
	if got.StreamType != 0 {
		t.Errorf("StreamType = %d, want 0 (main)", got.StreamType)
	}
}

// TestSwitchStreamType_NotFound 验证流不存在时返回 false。
func TestSwitchStreamType_NotFound(t *testing.T) {
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	if eng.SwitchStreamType("nonexistent_ch1", 1) {
		t.Error("SwitchStreamType should return false for non-existent stream")
	}
}

// TestSwitchStreamType_SameType 验证新旧类型相同时返回 false（幂等）。
func TestSwitchStreamType_SameType(t *testing.T) {
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "13800000004_ch1"
	eng.CreateSession(streamID, "13800000004", 1, 0) // 主码流

	// 请求切换到相同类型（主→主）应返回 false
	if eng.SwitchStreamType(streamID, 0) {
		t.Error("SwitchStreamType should return false when type is unchanged")
	}
}
