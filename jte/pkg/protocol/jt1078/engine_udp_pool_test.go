package jt1078

// AUTO-FIX-2026-07-02 [P1]: RTP 长连接池测试
//
// 原 engine_udp_pool_test 测试 engine 内联 udpPool 字段，重构为 RTPConnPool 后
// 改为直接测试 RTPConnPool 结构体（idle sweep / 复用 / 活跃不被扫除 / LRU 上限 / 复用率）。

import (
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRTPConnPool_UDPSweepIdle 验证 UDP 长连接空闲超时关闭。
func TestRTPConnPool_UDPSweepIdle(t *testing.T) {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	port := udpListener.LocalAddr().(*net.UDPAddr).Port

	pool := NewRTPConnPool("127.0.0.1", 50*time.Millisecond, 0, zap.NewNop())
	defer pool.Stop()

	conn, err := pool.GetUDP(port)
	if err != nil {
		t.Fatalf("GetUDP: %v", err)
	}

	// 确认连接已入池
	if stats := pool.Stats(); stats.UDPConns != 1 {
		t.Fatalf("UDPConns = %d, want 1", stats.UDPConns)
	}

	// 人为老化 lastActive 触发空闲扫描
	pool.mu.Lock()
	pool.udpLastActive[port] = time.Now().Add(-1 * time.Hour)
	pool.mu.Unlock()

	pool.sweepIdle()

	if stats := pool.Stats(); stats.UDPConns != 0 {
		t.Errorf("after sweep: UDPConns = %d, want 0", stats.UDPConns)
	}
	if stats := pool.Stats(); stats.IdleCloses != 1 {
		t.Errorf("IdleCloses = %d, want 1", stats.IdleCloses)
	}

	// 向已关闭的 conn 写入应失败
	if _, err := conn.Write([]byte("test")); err == nil {
		t.Error("write to closed udp conn should fail")
	}
}

// TestRTPConnPool_UDPReuse 验证同端口复用同一连接（hits 递增）。
func TestRTPConnPool_UDPReuse(t *testing.T) {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	port := udpListener.LocalAddr().(*net.UDPAddr).Port

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 0, zap.NewNop())
	defer pool.Stop()

	conn1, err := pool.GetUDP(port)
	if err != nil {
		t.Fatalf("GetUDP first: %v", err)
	}
	conn2, err := pool.GetUDP(port)
	if err != nil {
		t.Fatalf("GetUDP second: %v", err)
	}
	if conn1 != conn2 {
		t.Error("GetUDP should return the same pooled connection")
	}
	if stats := pool.Stats(); stats.Hits != 1 || stats.Misses != 1 {
		t.Errorf("Hits=%d Misses=%d, want Hits=1 Misses=1", stats.Hits, stats.Misses)
	}
}

// TestRTPConnPool_ActiveNotSwept 验证活跃连接不会被空闲扫描关闭。
func TestRTPConnPool_ActiveNotSwept(t *testing.T) {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	port := udpListener.LocalAddr().(*net.UDPAddr).Port

	pool := NewRTPConnPool("127.0.0.1", 50*time.Millisecond, 0, zap.NewNop())
	defer pool.Stop()

	if _, err := pool.GetUDP(port); err != nil {
		t.Fatalf("GetUDP: %v", err)
	}
	pool.sweepIdle()
	if stats := pool.Stats(); stats.UDPConns != 1 {
		t.Errorf("active conn swept: UDPConns = %d, want 1", stats.UDPConns)
	}
}

// TestRTPConnPool_LRUEvict 验证达到上限时按 LRU 淘汰最久未用连接。
func TestRTPConnPool_LRUEvict(t *testing.T) {
	// 启动 3 个 UDP 监听端口
	ports := make([]int, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatalf("listen udp %d: %v", i, err)
		}
		ports[i] = ln.LocalAddr().(*net.UDPAddr).Port
		defer ln.Close()
	}

	// maxConns=2，创建第 3 个时应淘汰第 1 个（最久未用）
	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 2, zap.NewNop())
	defer pool.Stop()

	if _, err := pool.GetUDP(ports[0]); err != nil {
		t.Fatalf("GetUDP port0: %v", err)
	}
	if _, err := pool.GetUDP(ports[1]); err != nil {
		t.Fatalf("GetUDP port1: %v", err)
	}
	// 访问 port1 使 port0 成为最久未用
	if _, err := pool.GetUDP(ports[1]); err != nil {
		t.Fatalf("GetUDP port1 again: %v", err)
	}
	// 创建 port2，应淘汰 port0
	if _, err := pool.GetUDP(ports[2]); err != nil {
		t.Fatalf("GetUDP port2: %v", err)
	}

	stats := pool.Stats()
	if stats.Evicts < 1 {
		t.Errorf("Evicts = %d, want >=1", stats.Evicts)
	}
	if stats.UDPConns > 2 {
		t.Errorf("UDPConns = %d, want <= 2 (maxConns)", stats.UDPConns)
	}
	// port0 应已被淘汰
	pool.mu.Lock()
	_, port0Exists := pool.udpConns[ports[0]]
	pool.mu.Unlock()
	if port0Exists {
		t.Error("port0 should have been LRU-evicted")
	}
}

// TestRTPConnPool_ReuseRate 验证复用率计算。
func TestRTPConnPool_ReuseRate(t *testing.T) {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	port := udpListener.LocalAddr().(*net.UDPAddr).Port

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 0, zap.NewNop())
	defer pool.Stop()

	// 1 次 miss + 9 次 hit
	for i := 0; i < 10; i++ {
		if _, err := pool.GetUDP(port); err != nil {
			t.Fatalf("GetUDP %d: %v", i, err)
		}
	}
	if rate := pool.ReuseRate(); rate < 0.89 || rate > 0.91 {
		t.Errorf("ReuseRate = %.3f, want ~0.90", rate)
	}
}

// TestRTPConnPool_TCPReuse 验证 TCP 连接复用。
func TestRTPConnPool_TCPReuse(t *testing.T) {
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer tcpLn.Close()
	port := tcpLn.Addr().(*net.TCPAddr).Port

	// 后台接收 TCP 连接
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

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 0, zap.NewNop())
	defer pool.Stop()

	c1, err := pool.GetTCP(port)
	if err != nil {
		t.Fatalf("GetTCP first: %v", err)
	}
	c2, err := pool.GetTCP(port)
	if err != nil {
		t.Fatalf("GetTCP second: %v", err)
	}
	if c1 != c2 {
		t.Error("GetTCP should return the same pooled connection")
	}
}

// TestRTPConnPool_Invalidate 验证失效连接后下次 Get 重建。
func TestRTPConnPool_Invalidate(t *testing.T) {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	port := udpListener.LocalAddr().(*net.UDPAddr).Port

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 0, zap.NewNop())
	defer pool.Stop()

	c1, _ := pool.GetUDP(port)
	pool.InvalidateUDP(port)
	c2, _ := pool.GetUDP(port)
	if c1 == c2 {
		t.Error("GetUDP after Invalidate should return a new connection")
	}
	if stats := pool.Stats(); stats.Misses != 2 {
		t.Errorf("Misses = %d, want 2", stats.Misses)
	}
}

// TestEngine_DelegatesToPool 验证 VideoEngine.getUDPConn 委托给 RTPConnPool。
func TestEngine_DelegatesToPool(t *testing.T) {
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpListener.Close()
	port := udpListener.LocalAddr().(*net.UDPAddr).Port

	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	c1, err := eng.getUDPConn(port)
	if err != nil {
		t.Fatalf("getUDPConn: %v", err)
	}
	c2, err := eng.getUDPConn(port)
	if err != nil {
		t.Fatalf("getUDPConn second: %v", err)
	}
	if c1 != c2 {
		t.Error("engine.getUDPConn should reuse pooled connection")
	}
	// 底层池应记录 1 miss + 1 hit
	if pool := eng.GetRTPConnPool(); pool != nil {
		if stats := pool.Stats(); stats.Hits != 1 || stats.Misses != 1 {
			t.Errorf("pool Stats Hits=%d Misses=%d, want 1/1", stats.Hits, stats.Misses)
		}
	}
}
