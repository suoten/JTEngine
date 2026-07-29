package jt1078

// ====================================================================
// [P1-修复] RTPConnPool LRU 淘汰活跃连接保护测试
// FIXED-2026-07-22 [P1]: 最近 10 秒内有数据传输的连接不可淘汰。
// 全部连接都活跃时返回 ErrPoolFull 而非强制淘汰。
// ====================================================================

import (
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestP1_RTPConnPool_ActiveConnNotEvicted 验证活跃连接不会被 LRU 淘汰。
// 即使连接是最久未用的（LRU 前端），只要在 10 秒内有活动就不被淘汰。
func TestP1_RTPConnPool_ActiveConnNotEvicted(t *testing.T) {
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

	// maxConns=2
	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 2, zap.NewNop())
	defer pool.Stop()

	// 创建 2 个连接（都活跃）
	if _, err := pool.GetUDP(ports[0]); err != nil {
		t.Fatalf("GetUDP port0: %v", err)
	}
	if _, err := pool.GetUDP(ports[1]); err != nil {
		t.Fatalf("GetUDP port1: %v", err)
	}

	// 尝试创建第 3 个连接——两个现有连接都活跃，应返回 ErrPoolFull
	_, err := pool.GetUDP(ports[2])
	if err == nil {
		t.Fatal("GetUDP port2 should fail with ErrPoolFull when all connections are active")
	}
	if err != ErrPoolFull {
		t.Errorf("GetUDP port2 error = %v, want ErrPoolFull", err)
	}

	// 验证两个活跃连接仍然存在
	stats := pool.Stats()
	if stats.UDPConns != 2 {
		t.Errorf("UDPConns = %d, want 2 (active connections should not be evicted)", stats.UDPConns)
	}
}

// TestP1_RTPConnPool_AgedConnEvicted 验证超过活跃阈值的连接可以被淘汰。
func TestP1_RTPConnPool_AgedConnEvicted(t *testing.T) {
	ports := make([]int, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatalf("listen udp %d: %v", i, err)
		}
		ports[i] = ln.LocalAddr().(*net.UDPAddr).Port
		defer ln.Close()
	}

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 2, zap.NewNop())
	defer pool.Stop()

	if _, err := pool.GetUDP(ports[0]); err != nil {
		t.Fatalf("GetUDP port0: %v", err)
	}
	if _, err := pool.GetUDP(ports[1]); err != nil {
		t.Fatalf("GetUDP port1: %v", err)
	}

	// 老化 port0（超过 10 秒活跃阈值）
	pool.mu.Lock()
	pool.udpLastActive[ports[0]] = time.Now().Add(-15 * time.Second)
	pool.mu.Unlock()

	// 创建 port2，应淘汰 port0（已超过活跃阈值）
	if _, err := pool.GetUDP(ports[2]); err != nil {
		t.Fatalf("GetUDP port2: %v", err)
	}

	// port0 应已被淘汰
	pool.mu.Lock()
	_, port0Exists := pool.udpConns[ports[0]]
	pool.mu.Unlock()
	if port0Exists {
		t.Error("port0 should have been evicted (aged beyond active threshold)")
	}

	// port1 和 port2 应仍然存在
	pool.mu.Lock()
	_, port1Exists := pool.udpConns[ports[1]]
	_, port2Exists := pool.udpConns[ports[2]]
	pool.mu.Unlock()
	if !port1Exists {
		t.Error("port1 should still exist (active)")
	}
	if !port2Exists {
		t.Error("port2 should exist (just created)")
	}
}

// TestP1_RTPConnPool_PartiallyActiveEvict 验证部分活跃场景：
// 池中有 1 个活跃 + 1 个非活跃连接，新连接创建时应淘汰非活跃的。
func TestP1_RTPConnPool_PartiallyActiveEvict(t *testing.T) {
	ports := make([]int, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatalf("listen udp %d: %v", i, err)
		}
		ports[i] = ln.LocalAddr().(*net.UDPAddr).Port
		defer ln.Close()
	}

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 2, zap.NewNop())
	defer pool.Stop()

	if _, err := pool.GetUDP(ports[0]); err != nil {
		t.Fatalf("GetUDP port0: %v", err)
	}
	if _, err := pool.GetUDP(ports[1]); err != nil {
		t.Fatalf("GetUDP port1: %v", err)
	}

	// 老化 port0，port1 保持活跃
	pool.mu.Lock()
	pool.udpLastActive[ports[0]] = time.Now().Add(-20 * time.Second)
	pool.mu.Unlock()

	// 创建 port2，应淘汰 port0（非活跃），保留 port1（活跃）
	if _, err := pool.GetUDP(ports[2]); err != nil {
		t.Fatalf("GetUDP port2: %v", err)
	}

	pool.mu.Lock()
	_, port0Exists := pool.udpConns[ports[0]]
	_, port1Exists := pool.udpConns[ports[1]]
	_, port2Exists := pool.udpConns[ports[2]]
	pool.mu.Unlock()

	if port0Exists {
		t.Error("port0 should have been evicted (inactive)")
	}
	if !port1Exists {
		t.Error("port1 should still exist (active)")
	}
	if !port2Exists {
		t.Error("port2 should exist (just created)")
	}
}

// TestP1_RTPConnPool_TCPActiveNotEvicted 验证 TCP 连接同样受活跃保护。
func TestP1_RTPConnPool_TCPActiveNotEvicted(t *testing.T) {
	// 启动 3 个 TCP 监听端口
	ports := make([]int, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen tcp %d: %v", i, err)
		}
		ports[i] = ln.Addr().(*net.TCPAddr).Port
		defer ln.Close()
		// 后台接收连接
		go func() {
			for {
				conn, err := ln.Accept()
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
	}

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 2, zap.NewNop())
	defer pool.Stop()

	if _, err := pool.GetTCP(ports[0]); err != nil {
		t.Fatalf("GetTCP port0: %v", err)
	}
	if _, err := pool.GetTCP(ports[1]); err != nil {
		t.Fatalf("GetTCP port1: %v", err)
	}

	// 两个 TCP 连接都活跃，创建第 3 个应返回 ErrPoolFull
	_, err := pool.GetTCP(ports[2])
	if err == nil {
		t.Fatal("GetTCP port2 should fail with ErrPoolFull when all TCP connections are active")
	}
	if err != ErrPoolFull {
		t.Errorf("GetTCP port2 error = %v, want ErrPoolFull", err)
	}
}

// TestP1_RTPConnPool_ErrPoolFullDoesNotLeakConn 验证 ErrPoolFull 时新创建的连接被正确关闭。
func TestP1_RTPConnPool_ErrPoolFullDoesNotLeakConn(t *testing.T) {
	ports := make([]int, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatalf("listen udp %d: %v", i, err)
		}
		ports[i] = ln.LocalAddr().(*net.UDPAddr).Port
		defer ln.Close()
	}

	pool := NewRTPConnPool("127.0.0.1", 5*time.Minute, 2, zap.NewNop())
	defer pool.Stop()

	if _, err := pool.GetUDP(ports[0]); err != nil {
		t.Fatalf("GetUDP port0: %v", err)
	}
	if _, err := pool.GetUDP(ports[1]); err != nil {
		t.Fatalf("GetUDP port1: %v", err)
	}

	// 尝试创建第 3 个（应失败）
	_, err := pool.GetUDP(ports[2])
	if err != ErrPoolFull {
		t.Fatalf("expected ErrPoolFull, got: %v", err)
	}

	// 池中仍应只有 2 个连接
	stats := pool.Stats()
	if stats.UDPConns != 2 {
		t.Errorf("UDPConns = %d, want 2 (failed connection should not be added)", stats.UDPConns)
	}
	// miss 计数不应增加（被回退）
	if stats.Misses != 2 {
		t.Errorf("Misses = %d, want 2 (miss count should be rolled back on ErrPoolFull)", stats.Misses)
	}
}
