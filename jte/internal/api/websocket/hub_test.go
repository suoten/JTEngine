package websocket

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestHub_Broadcast_DropsDeadClient 验证 broadcast 投递失败时清理死连接
// 修复点：原实现 RLock 迭代中途切 Lock 删 client 会触发 race / 迭代器失效；
// 新实现持写锁一次性完成投递+清理。
func TestHub_Broadcast_DropsDeadClient(t *testing.T) {
	h := NewHub(zap.NewNop())
	go h.Run()
	defer func() {
		// Hub 没有 Stop，靠 goroutine 泄漏可接受（测试进程退出即清理）
	}()

	// 注册一个 client，但 send chan 不消费 → 投递会失败
	c := newTestClient("c1", h)
	h.register <- c

	// 等待注册完成
	waitFor(t, func() bool { return h.ClientCount() == 1 }, 2*time.Second, "client not registered")

	// 订阅 topic
	h.Subscribe(c, "topic-1")

	// 发布消息：c.send 容量 256，第一条能投递成功；填满后投递失败触发清理
	// 这里直接发 300 条确保塞满
	for i := 0; i < 300; i++ {
		h.Publish("topic-1", "test", i)
	}

	// 等待 Hub 处理 broadcast 并清理死连接
	waitFor(t, func() bool { return h.ClientCount() == 0 }, 2*time.Second, "dead client not cleaned up")
}

// TestHub_Broadcast_DeliversToSubscribed 验证订阅者能收到消息
func TestHub_Broadcast_DeliversToSubscribed(t *testing.T) {
	h := NewHub(zap.NewNop())
	go h.Run()

	c := newTestClient("c1", h)
	h.register <- c
	waitFor(t, func() bool { return h.ClientCount() == 1 }, 2*time.Second, "client not registered")

	h.Subscribe(c, "topic-1")
	h.Publish("topic-1", "test", "hello")

	select {
	case msg := <-c.send:
		if msg.Topic != "topic-1" {
			t.Fatalf("expected topic-1, got %s", msg.Topic)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive message within 2s")
	}
}

// TestHub_Broadcast_ConcurrentSafe 验证并发 publish + subscribe + disconnect 不 race
func TestHub_Broadcast_ConcurrentSafe(t *testing.T) {
	h := NewHub(zap.NewNop())
	go h.Run()

	var wg sync.WaitGroup
	// 并发注册/订阅/发布
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c := newTestClient("c"+string(rune('a'+idx)), h)
			h.register <- c
			h.Subscribe(c, "topic-x")
			h.Publish("topic-x", "test", idx)
			// 消费一些消息
			time.Sleep(10 * time.Millisecond)
			h.unregister <- c
		}(i)
	}
	wg.Wait()
	// 不 panic 即通过；-race 模式下会检测数据竞争
}

// TestHub_SubscribeUnsubscribe 验证订阅/取消订阅
func TestHub_SubscribeUnsubscribe(t *testing.T) {
	h := NewHub(zap.NewNop())
	go h.Run()

	c := newTestClient("c1", h)
	h.register <- c
	waitFor(t, func() bool { return h.ClientCount() == 1 }, 2*time.Second, "client not registered")

	h.Subscribe(c, "topic-1")
	h.Publish("topic-1", "test", "msg1")
	select {
	case <-c.send:
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive msg1 after subscribe")
	}

	h.Unsubscribe(c, "topic-1")
	h.Publish("topic-1", "test", "msg2")
	select {
	case <-c.send:
		t.Fatal("should not receive msg2 after unsubscribe")
	case <-time.After(200 * time.Millisecond):
		// 预期：不收到
	}
}

// newTestClient 创建测试用 client（send chan 容量 256，不启动 WritePump）
func newTestClient(id string, h *Hub) *Client {
	return &Client{
		id:     id,
		hub:    h,
		send:   make(chan *Message, 256),
		topics: make(map[string]bool),
	}
}

// waitFor 轮询条件直到满足或超时
func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
