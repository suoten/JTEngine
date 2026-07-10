package websocket

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-29 [P1]: 测试 Hub.Stop() 的优雅停机能力。
// 覆盖：Run() 退出、幂等性、client.send 关闭、clients 清空。
// 注意：newTestClient / waitFor 复用 hub_test.go 中已有的定义，不重复声明。

// TestHub_Stop_ExitsRun 验证 Stop() 后 Run() goroutine 正常退出。
func TestHub_Stop_ExitsRun(t *testing.T) {
	hub := NewHub(zap.NewNop())
	done := make(chan struct{})
	go func() {
		hub.Run()
		close(done)
	}()

	// 通过 register 一个 client 确认 Run() 已进入 select 循环
	client := newTestClient("c1", hub)
	select {
	case hub.register <- client:
		// Run() 已就绪并处理了 register
	case <-time.After(1 * time.Second):
		t.Fatal("Hub.Run() did not start within 1s")
	}

	hub.Stop()

	select {
	case <-done:
		// 成功：Run() 已退出
	case <-time.After(2 * time.Second):
		t.Fatal("Hub.Run() did not exit after Stop()")
	}
}

// TestHub_Stop_Idempotent 验证多次调用 Stop() 不 panic（sync.Once 保护）。
func TestHub_Stop_Idempotent(t *testing.T) {
	hub := NewHub(zap.NewNop())
	go hub.Run()

	// 等待 Run() 就绪
	client := newTestClient("c1", hub)
	select {
	case hub.register <- client:
	case <-time.After(1 * time.Second):
		t.Fatal("Hub.Run() did not start within 1s")
	}

	hub.Stop()
	hub.Stop() // 不应 panic
	hub.Stop() // 不应 panic
}

// TestHub_Stop_ClosesClientSend 验证 Stop() 后所有 client.send 被关闭。
func TestHub_Stop_ClosesClientSend(t *testing.T) {
	hub := NewHub(zap.NewNop())
	go hub.Run()

	// 注册 3 个 client
	clients := make([]*Client, 3)
	for i := range clients {
		clients[i] = newTestClient(string(rune('a'+i)), hub)
		select {
		case hub.register <- clients[i]:
		case <-time.After(1 * time.Second):
			t.Fatalf("register client %d timed out", i)
		}
	}

	hub.Stop()

	// 等待 Stop 处理完成并验证 client.send 被关闭
	for _, c := range clients {
		if !waitForSendClosed(c, 2*time.Second) {
			t.Errorf("client %s send channel not closed after Stop", c.id)
		}
	}
}

// TestHub_Stop_ClearsClients 验证 Stop() 后 ClientCount() == 0。
func TestHub_Stop_ClearsClients(t *testing.T) {
	hub := NewHub(zap.NewNop())
	go hub.Run()

	client := newTestClient("c1", hub)
	select {
	case hub.register <- client:
	case <-time.After(1 * time.Second):
		t.Fatal("register client timed out")
	}

	if hub.ClientCount() != 1 {
		t.Fatalf("ClientCount before Stop = %d, want 1", hub.ClientCount())
	}

	hub.Stop()

	// 等待 Stop 处理完成
	waitFor(t, func() bool { return hub.ClientCount() == 0 }, 2*time.Second, "ClientCount not 0 after Stop")
}

// TestHub_Stop_PublishAfterStop 验证 Stop() 后 Publish 不会 panic（channel 仍在，只是无人消费）。
// 注意：Publish 会阻塞如果 broadcast channel 满（256 缓冲），但单次调用不会。
func TestHub_Stop_PublishAfterStop(t *testing.T) {
	hub := NewHub(zap.NewNop())
	go hub.Run()

	client := newTestClient("c1", hub)
	select {
	case hub.register <- client:
	case <-time.After(1 * time.Second):
		t.Fatal("register client timed out")
	}

	hub.Stop()

	// Stop 后 Publish 不应 panic（broadcast channel 有 256 缓冲）
	hub.Publish("test", "event", nil)
}

// waitForSendClosed 轮询检查 client.send 是否已关闭，返回 true 表示已关闭。
func waitForSendClosed(c *Client, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case _, ok := <-c.send:
			if !ok {
				return true // 已关闭
			}
			// 读到一条消息，继续检查
		default:
			// 无消息且未关闭，继续等
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
