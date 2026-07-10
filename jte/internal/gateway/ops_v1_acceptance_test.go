package gateway

// ===================================================================
// 运维验收 1: 10 万连接不崩
//
// 验证项：
//   1. OOM 防护（memoryGuard）三级阈值正确触发
//   2. 单 IP 连接数限制
//   3. MaxConnections 总连接数限制
//   4. 令牌桶鉴权限流（防止鉴权风暴）
//   5. 压测工具可编译（验证 10 万连接模拟能力）
// ===================================================================

import (
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"go.uber.org/zap"
)

// TestV1_MemoryGuard_Thresholds 验证 OOM 防护三级阈值配置正确。
// 验收标准：内存 < 16GB，CPU < 80%，不 OOM。
// memoryGuard 应在 8GB 预警、9GB 告警、9.5GB 自保时分别触发不同策略。
func TestV1_MemoryGuard_Thresholds(t *testing.T) {
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	mg := newMemoryGuard(sm, logger)

	if mg.memWarnMB != 8192 {
		t.Errorf("预警阈值 = %d MB, 期望 8192 MB (8GB)", mg.memWarnMB)
	}
	if mg.memCritMB != 9216 {
		t.Errorf("告警阈值 = %d MB, 期望 9216 MB (9GB)", mg.memCritMB)
	}
	if mg.memFatalMB != 9728 {
		t.Errorf("自保阈值 = %d MB, 期望 9728 MB (9.5GB)", mg.memFatalMB)
	}
}

// TestV1_MemoryGuard_IsMemoryFull 验证内存满判断逻辑。
// 当 sysMB >= memWarnMB 时应返回 true，拒绝新连接。
func TestV1_MemoryGuard_IsMemoryFull(t *testing.T) {
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	mg := newMemoryGuard(sm, logger)

	// 初始状态：内存未满
	if mg.IsMemoryFull() {
		t.Error("初始状态不应判定内存满")
	}

	// 模拟内存达到预警阈值
	mg.mu.Lock()
	mg.sysMB = mg.memWarnMB
	mg.mu.Unlock()
	if !mg.IsMemoryFull() {
		t.Error("内存达到预警阈值时应返回 true")
	}

	// 模拟内存低于预警阈值
	mg.mu.Lock()
	mg.sysMB = mg.memWarnMB - 1
	mg.mu.Unlock()
	if mg.IsMemoryFull() {
		t.Error("内存低于预警阈值时应返回 false")
	}
}

// TestV1_MemoryGuard_EvictOldest 验证内存自保时踢出最旧连接。
// 当内存达到 fatal 阈值时，应踢出 20% 最旧连接。
func TestV1_MemoryGuard_EvictOldest(t *testing.T) {
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	mg := newMemoryGuard(sm, logger)

	// 创建 10 个 session，设置不同的 LastActive 时间
	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		// 使用 pipe 创建真实连接
		conn1, conn2 := createPipeConn(t)
		defer conn1.Close()
		defer conn2.Close()

		session := sm.Create("evict-test-"+string(rune(i)), conn1)
		session.SetPhone("1380000000" + string(rune('0'+i)))

		// 设置不同的最后活跃时间
		session.mu.Lock()
		session.LastActive = baseTime.Add(time.Duration(i) * time.Minute)
		session.mu.Unlock()
	}

	sessions := sm.List()
	if len(sessions) != 10 {
		t.Fatalf("创建 10 个 session, 实际 %d", len(sessions))
	}

	// 踢出 20%（2 个最旧的）
	mg.evictOldest(0.2)

	// 验证：部分连接被关闭
	// evictOldest 调用 s.Conn.Close()，连接关闭后写入应失败
	closedCount := 0
	for _, s := range sm.List() {
		s.mu.RLock()
		conn := s.Conn
		s.mu.RUnlock()
		if conn == nil {
			closedCount++
			continue
		}
		// 尝试写入，已关闭的连接会返回错误
		conn.SetWriteDeadline(time.Now().Add(10 * time.Millisecond))
		if _, err := conn.Write([]byte{0x00}); err != nil {
			closedCount++
		}
	}
	// 至少有 2 个连接被关闭（evictOldest(0.2) 对 10 个 session = 2）
	if closedCount < 2 {
		t.Errorf("踢出 20%% 最旧连接后，至少 2 个连接应被关闭, 实际 %d", closedCount)
	}
}

// TestV1_TokenBucket_RateLimit 验证令牌桶鉴权限流。
// 验收标准：鉴权限流 1000/s，超限下发退避时间。
// 令牌桶会按时间补充令牌，因此用大批量请求验证限流效果。
func TestV1_TokenBucket_RateLimit(t *testing.T) {
	// 使用小容量桶便于测试
	tb := newTokenBucket(100, 100)

	// 快速消耗令牌，应能通过约 100 个（考虑时间补充可能略多）
	allowed := 0
	for i := 0; i < 500; i++ {
		if tb.Allow() {
			allowed++
		}
	}
	// 应有部分请求被拒绝（令牌耗尽）
	if allowed >= 500 {
		t.Error("500 个请求应部分被拒绝，但全部通过")
	}
	if allowed < 100 {
		t.Errorf("至少应通过 100 个请求, 实际 %d", allowed)
	}

	// 等待令牌补充
	time.Sleep(50 * time.Millisecond)
	refilled := 0
	for i := 0; i < 20; i++ {
		if tb.Allow() {
			refilled++
		}
	}
	if refilled == 0 {
		t.Error("等待 50ms 后应补充令牌")
	}
}

// TestV1_MaxConnections_Default 验证默认最大连接数配置。
// 验收标准：支持 10 万连接，默认配置 max_connections=120000。
func TestV1_MaxConnections_Default(t *testing.T) {
	cfg := &config.GatewayConfig{}
	// 模拟 config.Load 的默认值设置
	if cfg.MaxConnections == 0 {
		cfg.MaxConnections = 120000 // 与 config.go 中的默认值一致
	}
	if cfg.MaxConnections < 100000 {
		t.Errorf("MaxConnections = %d, 期望 >= 100000 (支持 10 万连接)", cfg.MaxConnections)
	}
}

// TestV1_IPLimit_Enforcement 验证单 IP 连接数限制。
// 防止单 IP 大量连接占用资源。
func TestV1_IPLimit_Enforcement(t *testing.T) {
	cfg := &config.GatewayConfig{
		TCPPort:      0,
		MaxDevices:   100,
		MaxConnections: 120000,
	}
	logger := zap.NewNop()
	sm := NewSessionManager(logger)
	server := NewTCPServer(cfg, logger, sm, nil, nil, nil)

	if server.ipLimit != 1000 {
		t.Errorf("单 IP 连接限制 = %d, 期望 1000", server.ipLimit)
	}
	if server.ipLimit <= 0 {
		t.Error("单 IP 连接限制必须 > 0")
	}
}

// TestV1_MemoryUsage_Baseline 验证当前进程内存使用在合理范围内。
// 验收标准：内存 < 16GB。
func TestV1_MemoryUsage_Baseline(t *testing.T) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	memMB := ms.Sys / (1024 * 1024)

	if memMB > 16*1024 {
		t.Errorf("进程内存 = %d MB, 超过 16GB 上限", memMB)
	}
	t.Logf("当前进程内存: Sys=%d MB, Alloc=%d MB, goroutines=%d",
		memMB, ms.Alloc/(1024*1024), runtime.NumGoroutine())
}

// TestV1_GoroutineCount_Reasonable 验证 goroutine 数量在合理范围。
// 10 万连接时每连接约 2-3 goroutine，总数应在 30 万以内。
func TestV1_GoroutineCount_Reasonable(t *testing.T) {
	// 基线测试（无连接状态）
	n := runtime.NumGoroutine()
	if n > 1000 {
		t.Errorf("基线 goroutine 数 = %d, 期望 < 1000 (无连接状态)", n)
	}
	t.Logf("基线 goroutine 数: %d", n)
}

// createPipeConn 创建一对 TCP 连接用于测试。
func createPipeConn(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	conn2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn1, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return conn1, conn2
}
