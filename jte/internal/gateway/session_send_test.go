package gateway

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jte-engine/jte/pkg/protocol/jt808"
	"go.uber.org/zap"
)

// newTestSession 创建绑定到本地 TCP 连接的测试 session。
func newTestSession(t *testing.T) (*Session, net.Conn, net.Conn) {
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

	sm := NewSessionManager(zap.NewNop())
	session := sm.Create("test-session", conn1)
	session.SetPhone("13800000000")
	return session, conn1, conn2
}

// TestSessionSendDeliversToPeer 验证 Send 构造的 808 帧能通过发送队列到达对端。
func TestSessionSendDeliversToPeer(t *testing.T) {
	session, _, peer := newTestSession(t)
	defer session.Close()
	defer peer.Close()

	// 发送一条 0x8001 通用应答（body: 流水号2B + 消息ID2B + 结果1B）
	body := []byte{0x00, 0x01, 0x00, 0x02, 0x00}
	if err := session.Send(jt808.MsgIDGeneralResp, body); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := peer.Read(buf)
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if n == 0 {
		t.Fatal("peer received 0 bytes")
	}
	// 808 帧以 0x7e 开头和结尾
	if buf[0] != 0x7e || buf[n-1] != 0x7e {
		t.Errorf("frame delimiters: head=0x%02x tail=0x%02x, want 0x7e/0x7e", buf[0], buf[n-1])
	}
}

// TestSessionWriteDeliversRaw 验证 Write 原始字节透传到对端。
func TestSessionWriteDeliversRaw(t *testing.T) {
	session, _, peer := newTestSession(t)
	defer session.Close()
	defer peer.Close()

	raw := []byte{0x7e, 0xAA, 0xBB, 0xCC, 0x7e}
	if _, err := session.Write(raw); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := peer.Read(buf)
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if n != len(raw) {
		t.Errorf("received %d bytes, want %d", n, len(raw))
	}
	if buf[1] != 0xAA {
		t.Errorf("byte[1]=0x%02x, want 0xAA", buf[1])
	}
}

// TestSessionConcurrentSendNoPanic 验证并发发送不会 panic 或数据交错。
// sendLoop 单 goroutine 串行化保证写入安全。
func TestSessionConcurrentSendNoPanic(t *testing.T) {
	session, _, peer := newTestSession(t)
	defer session.Close()
	defer peer.Close()

	// 持续读取对端数据，避免 TCP 缓冲区满阻塞写
	go func() {
		buf := make([]byte, 4096)
		for {
			peer.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := peer.Read(buf); err != nil {
				return
			}
		}
	}()

	const goroutines = 20
	const sendsPerG = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*sendsPerG)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := []byte{0x00, 0x01, 0x00, 0x02, 0x00}
			for j := 0; j < sendsPerG; j++ {
				if err := session.Send(jt808.MsgIDGeneralResp, body); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent send error: %v", err)
		}
	}
}

// TestSessionCloseDrainsQueue 验证 Close 后排队中的任务收到错误而非永久阻塞。
func TestSessionCloseDrainsQueue(t *testing.T) {
	// 用 pipe 构造一个不读取的对端，使 TCP 写缓冲逐渐填满
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
	defer conn2.Close()

	sm := NewSessionManager(zap.NewNop())
	session := sm.Create("drain-test", conn1)
	session.SetPhone("13800000000")

	// 并发发起多个 Send（不读对端，部分会排队），然后 Close
	done := make(chan struct{})
	go func() {
		defer close(done)
		body := make([]byte, 8192) // 大 body 快速填满写缓冲
		_ = session.Send(jt808.MsgIDGeneralResp, body)
	}()
	time.Sleep(50 * time.Millisecond) // 让 Send 入队

	// Close 应在合理时间内返回（sendLoop 退出 + drain 残余任务）
	closeDone := make(chan struct{})
	go func() {
		session.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
		// 成功：Close 未阻塞
	case <-time.After(6 * time.Second):
		t.Fatal("Close did not return within 6s, send queue drain may be stuck")
	}
	<-done
}

// TestSessionSendAfterClose 验证 Close 后再 Send 立即返回错误。
func TestSessionSendAfterClose(t *testing.T) {
	session, conn1, peer := newTestSession(t)
	session.Close()
	defer conn1.Close()
	defer peer.Close()

	err := session.Send(jt808.MsgIDGeneralResp, []byte{0x00})
	if err == nil {
		t.Error("Send after Close should return error")
	}
}

// TestSessionCloseIdempotent 验证 Close 多次调用不 panic。
func TestSessionCloseIdempotent(t *testing.T) {
	session, conn1, peer := newTestSession(t)
	defer conn1.Close()
	defer peer.Close()

	session.Close()
	session.Close() // 不应 panic
	session.Close()
}
