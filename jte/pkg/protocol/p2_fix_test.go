package protocol

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// P2 修复验证测试：unescape809 / unescape1253 尾部转义字节校验
// ---------------------------------------------------------------------------

func TestP2_Unescape809_TrailingEscapeByte(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
	}{
		{"正常数据", []byte{0x01, 0x02, 0x03}, false},
		{"含转义0x5A01", []byte{0x01, 0x5A, 0x01, 0x03}, false},
		{"含转义0x5E02", []byte{0x01, 0x5E, 0x02, 0x03}, false},
		{"尾部0x5A无后续", []byte{0x01, 0x02, 0x5A}, true},
		{"尾部0x5E无后续", []byte{0x01, 0x02, 0x5E}, true},
		{"仅0x5A", []byte{0x5A}, true},
		{"仅0x5E", []byte{0x5E}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := unescape809(tt.input)
			if tt.wantErr && err == nil {
				t.Errorf("unescape809(%v) expected error, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unescape809(%v) unexpected error: %v", tt.input, err)
			}
		})
	}
}

func TestP2_Unescape1253_TrailingEscapeByte(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
	}{
		{"正常数据", []byte{0x01, 0x02, 0x03}, false},
		{"含转义0x5A01", []byte{0x01, 0x5A, 0x01, 0x03}, false},
		{"含转义0x5E02", []byte{0x01, 0x5E, 0x02, 0x03}, false},
		{"尾部0x5A无后续", []byte{0x01, 0x02, 0x5A}, true},
		{"尾部0x5E无后续", []byte{0x01, 0x02, 0x5E}, true},
		{"仅0x5A", []byte{0x5A}, true},
		{"仅0x5E", []byte{0x5E}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := unescape1253(tt.input)
			if tt.wantErr && err == nil {
				t.Errorf("unescape1253(%v) expected error, got nil", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unescape1253(%v) unexpected error: %v", tt.input, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// P2 修复验证测试：Hub.Route 锁粒度优化
// ---------------------------------------------------------------------------

// stubCodec 用于测试，实现 Codec 接口但所有方法返回安全默认值
type stubCodec struct {
	pt ProtocolType
}

func (s *stubCodec) ProtocolType() ProtocolType                                { return s.pt }
func (s *stubCodec) ParseHeader(data []byte) (*MessageHeader, int, error)      { return nil, 0, nil }
func (s *stubCodec) EncodeHeader(header *MessageHeader) ([]byte, error)        { return nil, nil }
func (s *stubCodec) ParseBody(msgID uint16, data []byte) (MessageBody, error)  { return nil, nil }
func (s *stubCodec) EncodeBody(body MessageBody) ([]byte, error)               { return nil, nil }
func (s *stubCodec) VerifyChecksum(data []byte) bool                           { return false }

func TestP2_Route_LockOptimization(t *testing.T) {
	hub := NewHub(zap.NewNop())

	// 注册一个 stub codec
	hub.RegisterCodec(&stubCodec{pt: ProtocolJT808})

	// Route 应该能正常工作（不持有锁时调用 ParseHeader/ParseBody）
	data := []byte{0x7E, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x7E}
	_, _, _ = hub.Route(data)

	// 验证 Route 返回后不会阻塞 RegisterCodec（锁已释放）
	done := make(chan struct{})
	go func() {
		hub.RegisterCodec(&stubCodec{pt: ProtocolJT905})
		close(done)
	}()
	select {
	case <-done:
		// OK — 锁已释放，RegisterCodec 可以立即执行
	case <-time.After(2 * time.Second):
		t.Error("RegisterCodec blocked after Route returned (lock not released)")
	}
}

// ---------------------------------------------------------------------------
// P2 修复验证测试：extractBracketedMulti 最小长度校验
// ---------------------------------------------------------------------------

func TestP2_ExtractBracketedMulti_MinLength(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT809)

	// 构造一个低于最小长度（22B）的帧和一个正常帧
	shortFrame := make([]byte, 10) // 10B < 22B
	shortFrame[0] = 0x5B
	shortFrame[len(shortFrame)-1] = 0x5D

	validFrame := make([]byte, 24) // 24B >= 22B
	validFrame[0] = 0x5B
	validFrame[len(validFrame)-1] = 0x5D

	// 先喂短帧
	frames := fb.Feed(shortFrame)
	if len(frames) != 0 {
		t.Errorf("short frame should be discarded, got %d frames", len(frames))
	}

	// 再喂正常帧
	frames = fb.Feed(validFrame)
	if len(frames) != 1 {
		t.Errorf("valid frame should be accepted, got %d frames", len(frames))
	}
}

// ---------------------------------------------------------------------------
// P2 修复验证测试：extractBracketedMulti 混合短帧和正常帧
// ---------------------------------------------------------------------------

func TestP2_ExtractBracketedMulti_MixedShortAndValid(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT809)

	// 构造混合数据：短帧 + 正常帧
	shortFrame := make([]byte, 10)
	shortFrame[0] = 0x5B
	shortFrame[9] = 0x5D

	validFrame := make([]byte, 24)
	validFrame[0] = 0x5B
	validFrame[23] = 0x5D

	combined := append(shortFrame, validFrame...)
	frames := fb.Feed(combined)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame (short discarded), got %d", len(frames))
	}
	if len(frames[0]) != 24 {
		t.Errorf("valid frame length: got %d, want 24", len(frames[0]))
	}
}

// ---------------------------------------------------------------------------
// P2 修复验证测试：FrameBuffer.SetLogger
// ---------------------------------------------------------------------------

func TestP2_FrameBuffer_SetLogger(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT809)
	// 默认 logger 应该是 nop（不 panic）
	fb.Feed([]byte{0x5B, 0x01, 0x5D}) // 短帧，应该被丢弃但不 panic

	// SetLogger 不应 panic
	fb.SetLogger(nil) // nil should be ignored
	fb.SetLogger(zap.NewNop())
}
