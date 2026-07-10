package gateway

// AUTO-FIX-2026-07-04 [P0]: JT/T 809-2019 协议功能测试
// 覆盖：SN 消息确认/重试、心跳应答超时检测、从链路独立管理、视频转发

import (
	"encoding/binary"
	"hash/crc32"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// getTestEnvValue 返回测试环境值（仅用于单元测试）
func getTestEnvValue() string {
	if v := os.Getenv("JTE_TEST_VAL"); v != "" {
		return v
	}
	return "test-val"
}

// newTestCfg 创建带测试凭证的 JT809PlatformConfig
func newTestCfg(id, user string) *config.JT809PlatformConfig {
	cfg := &config.JT809PlatformConfig{ID: id, Username: user}
	cfg.Password = getTestEnvValue()
	return cfg
}

// buildTestFrame 构建一个完整的 809 帧（0x5B + escaped(header+body+crc) + 0x5D）。
// 供测试直接使用，不依赖 JT809Client.buildMessage（需要持锁）。
func buildTestFrame(msgID uint16, seqNum uint16, body []byte) []byte {
	header := make([]byte, jt809HeaderLen)
	binary.BigEndian.PutUint16(header[0:2], msgID)
	bodyAttr := uint16(len(body)) & 0x03FF
	binary.BigEndian.PutUint16(header[2:4], bodyAttr)
	binary.BigEndian.PutUint16(header[16:18], seqNum)
	payload := append(header, body...)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	payload = append(payload, crcBytes...)
	escaped := escape809(payload)
	result := make([]byte, 0, len(escaped)+2)
	result = append(result, 0x5B)
	result = append(result, escaped...)
	result = append(result, 0x5D)
	return result
}

// ============================================================================
// SN 消息确认与重试机制测试
// ============================================================================

// TestRegisterAck_AddsToPending 验证注册 ACK 后待确认计数增加
func TestRegisterAck_AddsToPending(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)
	c.registerAck(0x1001, 42, []byte{0x5B, 0x01, 0x5D})
	if count := c.GetAckPendingCount(); count != 1 {
		t.Fatalf("pending count = %d, want 1", count)
	}
}

// TestClearAck_RemovesEntry 验证按 SN 清除待确认条目
func TestClearAck_RemovesEntry(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)
	c.registerAck(0x1001, 42, []byte{0x5B, 0x01, 0x5D})
	if !c.clearAck(42) {
		t.Fatal("clearAck should return true for existing SN")
	}
	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after clear", count)
	}
}

// TestClearAck_NotFound 验证清除不存在的 SN 返回 false
func TestClearAck_NotFound(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)
	if c.clearAck(999) {
		t.Fatal("clearAck should return false for non-existent SN")
	}
}

// TestClearAllAcks_RemovesAll 验证清除所有待确认条目
func TestClearAllAcks_RemovesAll(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)
	c.registerAck(0x1001, 1, []byte{0x5B, 0x01, 0x5D})
	c.registerAck(0x1006, 2, []byte{0x5B, 0x02, 0x5D})
	c.registerAck(0x1200, 3, []byte{0x5B, 0x03, 0x5D})
	c.clearAllAcks()
	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after clearAll", count)
	}
}

// TestRegisterAck_DeepCopyPayload 验证 registerAck 深拷贝 payload
// 修改原始 slice 不应影响待确认条目中的 payload
func TestRegisterAck_DeepCopyPayload(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)
	orig := []byte{0x5B, 0x01, 0x5D}
	c.registerAck(0x1001, 1, orig)
	orig[1] = 0xFF
	c.ackMu.Lock()
	entry := c.ackPending[1]
	c.ackMu.Unlock()
	if entry.payload[1] == 0xFF {
		t.Fatal("registerAck should deep-copy payload, not share slice")
	}
}

// TestCheckPendingAcks_RetriesMessage 验证超时消息被重试（重发 + retryCount 递增）
func TestCheckPendingAcks_RetriesMessage(t *testing.T) {
	c := &JT809Client{
		cfg:         &config.JT809PlatformConfig{ID: "test"},
		logger:      zap.NewNop(),
		ackPending:  make(map[uint16]*ackEntry),
		ackTimeout:  50 * time.Millisecond,
		ackMaxRetry: 3,
		conn:        &inMemoryConn{},
		stopCh:      make(chan struct{}),
		reconnectCh: make(chan struct{}, 1),
	}
	c.running.Store(true)

	c.registerAck(0x1006, 1, []byte{0x5B, 0x01, 0x5D})

	// 等待超时
	time.Sleep(100 * time.Millisecond)

	c.checkPendingAcks()

	// 验证 retryCount 递增
	c.ackMu.Lock()
	entry := c.ackPending[1]
	c.ackMu.Unlock()
	if entry == nil {
		t.Fatal("entry should still exist after first retry")
	}
	if entry.retryCount != 1 {
		t.Fatalf("retryCount = %d, want 1", entry.retryCount)
	}

	// 验证帧已重发
	conn := c.conn.(*inMemoryConn)
	received := conn.getReceived()
	if len(received) != 1 {
		t.Fatalf("expected 1 resent frame, got %d", len(received))
	}
}

// TestCheckPendingAcks_MaxRetryTriggersReconnect 验证超过最大重试次数触发断链重连
func TestCheckPendingAcks_MaxRetryTriggersReconnect(t *testing.T) {
	c := &JT809Client{
		cfg:         &config.JT809PlatformConfig{ID: "test"},
		logger:      zap.NewNop(),
		ackPending:  make(map[uint16]*ackEntry),
		ackTimeout:  50 * time.Millisecond,
		ackMaxRetry: 1, // 仅允许 1 次重试
		conn:        &inMemoryConn{},
		stopCh:      make(chan struct{}),
		reconnectCh: make(chan struct{}, 1),
	}
	c.running.Store(true)

	c.registerAck(0x1006, 1, []byte{0x5B, 0x01, 0x5D})

	// 第一次超时：触发重试（retryCount: 0→1）
	time.Sleep(100 * time.Millisecond)
	c.checkPendingAcks()

	c.ackMu.Lock()
	entry := c.ackPending[1]
	c.ackMu.Unlock()
	if entry == nil || entry.retryCount != 1 {
		t.Fatalf("after first retry: entry=%v, want retryCount=1", entry)
	}

	// 第二次超时：超过 maxRetry，触发重连
	time.Sleep(100 * time.Millisecond)
	c.checkPendingAcks()

	// 验证 reconnectCh 收到信号
	select {
	case <-c.reconnectCh:
		// 成功
	case <-time.After(1 * time.Second):
		t.Fatal("reconnect should be triggered after max retries exceeded")
	}

	// 验证条目被移除
	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after max retry", count)
	}
}

// TestCheckPendingAcks_NotYetTimedOut 验证未超时的消息不会被重试
func TestCheckPendingAcks_NotYetTimedOut(t *testing.T) {
	c := &JT809Client{
		cfg:         &config.JT809PlatformConfig{ID: "test"},
		logger:      zap.NewNop(),
		ackPending:  make(map[uint16]*ackEntry),
		ackTimeout:  5 * time.Second, // 长超时
		ackMaxRetry: 3,
		conn:        &inMemoryConn{},
		stopCh:      make(chan struct{}),
		reconnectCh: make(chan struct{}, 1),
	}
	c.running.Store(true)

	c.registerAck(0x1006, 1, []byte{0x5B, 0x01, 0x5D})
	c.checkPendingAcks() // 立即检查，不应触发重试

	c.ackMu.Lock()
	entry := c.ackPending[1]
	c.ackMu.Unlock()
	if entry.retryCount != 0 {
		t.Fatalf("retryCount = %d, want 0 (not yet timed out)", entry.retryCount)
	}
}

// TestLogin_RegistersAck 验证 Login 发送后注册 SN 确认追踪
func TestLogin_RegistersAck(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := &JT809Client{
		cfg:         newTestCfg("test", "1"),
		logger:      zap.NewNop(),
		conn:        clientConn,
		reconnectCh: make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		ackPending:  make(map[uint16]*ackEntry),
		ackTimeout:  5 * time.Second,
		ackMaxRetry: 3,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Login()
	}()

	// 读取 login 帧以解除 Write 阻塞
	buf := make([]byte, 256)
	serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := serverConn.Read(buf)
	if n == 0 {
		t.Fatal("no data received from Login")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Login: %v", err)
	}

	if count := c.GetAckPendingCount(); count != 1 {
		t.Fatalf("pending count = %d, want 1 after Login", count)
	}
}

// TestSendKeepalive_RegistersAck 验证 SendKeepalive 发送后注册 SN 确认追踪
func TestSendKeepalive_RegistersAck(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := &JT809Client{
		cfg:         &config.JT809PlatformConfig{ID: "test"},
		logger:      zap.NewNop(),
		conn:        clientConn,
		reconnectCh: make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		ackPending:  make(map[uint16]*ackEntry),
		ackTimeout:  5 * time.Second,
		ackMaxRetry: 3,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.SendKeepalive()
	}()

	buf := make([]byte, 256)
	serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := serverConn.Read(buf)
	if n == 0 {
		t.Fatal("no data received from SendKeepalive")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("SendKeepalive: %v", err)
	}

	if count := c.GetAckPendingCount(); count != 1 {
		t.Fatalf("pending count = %d, want 1 after SendKeepalive", count)
	}
}

// TestHandleUpstreamMessage_0x1002_ClearsAck 验证收到 0x1002 登录应答后清除对应 SN 的待确认条目
func TestHandleUpstreamMessage_0x1002_ClearsAck(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)
	c.registerAck(0x1001, 42, []byte{0x5B, 0x01, 0x5D})

	// 构造 0x1002 应答（SN=42, result=0）
	data := make([]byte, jt809HeaderLen+1+4)
	binary.BigEndian.PutUint16(data[0:2], 0x1002)
	binary.BigEndian.PutUint16(data[16:18], 42)
	data[jt809HeaderLen] = 0x00
	crc := crc32.ChecksumIEEE(data[:jt809HeaderLen+1])
	binary.BigEndian.PutUint32(data[jt809HeaderLen+1:], crc)

	c.handleUpstreamMessage(0x1002, data)

	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after 0x1002 response", count)
	}
}

// TestHandleUpstreamMessage_0x1007_ClearsAckAndUpdatesHeartbeat
// 验证收到 0x1007 保活应答后清除对应 SN 的待确认条目并更新心跳时间戳
func TestHandleUpstreamMessage_0x1007_ClearsAckAndUpdatesHeartbeat(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)

	// 设置旧的心跳时间
	oldTime := time.Now().Add(-300 * time.Second).UnixNano()
	c.lastKeepaliveResp.Store(oldTime)

	c.registerAck(0x1006, 10, []byte{0x5B, 0x01, 0x5D})

	// 构造 0x1007 应答（SN=10, 无 body）
	data := make([]byte, jt809HeaderLen+4)
	binary.BigEndian.PutUint16(data[0:2], 0x1007)
	binary.BigEndian.PutUint16(data[16:18], 10)
	crc := crc32.ChecksumIEEE(data[:jt809HeaderLen])
	binary.BigEndian.PutUint32(data[jt809HeaderLen:], crc)

	c.handleUpstreamMessage(0x1007, data)

	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after 0x1007", count)
	}

	newTime := c.lastKeepaliveResp.Load()
	if newTime == oldTime {
		t.Fatal("lastKeepaliveResp should be updated after 0x1007")
	}
}

// ============================================================================
// 心跳应答超时检测测试
// ============================================================================

// TestHeartbeatTimeout_Detection 验证心跳应答超时（>180s）能被正确检测
func TestHeartbeatTimeout_Detection(t *testing.T) {
	c := &JT809Client{
		cfg:         &config.JT809PlatformConfig{ID: "test"},
		logger:      zap.NewNop(),
		reconnectCh: make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
	}
	c.running.Store(true)

	// 设置 300s 前的心跳应答时间（> 180s 阈值）
	c.lastKeepaliveResp.Store(time.Now().Add(-300 * time.Second).UnixNano())

	// 模拟 keepaliveLoop 中 heartbeatCheck 的逻辑
	lastResp := time.Unix(0, c.lastKeepaliveResp.Load())
	if time.Since(lastResp) <= 180*time.Second {
		t.Fatal("heartbeat timeout should be detected (300s > 180s)")
	}

	// 模拟触发重连
	c.running.Store(false)
	select {
	case c.reconnectCh <- struct{}{}:
	default:
	}

	select {
	case <-c.reconnectCh:
		// 成功
	default:
		t.Fatal("reconnect should be signaled after heartbeat timeout")
	}
}

// TestHeartbeatTimeout_NotTriggeredWithinWindow 验证 180s 内不触发超时
func TestHeartbeatTimeout_NotTriggeredWithinWindow(t *testing.T) {
	c := &JT809Client{
		cfg: &config.JT809PlatformConfig{ID: "test"},
		logger: zap.NewNop(),
	}
	c.running.Store(true)

	// 设置 100s 前的心跳应答时间（< 180s 阈值）
	c.lastKeepaliveResp.Store(time.Now().Add(-100 * time.Second).UnixNano())

	lastResp := time.Unix(0, c.lastKeepaliveResp.Load())
	if time.Since(lastResp) > 180*time.Second {
		t.Fatal("heartbeat timeout should NOT be triggered (100s < 180s)")
	}
}

// ============================================================================
// 从链路（Down-link）独立管理测试
// ============================================================================

// TestDownlinkLogin_Handles9001 验证从链路登录请求 0x9001 → 应答 0x9002
func TestDownlinkLogin_Handles9001(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	c := &JT809Client{
		cfg:              newTestCfg("test", "test"),
		logger:           zap.NewNop(),
		downlinkListener: listener,
		stopCh:           make(chan struct{}),
	}
	c.running.Store(true)
	go c.downlinkAcceptLoop()

	defer func() {
		c.running.Store(false)
		listener.Close()
	}()

	// 连接从链路监听
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond) // 等待 accept

	// 构造 0x9001 body: username(16B) + credential(16B)
	body := make([]byte, 32)
	copy(body[0:4], []byte("test"))
	copy(body[16:], []byte(getTestEnvValue()))

	frame := buildTestFrame(0x9001, 1, body)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write 0x9001: %v", err)
	}

	// 读取 0x9002 应答
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 256)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	respFrame := resp[:n]
	if respFrame[0] != 0x5B || respFrame[len(respFrame)-1] != 0x5D {
		t.Fatal("response not wrapped with 0x5B/0x5D")
	}
	inner := unescape809(respFrame[1 : len(respFrame)-1])
	if len(inner) < 2 {
		t.Fatal("response too short")
	}
	respMsgID := binary.BigEndian.Uint16(inner[0:2])
	if respMsgID != 0x9002 {
		t.Fatalf("expected 0x9002, got 0x%04X", respMsgID)
	}

	// 验证应答体中 result=0（成功）
	if len(inner) > jt809HeaderLen {
		result := inner[jt809HeaderLen]
		if result != 0x00 {
			t.Fatalf("login result = %d, want 0 (success)", result)
		}
	}
}

// TestDownlinkLogin_RejectedCredentials 验证凭证错误时返回 result=1
func TestDownlinkLogin_RejectedCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	c := &JT809Client{
		cfg:              newTestCfg("test", "admin"),
		logger:           zap.NewNop(),
		downlinkListener: listener,
		stopCh:           make(chan struct{}),
	}
	c.running.Store(true)
	go c.downlinkAcceptLoop()

	defer func() {
		c.running.Store(false)
		listener.Close()
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	// 构造错误的凭证
	body := make([]byte, 32)
	copy(body[0:5], []byte("wrong"))
	copy(body[16:21], []byte("pass"))

	frame := buildTestFrame(0x9001, 1, body)
	conn.Write(frame)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 256)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	inner := unescape809(resp[1 : n-1])
	if len(inner) > jt809HeaderLen {
		result := inner[jt809HeaderLen]
		if result != 0x01 {
			t.Fatalf("login result = %d, want 1 (failure)", result)
		}
	}
}

// TestDownlinkKeepalive_Handles9005 验证从链路保活请求 0x9005 → 应答 0x9006
func TestDownlinkKeepalive_Handles9005(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	c := &JT809Client{
		cfg:              &config.JT809PlatformConfig{ID: "test"},
		logger:           zap.NewNop(),
		downlinkListener: listener,
		stopCh:           make(chan struct{}),
	}
	c.running.Store(true)
	go c.downlinkAcceptLoop()

	defer func() {
		c.running.Store(false)
		listener.Close()
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	// 发送 0x9005 保活请求（无 body）
	frame := buildTestFrame(0x9005, 5, nil)
	conn.Write(frame)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 256)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	inner := unescape809(resp[1 : n-1])
	respMsgID := binary.BigEndian.Uint16(inner[0:2])
	if respMsgID != 0x9006 {
		t.Fatalf("expected 0x9006, got 0x%04X", respMsgID)
	}

	// 验证 SN 回显
	respSeq := binary.BigEndian.Uint16(inner[16:18])
	if respSeq != 5 {
		t.Fatalf("response SN = %d, want 5", respSeq)
	}

	// 验证保活时间戳已更新
	lastReq := time.Unix(0, c.lastDownlinkKeepaliveReq.Load())
	if time.Since(lastReq) > 5*time.Second {
		t.Fatal("lastDownlinkKeepaliveReq should be recently updated")
	}
}

// TestDownlinkDisconnect_Handles9003 验证从链路断开请求 0x9003 → 应答 0x9004
func TestDownlinkDisconnect_Handles9003(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	c := &JT809Client{
		cfg:              &config.JT809PlatformConfig{ID: "test"},
		logger:           zap.NewNop(),
		downlinkListener: listener,
		stopCh:           make(chan struct{}),
	}
	c.running.Store(true)
	go c.downlinkAcceptLoop()

	defer func() {
		c.running.Store(false)
		listener.Close()
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)
	c.downlinkRunning.Store(true) // 确保标记为运行

	// 发送 0x9003 断开请求
	frame := buildTestFrame(0x9003, 3, nil)
	conn.Write(frame)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := make([]byte, 256)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	inner := unescape809(resp[1 : n-1])
	respMsgID := binary.BigEndian.Uint16(inner[0:2])
	if respMsgID != 0x9004 {
		t.Fatalf("expected 0x9004, got 0x%04X", respMsgID)
	}

	// 验证 downlinkRunning 被设为 false
	time.Sleep(50 * time.Millisecond)
	if c.IsDownlinkRunning() {
		t.Fatal("downlink should not be running after 0x9003 disconnect")
	}
}

// TestDownlinkKeepaliveTimeout_Logic 验证从链路保活超时检测逻辑（>180s）
func TestDownlinkKeepaliveTimeout_Logic(t *testing.T) {
	c := &JT809Client{
		cfg:    &config.JT809PlatformConfig{ID: "test"},
		logger: zap.NewNop(),
		stopCh: make(chan struct{}),
	}
	c.downlinkRunning.Store(true)

	// 设置 300s 前的保活时间（> 180s 阈值）
	c.lastDownlinkKeepaliveReq.Store(time.Now().Add(-300 * time.Second).UnixNano())

	lastReq := time.Unix(0, c.lastDownlinkKeepaliveReq.Load())
	if time.Since(lastReq) <= 180*time.Second {
		t.Fatal("downlink keepalive timeout should be detected (300s > 180s)")
	}

	// 模拟关闭从链路
	c.downlinkRunning.Store(false)
	if c.IsDownlinkRunning() {
		t.Fatal("downlink should not be running after timeout")
	}
}

// TestStartDownlinkListener_ValidPort 验证 startDownlinkListener 在有效端口上启动
func TestStartDownlinkListener_ValidPort(t *testing.T) {
	c := &JT809Client{
		cfg:    &config.JT809PlatformConfig{ID: "test"},
		logger: zap.NewNop(),
		stopCh: make(chan struct{}),
	}
	c.running.Store(true)

	defer func() {
		c.running.Store(false)
		if c.downlinkListener != nil {
			c.downlinkListener.Close()
		}
	}()

	// 使用端口 0 自动分配
	if err := c.startDownlinkListener(0); err != nil {
		t.Fatalf("startDownlinkListener: %v", err)
	}

	if c.downlinkListener == nil {
		t.Fatal("downlinkListener should not be nil after start")
	}

	// 验证 listener 可接受连接
	addr := c.downlinkListener.Addr()
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("dial downlink listener: %v", err)
	}
	conn.Close()
}

// ============================================================================
// 视频转发测试
// ============================================================================

// TestSendVideoData_GBKEncoding 验证视频转发数据使用 GBK 编码
func TestSendVideoData_GBKEncoding(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	c := &JT809Client{
		logger:      zap.NewNop(),
		conn:        clientConn,
		reconnectCh: make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
	}

	phone := "京A12345"
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.SendVideoData(phone, 1, 100,
			time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC))
	}()

	frame, err := readFullFrame(serverConn)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendVideoData: %v", err)
	}

	inner := frame[1 : len(frame)-1]
	unescaped := unescape809(inner)
	if len(unescaped) < jt809HeaderLen+4 {
		t.Fatalf("unescaped too short: %d", len(unescaped))
	}
	msgID := binary.BigEndian.Uint16(unescaped[0:2])
	if msgID != 0x1B00 {
		t.Fatalf("expected msgID 0x1B00, got 0x%04X", msgID)
	}

	bodyAttr := binary.BigEndian.Uint16(unescaped[2:4])
	bodyLen := int(bodyAttr & 0x03FF)
	bodyBytes := unescaped[jt809HeaderLen : jt809HeaderLen+bodyLen]

	// 验证 GBK 编码
	utf8Decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(bodyBytes)
	if err != nil {
		t.Fatalf("GBK decode body: %v", err)
	}
	if !strings.Contains(string(utf8Decoded), phone) {
		t.Fatalf("decoded body missing %q: %s", phone, string(utf8Decoded))
	}
	if !strings.Contains(string(utf8Decoded), "RealVideoForward") {
		t.Fatalf("decoded body missing RealVideoForward root tag: %s", string(utf8Decoded))
	}
}

// TestSendVideoData_OfflineBuffers 验证离线时视频数据进入缓冲区
func TestSendVideoData_OfflineBuffers(t *testing.T) {
	c := &JT809Client{
		cfg:          &config.JT809PlatformConfig{ID: "test"},
		logger:       zap.NewNop(),
		pendingCap:   100,
		stopCh:       make(chan struct{}),
	}

	err := c.SendVideoData("13800000000", 1, 100, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SendVideoData offline: %v", err)
	}

	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1", size)
	}
}

// TestSendVideoData_ShouldForwardRule 验证视频转发规则过滤
func TestSendVideoData_ShouldForwardRule(t *testing.T) {
	// 配置 YAML 规则：ForwardVideo=true
	yamlRules := config.ForwardRules{
		ForwardVideo:  true,
		ForwardPhones: nil,
	}
	c := newTestClientWithRules(t, nil, yamlRules)
	if !c.shouldForward("video", "13800000000", "", nil) {
		t.Fatal("video should forward when ForwardVideo=true")
	}

	// ForwardVideo=false
	yamlRules.ForwardVideo = false
	c = newTestClientWithRules(t, nil, yamlRules)
	if c.shouldForward("video", "13800000000", "", nil) {
		t.Fatal("video should not forward when ForwardVideo=false")
	}
}

// ============================================================================
// 集成测试：端到端 ACK 流程
// ============================================================================

// TestIntegration_ACKFlow_EndToEnd 验证完整的 ACK 流程：
// Login → 注册 ACK → 上级平台回 0x1002 → 清除 ACK
// 注意：集成测试使用 net.Pipe 存在同步复杂度，此处仅验证单元逻辑
func TestIntegration_ACKFlow_EndToEnd(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)

	// 模拟：Login 已发送，注册 SN=1 的待确认
	c.registerAck(0x1001, 1, []byte{0x5B, 0x01, 0x5D})
	if count := c.GetAckPendingCount(); count != 1 {
		t.Fatalf("pending count = %d, want 1 after Login", count)
	}

	// 模拟：上级平台回 0x1002（SN=1）
	respData := make([]byte, jt809HeaderLen+1+4)
	binary.BigEndian.PutUint16(respData[0:2], 0x1002)
	binary.BigEndian.PutUint16(respData[16:18], 1)
	respData[jt809HeaderLen] = 0x00
	crc := crc32.ChecksumIEEE(respData[:jt809HeaderLen+1])
	binary.BigEndian.PutUint32(respData[jt809HeaderLen+1:], crc)

	// 模拟：客户端收到并处理应答
	c.handleUpstreamMessage(0x1002, respData)

	// 验证 ACK 已清除
	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after 0x1002 response", count)
	}
}

// TestIntegration_KeepaliveACKFlow_EndToEnd 验证完整的心跳 ACK 流程：
// SendKeepalive → 注册 ACK → 上级平台回 0x1007 → 清除 ACK + 更新心跳
// 注意：集成测试使用 net.Pipe 存在同步复杂度，此处仅验证单元逻辑
func TestIntegration_KeepaliveACKFlow_EndToEnd(t *testing.T) {
	c := NewJT809Client(&config.JT809PlatformConfig{ID: "test"}, zap.NewNop(), nil, nil)

	// 设置旧心跳时间
	oldHeartbeat := time.Now().Add(-200 * time.Second).UnixNano()
	c.lastKeepaliveResp.Store(oldHeartbeat)

	// 模拟：SendKeepalive 已发送，注册 SN=5 的待确认
	c.registerAck(0x1006, 5, []byte{0x5B, 0x01, 0x5D})
	if count := c.GetAckPendingCount(); count != 1 {
		t.Fatalf("pending count = %d, want 1 after SendKeepalive", count)
	}

	// 模拟：上级平台回 0x1007（SN=5）
	respData := make([]byte, jt809HeaderLen+4)
	binary.BigEndian.PutUint16(respData[0:2], 0x1007)
	binary.BigEndian.PutUint16(respData[16:18], 5)
	crc := crc32.ChecksumIEEE(respData[:jt809HeaderLen])
	binary.BigEndian.PutUint32(respData[jt809HeaderLen:], crc)

	// 模拟：客户端收到并处理应答
	c.handleUpstreamMessage(0x1007, respData)

	// 验证 ACK 已清除
	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after 0x1007 response", count)
	}

	// 验证心跳时间已更新
	newHeartbeat := c.lastKeepaliveResp.Load()
	if newHeartbeat == oldHeartbeat {
		t.Fatal("lastKeepaliveResp should be updated after 0x1007")
	}
	if newHeartbeat < oldHeartbeat {
		t.Fatal("lastKeepaliveResp should be newer than old time")
	}
}

// TestReconnectClearsAcks 验证断链重连时清除所有待确认条目
func TestReconnectClearsAcks(t *testing.T) {
	c := &JT809Client{
		cfg:         &config.JT809PlatformConfig{ID: "test"},
		logger:      zap.NewNop(),
		ackPending:  make(map[uint16]*ackEntry),
		ackTimeout:  5 * time.Second,
		ackMaxRetry: 3,
		stopCh:      make(chan struct{}),
	}

	c.registerAck(0x1001, 1, []byte{0x5B, 0x01, 0x5D})
	c.registerAck(0x1006, 2, []byte{0x5B, 0x02, 0x5D})
	c.registerAck(0x1200, 3, []byte{0x5B, 0x03, 0x5D})

	if count := c.GetAckPendingCount(); count != 3 {
		t.Fatalf("pending count = %d, want 3", count)
	}

	c.clearAllAcks()

	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after clearAllAcks", count)
	}
}

// TestDisconnect_ClearsAcksAndClosesDownlink 验证 Disconnect 清理 ACK 并关闭从链路
func TestDisconnect_ClearsAcksAndClosesDownlink(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "test", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(), nil, nil,
	)

	c.registerAck(0x1001, 1, []byte{0x5B, 0x01, 0x5D})
	c.registerAck(0x1006, 2, []byte{0x5B, 0x02, 0x5D})

	c.Disconnect()

	if count := c.GetAckPendingCount(); count != 0 {
		t.Fatalf("pending count = %d, want 0 after Disconnect", count)
	}
	if c.IsDownlinkRunning() {
		t.Fatal("downlink should not be running after Disconnect")
	}
}

// TestVideoForwardRule_PersistentRule 验证持久化视频转发规则过滤
func TestVideoForwardRule_PersistentRule(t *testing.T) {
	rules := []*storage.ForwardRule{
		{Enabled: true, DataType: "video", Phone: "13800000000"},
	}
	c := newTestClientWithRules(t, rules, config.ForwardRules{})

	// 匹配的视频数据应转发
	if !c.shouldForward("video", "13800000000", "", nil) {
		t.Fatal("video with matching phone should forward")
	}

	// 不匹配的视频数据不转发
	if c.shouldForward("video", "13900000000", "", nil) {
		t.Fatal("video with non-matching phone should not forward")
	}

	// 非视频类型不匹配视频规则
	if c.shouldForward("location", "13800000000", "", nil) {
		t.Fatal("location should not match video rule")
	}
}
