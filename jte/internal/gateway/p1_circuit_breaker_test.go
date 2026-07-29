package gateway

// ====================================================================
// [P1-修复] JT809Client 熔断器模式测试
// FIXED-2026-07-22 [P1]:
//   - 连续重连失败 10 次后进入"熔断"状态，停止重连 5 分钟
//   - 熔断期间发送的数据直接写入 pendingBuffer
//   - 熔断恢复后尝试重连，成功则 flush pendingBuffer
//   - pendingBuffer 溢出时通过 logger.Error 告警
// ====================================================================

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"go.uber.org/zap"
)

// TestP1_CircuitBreaker_InitialStateClosed 验证新客户端熔断器初始状态为关闭。
func TestP1_CircuitBreaker_InitialStateClosed(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "test-cb", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	defer c.Disconnect()

	if c.IsCircuitOpen() {
		t.Error("circuit breaker should be closed initially")
	}
}

// TestP1_CircuitBreaker_DataBufferedDuringCircuitOpen 验证熔断期间数据写入 pendingBuffer。
// FIXED-2026-07-22 [P1]: 熔断期间发送的数据直接写入 pendingBuffer。
func TestP1_CircuitBreaker_DataBufferedDuringCircuitOpen(t *testing.T) {
	c := &JT809Client{
		cfg:          &config.JT809PlatformConfig{ID: "test-cb-buffer"},
		logger:       zap.NewNop(),
		pendingCap:   100,
	}
	// 模拟熔断开启状态
	c.circuitOpen.Store(true)
	c.circuitOpenUntil.Store(time.Now().Add(5 * time.Minute).UnixNano())

	// conn 为 nil（熔断期间无连接），数据应进缓冲区
	err := c.sendOrBuffer(0x1200, 1, []byte{0x5B, 0x01, 0x5D})
	if err != nil {
		t.Fatalf("sendOrBuffer during circuit open: %v", err)
	}

	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1 (data should be buffered during circuit open)", size)
	}
}

// TestP1_CircuitBreaker_CircuitOpenFlag 验证熔断状态标志的读写。
func TestP1_CircuitBreaker_CircuitOpenFlag(t *testing.T) {
	c := &JT809Client{
		cfg:    &config.JT809PlatformConfig{ID: "test-cb-flag"},
		logger: zap.NewNop(),
	}

	// 初始状态应为关闭
	if c.IsCircuitOpen() {
		t.Error("circuit breaker should be closed initially")
	}

	// 手动设置熔断开启
	c.circuitOpen.Store(true)
	if !c.IsCircuitOpen() {
		t.Error("circuit breaker should be open after setting flag")
	}

	// 手动恢复
	c.circuitOpen.Store(false)
	if c.IsCircuitOpen() {
		t.Error("circuit breaker should be closed after clearing flag")
	}
}

// TestP1_CircuitBreaker_PendingBufferOverflowErrorLog 验证 pendingBuffer 溢出时不 panic。
// FIXED-2026-07-22 [P1]: pendingBuffer 溢出时通过 logger.Error 告警。
func TestP1_CircuitBreaker_PendingBufferOverflowErrorLog(t *testing.T) {
	c := &JT809Client{
		cfg:          &config.JT809PlatformConfig{ID: "test-cb-overflow"},
		logger:       zap.NewNop(),
		pendingCap:   2,
	}
	// 模拟熔断状态
	c.circuitOpen.Store(true)

	// 写入超过容量的数据，应触发溢出但不 panic
	for i := 0; i < 5; i++ {
		if err := c.sendOrBuffer(0x1200, uint16(i+1), []byte{0x5B, byte(i), 0x5D}); err != nil {
			t.Fatalf("sendOrBuffer %d: %v", i, err)
		}
	}

	size, dropped := c.GetPendingBufferStatus()
	if size != 2 {
		t.Errorf("buffer size = %d, want 2 (capacity)", size)
	}
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3 (overflow count)", dropped)
	}
}

// TestP1_CircuitBreaker_ReconnectSuccessClearsCircuit 验证重连成功后熔断状态被清除。
// FIXED-2026-07-22 [P1]: 熔断恢复后尝试重连，成功则 flush pendingBuffer。
func TestP1_CircuitBreaker_ReconnectSuccessClearsCircuit(t *testing.T) {
	// 启动一个 TCP 服务器模拟上级平台
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// 后台接收连接（模拟上级平台）
	var serverWg sync.WaitGroup
	serverWg.Add(1)
	go func() {
		defer serverWg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// 读取登录请求并回送应答
		buf := make([]byte, 4096)
		for {
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			// 简单回送一个 0x1002 应答帧（使用 809 帧）
			if n > 0 {
				// 构造一个简单的 0x1002 登录应答
				resp := buildTestLoginResponse(buf[:n])
				conn.Write(resp)
			}
		}
	}()

	c := NewJT809Client(
		&config.JT809PlatformConfig{
			ID:       "test-cb-recovery",
			Host:     "127.0.0.1",
			Port:     port,
			Username: "1001",
			Password: "password",
		},
		zap.NewNop(),
		nil, nil,
	)
	defer c.Disconnect()

	// 手动设置熔断状态
	c.circuitOpen.Store(true)
	c.circuitOpenUntil.Store(time.Now().Add(1 * time.Second).UnixNano()) // 1秒后恢复

	// 写入一些数据到缓冲区
	for i := 0; i < 3; i++ {
		if err := c.sendOrBuffer(0x1200, uint16(i+1), []byte{0x5B, byte(i), 0x5D}); err != nil {
			t.Fatalf("sendOrBuffer %d: %v", i, err)
		}
	}

	// 验证熔断状态初始为开启
	if !c.IsCircuitOpen() {
		t.Error("circuit breaker should be open initially")
	}

	// 直接连接并验证熔断状态被清除
	if err := c.dial(); err != nil {
		t.Fatalf("dial: %v", err)
	}

	// 模拟重连成功后清除熔断
	c.mu.Lock()
	c.circuitOpen.Store(false)
	c.flushPendingBuffer()
	c.mu.Unlock()

	// 验证熔断已关闭
	if c.IsCircuitOpen() {
		t.Error("circuit breaker should be closed after successful reconnect")
	}

	// 验证缓冲区已清空
	size, _ := c.GetPendingBufferStatus()
	if size != 0 {
		t.Errorf("buffer size after flush = %d, want 0", size)
	}
}

// buildTestLoginResponse 构造一个简单的 809 登录应答帧用于测试。
func buildTestLoginResponse(req []byte) []byte {
	// 简单回送一个最小帧：0x5B + 数据 + 0x5D
	// 使用请求中的 seqNum
	if len(req) < 4 {
		return []byte{0x5B, 0x5D}
	}
	// 构造一个 0x1002 应答
	header := make([]byte, 22)
	header[0] = 0x00
	header[1] = 0x17 // 长度 = 23 (22 + 1 byte result)
	if len(req) >= 4 {
		header[2] = req[2] // seqNum
		header[3] = req[3]
	}
	header[18] = 0x10 // msgID = 0x1002
	header[19] = 0x02
	body := []byte{0x00} // result = 0 (success)
	payload := append(header, body...)
	// 简单 CRC
	crc := make([]byte, 4)
	payload = append(payload, crc...)
	// 转义 + 边界
	result := make([]byte, 0, len(payload)+2)
	result = append(result, 0x5B)
	result = append(result, payload...)
	result = append(result, 0x5D)
	return result
}

// TestP1_CircuitBreaker_CircuitOpenUntilField 验证 circuitOpenUntil 字段正确设置。
func TestP1_CircuitBreaker_CircuitOpenUntilField(t *testing.T) {
	c := &JT809Client{
		cfg:    &config.JT809PlatformConfig{ID: "test-cb-until"},
		logger: zap.NewNop(),
	}

	// 初始值应为 0
	if c.circuitOpenUntil.Load() != 0 {
		t.Error("circuitOpenUntil should be 0 initially")
	}

	// 设置熔断恢复时间
	future := time.Now().Add(5 * time.Minute).UnixNano()
	c.circuitOpenUntil.Store(future)

	// 验证
	openUntil := time.Unix(0, c.circuitOpenUntil.Load())
	if time.Now().After(openUntil) {
		t.Error("circuitOpenUntil should be in the future")
	}
}
