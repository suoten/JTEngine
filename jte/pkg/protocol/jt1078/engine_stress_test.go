package jt1078

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// AUTO-FIX-2026-07-02 [P1]: 1000 并发 RTP 转发压力测试（复用率 > 90%）
// ===================================================================
//
// 验收标准（用户要求）：
//   - 压力测试：1000 并发 RTP 转发，连接复用率 > 90%
//
// 设计：
//   1. 启动 N 个 UDP 监听器（模拟 ZLMediaKit openRtpServer 端口）
//   2. 创建 VideoEngine，注册 N 个 streamID → N 个端口
//   3. 1000 个 goroutine 并发调用 ForwardRTP（分布在 N 个 streamID 上）
//   4. 统计 RTPConnPool 的 ReuseRate，验证 > 0.90
//   5. 验证无 panic、无 deadlock、全部发送成功

// TestRTPConnPool_Stress1000Concurrent 验证 1000 并发 RTP 转发复用率 > 90%。
// 使用 10 个 UDP 端口（模拟 10 路 ZLMediaKit 流），每路 100 并发 = 1000 总并发。
// 预期：10 misses（每端口首次创建）+ 990 hits（复用）→ 复用率 99%。
func TestRTPConnPool_Stress1000Concurrent(t *testing.T) {
	const (
		numPorts      = 10  // 10 个 UDP 端口
		connsPerPort  = 100 // 每端口 100 并发
		totalConns    = numPorts * connsPerPort
		reuseThreshold = 0.90 // 验收标准：复用率 > 90%
	)

	// 1) 启动 10 个 UDP 监听器（模拟 ZLMediaKit 接收 RTP）
	udpPorts := make([]int, numPorts)
	udpListeners := make([]*net.UDPConn, numPorts)
	for i := 0; i < numPorts; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatalf("listen udp %d: %v", i, err)
		}
		udpPorts[i] = ln.LocalAddr().(*net.UDPAddr).Port
		udpListeners[i] = ln
	}
	defer func() {
		for _, ln := range udpListeners {
			ln.Close()
		}
	}()

	// 后台持续读取 UDP 数据（防止发送缓冲区满）
	for _, ln := range udpListeners {
		go func(c *net.UDPConn) {
			buf := make([]byte, 4096)
			for {
				if _, _, err := c.ReadFromUDP(buf); err != nil {
					return
				}
			}
		}(ln)
	}

	// 2) 创建 VideoEngine
	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	// 3) 注册 10 个 streamID → 10 个端口，模式设为 "udp"（不走 auto fallback）
	for i := 0; i < numPorts; i++ {
		streamID := stressStreamID(i)
		eng.RegisterStreamPort(streamID, udpPorts[i])
		eng.SetStreamMode(streamID, "udp")
	}

	// 4) 1000 个 goroutine 并发调用 ForwardRTP
	var wg sync.WaitGroup
	var failures int64
	rtpData := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01}

	wg.Add(totalConns)
	start := make(chan struct{})
	for i := 0; i < totalConns; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start // 同时开始，模拟高并发
			streamID := stressStreamID(idx % numPorts)
			if err := eng.ForwardRTP(streamID, rtpData); err != nil {
				atomic.AddInt64(&failures, 1)
			}
		}(i)
	}
	close(start)

	// 5) 等待所有 goroutine 完成（超时 30s）
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stress test timed out after 30s (possible deadlock)")
	}

	// 6) 验证全部成功
	if failures > 0 {
		t.Errorf("%d/%d ForwardRTP calls failed", failures, totalConns)
	}

	// 7) 验证复用率 > 90%
	pool := eng.GetRTPConnPool()
	stats := pool.Stats()
	reuseRate := stats.ReuseRate
	t.Logf("stress test stats: hits=%d misses=%d evicts=%d reuse_rate=%.4f udp_conns=%d",
		stats.Hits, stats.Misses, stats.Evicts, reuseRate, stats.UDPConns)

	if reuseRate <= reuseThreshold {
		t.Errorf("reuse rate = %.4f, want > %.2f (hits=%d misses=%d)",
			reuseRate, reuseThreshold, stats.Hits, stats.Misses)
	}
}

// TestRTPConnPool_StressSinglePort1000 验证单端口 1000 并发复用率 > 99%。
// 极端场景：所有 1000 个转发指向同一端口（1 个 ZLMediaKit 流）。
// 预期：1 miss + 999 hits → 复用率 99.9%。
func TestRTPConnPool_StressSinglePort1000(t *testing.T) {
	const totalConns = 1000

	// 启动 1 个 UDP 监听器
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer ln.Close()
	udpPort := ln.LocalAddr().(*net.UDPAddr).Port

	// 后台读取
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, _, err := ln.ReadFromUDP(buf); err != nil {
				return
			}
		}
	}()

	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	streamID := "stress_single_ch1"
	eng.RegisterStreamPort(streamID, udpPort)
	eng.SetStreamMode(streamID, "udp")

	var wg sync.WaitGroup
	var failures int64
	rtpData := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01}

	wg.Add(totalConns)
	start := make(chan struct{})
	for i := 0; i < totalConns; i++ {
		go func() {
			defer wg.Done()
			<-start
			if err := eng.ForwardRTP(streamID, rtpData); err != nil {
				atomic.AddInt64(&failures, 1)
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stress test timed out after 30s")
	}

	if failures > 0 {
		t.Errorf("%d/%d ForwardRTP calls failed", failures, totalConns)
	}

	pool := eng.GetRTPConnPool()
	stats := pool.Stats()
	t.Logf("single-port stats: hits=%d misses=%d reuse_rate=%.4f",
		stats.Hits, stats.Misses, stats.ReuseRate)

	// 单端口：1 miss + 999 hits → 99.9%
	if stats.ReuseRate <= 0.99 {
		t.Errorf("reuse rate = %.4f, want > 0.99 (hits=%d misses=%d)",
			stats.ReuseRate, stats.Hits, stats.Misses)
	}
}

// TestRTPConnPool_ConcurrentSafe 验证高并发下连接池无 panic、无数据竞争。
// 使用 race detector 运行：go test -race -run TestRTPConnPool_ConcurrentSafe
func TestRTPConnPool_ConcurrentSafe(t *testing.T) {
	const (
		numPorts     = 20
		opsPerPort   = 50
		totalOps     = numPorts * opsPerPort
	)

	udpPorts := make([]int, numPorts)
	udpListeners := make([]*net.UDPConn, numPorts)
	for i := 0; i < numPorts; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatalf("listen udp %d: %v", i, err)
		}
		udpPorts[i] = ln.LocalAddr().(*net.UDPAddr).Port
		udpListeners[i] = ln
	}
	defer func() {
		for _, ln := range udpListeners {
			ln.Close()
		}
	}()

	for _, ln := range udpListeners {
		go func(c *net.UDPConn) {
			buf := make([]byte, 4096)
			for {
				if _, _, err := c.ReadFromUDP(buf); err != nil {
					return
				}
			}
		}(ln)
	}

	eng := NewVideoEngine(zap.NewNop(), "127.0.0.1")
	defer eng.Stop()

	for i := 0; i < numPorts; i++ {
		streamID := stressStreamID(i)
		eng.RegisterStreamPort(streamID, udpPorts[i])
		eng.SetStreamMode(streamID, "udp")
	}

	// 并发 ForwardRTP + 并发 Stats 查询（验证读写并发安全）
	var wg sync.WaitGroup
	rtpData := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01}

	wg.Add(totalOps + 10)
	start := make(chan struct{})

	// totalOps 个 ForwardRTP
	for i := 0; i < totalOps; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			streamID := stressStreamID(idx % numPorts)
			_ = eng.ForwardRTP(streamID, rtpData)
		}(i)
	}

	// 10 个并发 Stats 查询
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				_ = eng.GetRTPConnPool().Stats()
				_ = eng.GetRTPConnPool().ReuseRate()
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent safety test timed out")
	}

	// 若到达此处无 panic，则并发安全验证通过
	t.Log("concurrent safety test passed (no panic, no deadlock)")
}

// stressStreamID 生成压力测试用 streamID。
func stressStreamID(idx int) string {
	return "stress" + string([]byte{byte('0' + idx/10), byte('0' + idx%10)}) + "_ch1"
}
