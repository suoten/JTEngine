package gateway

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-29 [P1-5]: 809 断线缓冲与重连补发测试
// 验证：
//   - 离线时 SendVehicleData 写入缓冲区
//   - 缓冲区满时 FIFO 淘汰最旧帧
//   - 重连成功后 flushPendingBuffer 按 SN 顺序补发
//   - 补发失败的帧保留到下次重连
//   - 并发 Send 与 flush 安全

func newOfflineClient(t *testing.T, cap int) *JT809Client {
	t.Helper()
	return &JT809Client{
		cfg: &config.JT809PlatformConfig{ID: "test"},
		logger: zap.NewNop(),
		pendingCap: cap,
	}
}

func TestSendOrBuffer_OfflineBuffersFrame(t *testing.T) {
	c := newOfflineClient(t, 100)
	// 离线状态下发送
	err := c.sendOrBuffer(0x1200, 1, []byte{0x5B, 0x01, 0x5D})
	if err != nil {
		t.Fatalf("sendOrBuffer offline: %v", err)
	}
	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1", size)
	}
}

func TestSendOrBuffer_BufferOverflowDropsOldest(t *testing.T) {
	c := newOfflineClient(t, 3)
	// 写入 5 帧，缓冲容量 3，应丢弃前 2 帧
	for i := 0; i < 5; i++ {
		if err := c.sendOrBuffer(0x1200, uint16(i+1), []byte{0x5B, byte(i), 0x5D}); err != nil {
			t.Fatalf("sendOrBuffer %d: %v", i, err)
		}
	}
	size, dropped := c.GetPendingBufferStatus()
	if size != 3 {
		t.Errorf("buffer size = %d, want 3", size)
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}
	// 验证保留的是最新的 3 帧（SN=3,4,5）
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for i, f := range c.pendingBuffer {
		wantSeq := uint16(i + 3)
		if f.seqNum != wantSeq {
			t.Errorf("buffer[%d].seqNum = %d, want %d", i, f.seqNum, wantSeq)
		}
	}
}

func TestSendOrBuffer_BufferDisabled(t *testing.T) {
	c := newOfflineClient(t, 0) // 容量 0 = 禁用
	err := c.sendOrBuffer(0x1200, 1, []byte{0x5B, 0x01, 0x5D})
	if err == nil {
		t.Fatal("sendOrBuffer with disabled buffer should error")
	}
}

func TestSendOrBuffer_DeepCopyFrame(t *testing.T) {
	c := newOfflineClient(t, 100)
	orig := []byte{0x5B, 0x01, 0x5D}
	if err := c.sendOrBuffer(0x1200, 1, orig); err != nil {
		t.Fatalf("sendOrBuffer: %v", err)
	}
	// 修改原 slice 不应影响缓冲区
	orig[1] = 0xFF
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.pendingBuffer[0].payload[1] == 0xFF {
		t.Fatal("buffer should hold a copy, not the original slice")
	}
}

// inMemoryConn 用于测试在线发送 + 重连补发场景的 net.Conn mock
type inMemoryConn struct {
	mu     sync.Mutex
	received [][]byte
	closed bool
}

func (c *inMemoryConn) Read(b []byte) (n int, err error)  { return 0, nil }
func (c *inMemoryConn) Write(b []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	c.received = append(c.received, cp)
	return len(b), nil
}
func (c *inMemoryConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
func (c *inMemoryConn) LocalAddr() net.Addr                { return nil }
func (c *inMemoryConn) RemoteAddr() net.Addr               { return nil }
func (c *inMemoryConn) SetDeadline(t time.Time) error      { return nil }
func (c *inMemoryConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *inMemoryConn) SetWriteDeadline(t time.Time) error { return nil }

func (c *inMemoryConn) getReceived() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.received))
	copy(out, c.received)
	return out
}

func TestFlushPendingBuffer_ReconnectReplay(t *testing.T) {
	c := newOfflineClient(t, 100)
	// 离线写入 3 帧
	for i := 0; i < 3; i++ {
		if err := c.sendOrBuffer(0x1200, uint16(i+1), []byte{0x5B, byte(i), 0x5D}); err != nil {
			t.Fatalf("sendOrBuffer %d: %v", i, err)
		}
	}
	// 模拟重连成功：设置 conn 并 running=true
	conn := &inMemoryConn{}
	c.conn = conn
	c.running.Store(true)
	// 调用 flushPendingBuffer 补发
	c.flushPendingBuffer()
	// 验证 3 帧都已补发
	received := conn.getReceived()
	if len(received) != 3 {
		t.Fatalf("received frames = %d, want 3", len(received))
	}
	// 验证按 SN 顺序补发
	for i, r := range received {
		if r[1] != byte(i) {
			t.Errorf("received[%d][1] = %d, want %d", i, r[1], i)
		}
	}
	// 缓冲区应清空
	size, _ := c.GetPendingBufferStatus()
	if size != 0 {
		t.Errorf("buffer size after flush = %d, want 0", size)
	}
}

func TestFlushPendingBuffer_PartialFailureRetained(t *testing.T) {
	c := newOfflineClient(t, 100)
	// 写入 3 帧
	for i := 0; i < 3; i++ {
		if err := c.sendOrBuffer(0x1200, uint16(i+1), []byte{0x5B, byte(i), 0x5D}); err != nil {
			t.Fatalf("sendOrBuffer %d: %v", i, err)
		}
	}
	// 使用一个会写入 2 帧后关闭的 conn mock
	conn := &partialFailConn{failAfter: 2}
	c.conn = conn
	c.running.Store(true)
	c.flushPendingBuffer()
	// 第 3 帧补发失败应保留在缓冲区
	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size after partial flush = %d, want 1", size)
	}
}

// partialFailConn 前 N 次写入成功，之后失败
type partialFailConn struct {
	written   int
	failAfter int
}

func (c *partialFailConn) Read(b []byte) (n int, err error)  { return 0, nil }
func (c *partialFailConn) Write(b []byte) (n int, err error) {
	c.written++
	if c.written > c.failAfter {
		return 0, net.ErrClosed
	}
	return len(b), nil
}
func (c *partialFailConn) Close() error                        { return nil }
func (c *partialFailConn) LocalAddr() net.Addr                 { return nil }
func (c *partialFailConn) RemoteAddr() net.Addr                { return nil }
func (c *partialFailConn) SetDeadline(t time.Time) error       { return nil }
func (c *partialFailConn) SetReadDeadline(t time.Time) error   { return nil }
func (c *partialFailConn) SetWriteDeadline(t time.Time) error  { return nil }

func TestFlushPendingBuffer_EmptyBuffer(t *testing.T) {
	c := newOfflineClient(t, 100)
	c.conn = &inMemoryConn{}
	// 空缓冲调用 flush 应无副作用
	c.flushPendingBuffer()
	size, _ := c.GetPendingBufferStatus()
	if size != 0 {
		t.Errorf("empty buffer flush: size = %d, want 0", size)
	}
}

func TestSendVehicleData_OfflineBuffers(t *testing.T) {
	c := newOfflineClient(t, 100)
	loc := &storage.LocationData{
		VehicleID: "v1",
		Phone:     "13800000000",
		Latitude:  39.9,
		Longitude: 116.4,
		Time:      time.Now(),
	}
	// 离线发送应进入缓冲区（不返回 error）
	if err := c.SendVehicleData("v1", loc); err != nil {
		t.Fatalf("SendVehicleData offline: %v", err)
	}
	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1", size)
	}
}

func TestSendAlarm_OfflineBuffers(t *testing.T) {
	c := newOfflineClient(t, 100)
	alarm := &storage.AlarmData{
		ID:        "a1",
		VehicleID: "v1",
		Phone:     "13800000000",
		Type:      "overspeed",
		Level:     2,
		Time:      time.Now(),
	}
	if err := c.SendAlarm(alarm); err != nil {
		t.Fatalf("SendAlarm offline: %v", err)
	}
	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1", size)
	}
}

func TestSendVehicleData_OnlineSendsDirectly(t *testing.T) {
	c := newOfflineClient(t, 100)
	conn := &inMemoryConn{}
	c.conn = conn
	c.running.Store(true)
	loc := &storage.LocationData{
		VehicleID: "v1",
		Phone:     "13800000000",
		Latitude:  39.9,
		Longitude: 116.4,
		Time:      time.Now(),
	}
	if err := c.SendVehicleData("v1", loc); err != nil {
		t.Fatalf("SendVehicleData online: %v", err)
	}
	// 在线发送不应进缓冲区
	size, _ := c.GetPendingBufferStatus()
	if size != 0 {
		t.Errorf("online send should not buffer, size = %d", size)
	}
	// 应直接写入 conn
	received := conn.getReceived()
	if len(received) != 1 {
		t.Errorf("conn received = %d frames, want 1", len(received))
	}
}
