package websocket

import (
	"sync"

	"go.uber.org/zap"
)

type Hub struct {
	mu        sync.RWMutex
	clients   map[*Client]bool
	topics    map[string]map[*Client]bool
	logger    *zap.Logger
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Message
	// AUTO-FIX-2026-06-29 [P1]: 原 Run() 无退出机制，goroutine 永久泄漏。
	// stopCh 关闭后 Run() 退出，并统一关闭所有 client.send 通知它们停止。
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewHub(logger *zap.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		topics:     make(map[string]map[*Client]bool),
		logger:     logger,
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *Message, 256),
		stopCh:     make(chan struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case <-h.stopCh:
			// 停机：统一关闭所有 client 的 send 通道，通知写入 goroutine 退出。
			// 持写锁避免与 broadcast/unregister 路径并发修改 clients。
			// client.send 可能已被 unregister 路径关闭，safeCloseSend 用 recover 忽略重复 close panic。
			h.mu.Lock()
			for client := range h.clients {
				safeCloseSend(client)
				delete(h.clients, client)
			}
			h.topics = make(map[string]map[*Client]bool)
			h.mu.Unlock()
			h.logger.Info("websocket hub stopped")
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			h.logger.Debug("websocket client connected", zap.String("id", client.id))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				for topic := range client.topics {
					if subs, ok := h.topics[topic]; ok {
						delete(subs, client)
					}
				}
				safeCloseSend(client)
			}
			h.mu.Unlock()
			h.logger.Debug("websocket client disconnected", zap.String("id", client.id))

		case msg := <-h.broadcast:
			// AUTO-FIX-2026-06-29: 原实现 RLock 迭代中途切换到 Lock 删 client 再切回 RLock，
			// map 迭代器在锁切换间被并发修改会触发 race / 迭代器失效。
			// 修复：整个 broadcast 路径持写锁一次性完成"投递+清理"。
			h.mu.Lock()
			var deadClients []*Client
			if subs, ok := h.topics[msg.Topic]; ok {
				for client := range subs {
					select {
					case client.send <- msg:
					default:
						// 投递失败：标记为死连接，循环结束后统一清理
						deadClients = append(deadClients, client)
					}
				}
				// 统一清理投递失败的 client（不在迭代中途删 map）
				for _, client := range deadClients {
					delete(h.clients, client)
					delete(subs, client)
					// 同时从该 client 订阅的其它 topic 中移除，避免残留引用
					for topic := range client.topics {
						if otherSubs, ok := h.topics[topic]; ok {
							delete(otherSubs, client)
						}
					}
					safeCloseSend(client)
				}
			}
			h.mu.Unlock()
		}
	}
}

// Stop 关闭 stopCh 通知 Run() 退出，并清理所有 client 连接。
// 使用 sync.Once 保证多次调用安全（停机流程可能被 GracefulShutdown 和 Stop 重复触发）。
func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stopCh)
	})
}

// safeCloseSend 安全关闭 client.send 通道，忽略重复关闭的 panic。
// client.send 可能在 unregister 和 stopCh 两条路径都被关闭，recover 防止 panic。
func safeCloseSend(client *Client) {
	defer func() { recover() }()
	close(client.send)
}

func (h *Hub) Subscribe(client *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	client.topics[topic] = true
	if _, ok := h.topics[topic]; !ok {
		h.topics[topic] = make(map[*Client]bool)
	}
	h.topics[topic][client] = true
}

func (h *Hub) Unsubscribe(client *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(client.topics, topic)
	if subs, ok := h.topics[topic]; ok {
		delete(subs, client)
	}
}

func (h *Hub) Publish(topic string, msgType string, data interface{}) {
	h.broadcast <- &Message{
		Topic: topic,
		Type:  msgType,
		Data:  data,
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
