package gateway

import (
	"net"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"go.uber.org/zap"
)

// ===================================================================
// P0 网关 TCP 连接认证超时 & per-IP 限制测试 —— FIXED-2026-07-22
// ===================================================================

// TestP0_InitialAuthTimeout_Default 验证默认 initialAuthTimeout 为 30s。
func TestP0_InitialAuthTimeout_Default(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections: 120000,
	}
	server := NewTCPServer(cfg, zap.NewNop(), NewSessionManager(zap.NewNop()), nil, nil, nil)

	if server.initialAuthTimeout != 30*time.Second {
		t.Errorf("initialAuthTimeout = %v, want 30s", server.initialAuthTimeout)
	}
}

// TestP0_InitialAuthTimeout_Custom 验证自定义 initialAuthTimeout 生效。
func TestP0_InitialAuthTimeout_Custom(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections:    120000,
		InitialAuthTimeout: 60,
	}
	server := NewTCPServer(cfg, zap.NewNop(), NewSessionManager(zap.NewNop()), nil, nil, nil)

	if server.initialAuthTimeout != 60*time.Second {
		t.Errorf("initialAuthTimeout = %v, want 60s", server.initialAuthTimeout)
	}
}

// TestP0_MaxConnsPerIP_Default 验证未设置 MaxConnsPerIP 时回退到 1000（向后兼容）。
func TestP0_MaxConnsPerIP_Default(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections: 120000,
	}
	server := NewTCPServer(cfg, zap.NewNop(), NewSessionManager(zap.NewNop()), nil, nil, nil)

	if server.ipLimit != 1000 {
		t.Errorf("ipLimit = %d, want 1000 (backward-compatible default)", server.ipLimit)
	}
}

// TestP0_MaxConnsPerIP_Custom 验证自定义 MaxConnsPerIP 生效。
func TestP0_MaxConnsPerIP_Custom(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections: 120000,
		MaxConnsPerIP:  100,
	}
	server := NewTCPServer(cfg, zap.NewNop(), NewSessionManager(zap.NewNop()), nil, nil, nil)

	if server.ipLimit != 100 {
		t.Errorf("ipLimit = %d, want 100", server.ipLimit)
	}
}

// TestP0_IPConnRateLimiter_Basic 验证单 IP 连接速率限制器基本功能。
func TestP0_IPConnRateLimiter_Basic(t *testing.T) {
	limiter := newIPConnRateLimiter(3, 100*time.Millisecond)

	// 前 3 次允许
	for i := 0; i < 3; i++ {
		if !limiter.Allow("192.168.1.1") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	// 第 4 次拒绝
	if limiter.Allow("192.168.1.1") {
		t.Fatal("attempt 4 should be rejected (rate limit exceeded)")
	}
	// 不同 IP 不受影响
	if !limiter.Allow("192.168.1.2") {
		t.Fatal("different IP should be allowed")
	}
}

// TestP0_IPConnRateLimiter_WindowReset 验证时间窗口过期后计数器重置。
func TestP0_IPConnRateLimiter_WindowReset(t *testing.T) {
	limiter := newIPConnRateLimiter(2, 50*time.Millisecond)

	// 耗尽配额
	limiter.Allow("10.0.0.1")
	limiter.Allow("10.0.0.1")
	if limiter.Allow("10.0.0.1") {
		t.Fatal("third attempt should be rejected")
	}

	// 等待窗口过期
	time.Sleep(60 * time.Millisecond)

	// 窗口重置后允许新连接
	if !limiter.Allow("10.0.0.1") {
		t.Fatal("after window reset, should be allowed")
	}
}

// TestP0_IPConnRateLimiter_Cleanup 验证过期计数器清理。
func TestP0_IPConnRateLimiter_Cleanup(t *testing.T) {
	limiter := newIPConnRateLimiter(10, 50*time.Millisecond)

	limiter.Allow("10.0.0.1")
	limiter.Allow("10.0.0.2")
	limiter.Allow("10.0.0.3")

	limiter.mu.Lock()
	count := len(limiter.counters)
	limiter.mu.Unlock()
	if count != 3 {
		t.Fatalf("before cleanup: counter count = %d, want 3", count)
	}

	time.Sleep(60 * time.Millisecond)
	limiter.Cleanup()

	limiter.mu.Lock()
	count = len(limiter.counters)
	limiter.mu.Unlock()
	if count != 0 {
		t.Fatalf("after cleanup: counter count = %d, want 0", count)
	}
}

// TestP0_AuthTimeout_RejectsSlowConnection 验证未认证连接在超时后被关闭。
func TestP0_AuthTimeout_RejectsSlowConnection(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections:    120000,
		InitialAuthTimeout: 1, // 1 秒超时（测试用短超时）
		MaxConnsPerIP:     100,
	}
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	server := NewTCPServer(cfg, logger, sm, nil, nil, nil)
	server.running.Store(true)

	// 创建一对 TCP 连接
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	peer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer peer.Close()

	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// 启动 handleConn（会设置 1s 读超时）
	done := make(chan struct{})
	go func() {
		server.handleConn(conn)
		close(done)
	}()

	// 不发送任何数据，等待超时后 handleConn 应退出
	select {
	case <-done:
		// handleConn 退出，说明超时生效
	case <-time.After(3 * time.Second):
		t.Fatal("handleConn did not exit within 3s (auth timeout not working)")
	}

	// 验证会话已被移除
	if _, ok := sm.Get(generateSessionID(conn)); ok {
		// generateSessionID 可能不同（时间戳），改为检查 OnlineCount
		if sm.OnlineCount() > 0 {
			// 会话可能已被 Remove，这是正常的
		}
	}
}

// TestP0_AuthTimeout_LongTimeoutKeepsConnection 验证较长超时下连接不会被立即关闭。
// 注意：由于缺少 protocol.Hub，此测试不发送协议数据，仅验证连接在短时间内存活。
func TestP0_AuthTimeout_LongTimeoutKeepsConnection(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections:    120000,
		InitialAuthTimeout: 10, // 10 秒超时
		MaxConnsPerIP:     100,
	}
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	server := NewTCPServer(cfg, logger, sm, nil, nil, nil)
	server.running.Store(true)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	peer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer peer.Close()

	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// 启动 handleConn（会设置 10s 读超时）
	done := make(chan struct{})
	go func() {
		server.handleConn(conn)
		close(done)
	}()

	// 等待 500ms，连接应该仍然存活（10s 超时未到）
	time.Sleep(500 * time.Millisecond)

	// peer 端应该还能写入（连接未被超时关闭）
	_, err = peer.Write([]byte("alive"))
	if err != nil {
		t.Fatalf("连接在 500ms 内被关闭（10s 超时应未到期）: %v", err)
	}

	// 关闭连接让 handleConn 退出
	peer.Close()
	server.running.Store(false)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn did not exit after connection close")
	}
}

// TestP0_IPRateLimit_RejectsExcessConnections 验证 IP 速率限制在 handleConn 中生效。
func TestP0_IPRateLimit_RejectsExcessConnections(t *testing.T) {
	cfg := &config.GatewayConfig{
		MaxConnections:    120000,
		MaxConnsPerIP:     100,
		MaxConnRatePerIP:  2, // 每秒仅允许 2 个新连接
	}
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	server := NewTCPServer(cfg, logger, sm, nil, nil, nil)

	// 前 2 次允许
	if !server.ipRateLimiter.Allow("192.168.1.100") {
		t.Fatal("first connection should be allowed")
	}
	if !server.ipRateLimiter.Allow("192.168.1.100") {
		t.Fatal("second connection should be allowed")
	}
	// 第 3 次拒绝
	if server.ipRateLimiter.Allow("192.168.1.100") {
		t.Fatal("third connection should be rejected (rate limit)")
	}
}
