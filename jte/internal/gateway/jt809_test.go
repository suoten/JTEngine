package gateway

import (
	"testing"
	"time"

	"github.com/suoten/jt-engine/internal/config"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

// 注：getTestEnvValue() 定义在 jt809_2019_test.go 中，同一包内共享

// TestJT809Client_Disconnect_Idempotent 验证 Disconnect 多次调用不会 panic
// 修复点：原实现不 close stopCh 且不防御多次调用，sync.Once 保证安全
func TestJT809Client_Disconnect_Idempotent(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)

	// 多次 Disconnect 不应 panic（sync.Once 保护 close stopCh）
	c.Disconnect()
	c.Disconnect()
	c.Disconnect()
}

// TestJT809Client_SendKeepalive_NilConn_NoPanic 验证 conn 为 nil 时 SendKeepalive 返回 error 而非 panic
// 修复点：原实现直接 c.conn.Write，重连窗口期 c.conn=nil 会 nil panic
func TestJT809Client_SendKeepalive_NilConn_NoPanic(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	// c.conn 初始为 nil
	if err := c.SendKeepalive(); err == nil {
		t.Fatal("SendKeepalive with nil conn should return error")
	}
}

// TestJT809Client_Login_NilConn_NoPanic 验证 conn 为 nil 时 Login 返回 error 而非 panic
func TestJT809Client_Login_NilConn_NoPanic(t *testing.T) {
	testCfg := newTestCfg("p1", "1")
	testCfg.Host = "127.0.0.1"
	testCfg.Port = 9999
	c := NewJT809Client(
		testCfg,
		zap.NewNop(),
		nil, nil,
	)
	if err := c.Login(); err == nil {
		t.Fatal("Login with nil conn should return error")
	}
}

// TestJT809Client_SendVehicleData_NilConn_NoPanic 验证 conn 为 nil 时 SendVehicleData 不 panic
// AUTO-FIX-2026-06-29 [P1-5]: 行为变更——nil conn 时数据进缓冲区，不再返回 error。
// 断言改为：不 panic + 数据进入 pendingBuffer 等待重连补发。
func TestJT809Client_SendVehicleData_NilConn_NoPanic(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	loc := &storage.LocationData{Phone: "13800138000", Latitude: 39.9, Longitude: 116.4, Speed: 60, Direction: 90, Time: time.Now()}
	if err := c.SendVehicleData("13800138000", loc); err != nil {
		t.Fatalf("SendVehicleData with nil conn should buffer silently, got error: %v", err)
	}
	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1 (frame should be buffered for reconnect replay)", size)
	}
}

// TestJT809Client_SendAlarm_NilConn_NoPanic 验证 conn 为 nil 时 SendAlarm 不 panic
// AUTO-FIX-2026-06-29 [P1-5]: 行为变更——nil conn 时数据进缓冲区，不再返回 error。
func TestJT809Client_SendAlarm_NilConn_NoPanic(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	alarm := &storage.AlarmData{Phone: "13800138000", Type: "overspeed", Level: 1, Latitude: 39.9, Longitude: 116.4, Speed: 120, Direction: 90, Time: time.Now()}
	if err := c.SendAlarm(alarm); err != nil {
		t.Fatalf("SendAlarm with nil conn should buffer silently, got error: %v", err)
	}
	size, _ := c.GetPendingBufferStatus()
	if size != 1 {
		t.Errorf("buffer size = %d, want 1", size)
	}
}

// TestJT809Client_ReadLoop_NilConn_Returns 验证 conn 为 nil 时 readLoop 立即返回而非 panic
// 修复点：原实现直接 c.conn.SetReadDeadline，重连窗口期会 nil panic
func TestJT809Client_ReadLoop_NilConn_Returns(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	c.running.Store(true)
	// c.conn 为 nil，readLoop 应立即返回
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.readLoop()
	}()
	select {
	case <-done:
		// 成功：readLoop 在 nil conn 时退出
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop with nil conn should return immediately, not hang")
	}
}

// TestJT809Client_NextSeq_Sequential 验证 nextSeq 在持锁调用下递增
func TestJT809Client_NextSeq_Sequential(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	c.mu.Lock()
	s1 := c.nextSeq()
	s2 := c.nextSeq()
	s3 := c.nextSeq()
	c.mu.Unlock()
	if s2 != s1+1 || s3 != s2+1 {
		t.Fatalf("nextSeq should increment: %d, %d, %d", s1, s2, s3)
	}
}

// TestJT809Client_buildMessage_Structure 验证 buildMessage 生成的帧结构
// 帧格式: 0x5B + escaped(header + body + crc32) + 0x5D
func TestJT809Client_buildMessage_Structure(t *testing.T) {
	c := NewJT809Client(
		&config.JT809PlatformConfig{ID: "p1", Host: "127.0.0.1", Port: 9999},
		zap.NewNop(),
		nil, nil,
	)
	body := []byte{0x01, 0x02, 0x03}
	msg, err := c.buildMessage(0x1006, body, 42)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if len(msg) < 2 {
		t.Fatal("message too short")
	}
	if msg[0] != 0x5B || msg[len(msg)-1] != 0x5D {
		t.Fatal("message should be wrapped with 0x5B...0x5D")
	}
	// 解开转义后应能读出 msgID=0x1006 和 seqNum=42
	inner := msg[1 : len(msg)-1]
	unescaped, _ := unescape809(inner)
	if len(unescaped) < jt809HeaderLen {
		t.Fatalf("unescaped too short: %d", len(unescaped))
	}
	// AUTO-FIX-2026-07-16: 809标准22字节帧头：msgID在[18:20], seqNum在[2:4]
	msgID := uint16(unescaped[18])<<8 | uint16(unescaped[19])
	if msgID != 0x1006 {
		t.Fatalf("msgID = %#x, want 0x1006", msgID)
	}
	seqNum := uint16(unescaped[2])<<8 | uint16(unescaped[3])
	if seqNum != 42 {
		t.Fatalf("seqNum = %d, want 42", seqNum)
	}
}

// TestJT809Client_EscapeUnescape_RoundTrip 验证 809 转义/反转义可逆
func TestJT809Client_EscapeUnescape_RoundTrip(t *testing.T) {
	cases := [][]byte{
		{0x5B, 0x5A, 0x5D, 0x5E, 0x00, 0xFF, 0x5B},
		{0x5A, 0x5A, 0x5A},
		{0x5E, 0x5E, 0x5D, 0x5D},
		{},
	}
	for i, original := range cases {
		escaped := escape809(original)
		roundtrip, _ := unescape809(escaped)
		if len(roundtrip) != len(original) {
			t.Fatalf("case %d: length mismatch: got %d, want %d", i, len(roundtrip), len(original))
		}
		for j := 0; j < len(roundtrip); j++ {
			if roundtrip[j] != original[j] {
				t.Fatalf("case %d byte %d: got %#x, want %#x", i, j, roundtrip[j], original[j])
			}
		}
	}
}
