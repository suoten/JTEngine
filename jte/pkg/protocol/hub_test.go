package protocol

import (
	"testing"

	"go.uber.org/zap"
)

func TestFrameBuffer_808Delimited(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT808)

	data := []byte{0x7E, 0x01, 0x02, 0x03, 0x7E}
	frames := fb.Feed(data)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if frames[0][0] != 0x7E || frames[0][len(frames[0])-1] != 0x7E {
		t.Error("frame not properly delimited")
	}
}

func TestFrameBuffer_808MultipleFrames(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT808)

	data := []byte{0x7E, 0x01, 0x02, 0x7E, 0x7E, 0x03, 0x04, 0x7E}
	frames := fb.Feed(data)
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
}

func TestFrameBuffer_808PartialFrame(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT808)

	data1 := []byte{0x7E, 0x01, 0x02}
	frames := fb.Feed(data1)
	if len(frames) != 0 {
		t.Fatalf("expected 0 frames for partial data, got %d", len(frames))
	}

	data2 := []byte{0x03, 0x7E}
	frames = fb.Feed(data2)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame after completing, got %d", len(frames))
	}
}

func TestFrameBuffer_809Bracketed(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT809)

	// AUTO-FIX-2026-06-26: 结束符由0x5D修正为标准0x5E
	data := []byte{0x5B, 0x01, 0x02, 0x03, 0x5E}
	frames := fb.Feed(data)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if frames[0][0] != 0x5B || frames[0][len(frames[0])-1] != 0x5E {
		t.Error("frame not properly bracketed")
	}
}

func TestFrameBuffer_32960LengthPrefixed(t *testing.T) {
	fb := NewFrameBuffer(ProtocolGBT32960)

	frame := make([]byte, 30)
	frame[0] = 0x23
	frame[1] = 0x23
	for i := 2; i < 22; i++ {
		frame[i] = 0x00
	}
	frame[22] = 0x00
	frame[23] = 0x05
	for i := 24; i < 29; i++ {
		frame[i] = byte(i)
	}
	frame[29] = 0x00

	frames := fb.Feed(frame)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
}

func TestUnescape808(t *testing.T) {
	tests := []struct {
		input    []byte
		expected []byte
	}{
		{[]byte{0x7D, 0x02}, []byte{0x7E}},
		{[]byte{0x7D, 0x01}, []byte{0x7D}},
		{[]byte{0x01, 0x7D, 0x02, 0x03}, []byte{0x01, 0x7E, 0x03}},
		{[]byte{0x01, 0x02, 0x03}, []byte{0x01, 0x02, 0x03}},
	}

	for _, tt := range tests {
		result := unescape808(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("unescape808 length: got %d, want %d", len(result), len(tt.expected))
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("unescape808[%d]: got 0x%02X, want 0x%02X", i, result[i], tt.expected[i])
			}
		}
	}
}

func TestUnescape809(t *testing.T) {
	tests := []struct {
		input    []byte
		expected []byte
	}{
		{[]byte{0x5A, 0x01}, []byte{0x5B}},
		{[]byte{0x5A, 0x02}, []byte{0x5A}},
		{[]byte{0x5E, 0x01}, []byte{0x5D}},
		{[]byte{0x5E, 0x02}, []byte{0x5E}},
		{[]byte{0x01, 0x5A, 0x01, 0x03}, []byte{0x01, 0x5B, 0x03}},
	}

	for _, tt := range tests {
		result := unescape809(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("unescape809 length: got %d, want %d", len(result), len(tt.expected))
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("unescape809[%d]: got 0x%02X, want 0x%02X", i, result[i], tt.expected[i])
			}
		}
	}
}

func TestFrameBuffer_Protocol(t *testing.T) {
	fb := NewFrameBuffer(ProtocolJT808)
	if fb.GetProtocol() != ProtocolJT808 {
		t.Errorf("Protocol = %s, want %s", fb.GetProtocol(), ProtocolJT808)
	}

	fb2 := NewFrameBuffer(ProtocolJT809)
	if fb2.GetProtocol() != ProtocolJT809 {
		t.Errorf("Protocol = %s, want %s", fb2.GetProtocol(), ProtocolJT809)
	}
}

func TestIs1045Message_StatusMessages(t *testing.T) {
	hub := NewHub(zap.NewNop())

	for _, msgID := range []uint16{0x0900, 0x0901, 0x0902, 0x0903, 0x0904, 0x0905, 0x0906, 0x0907} {
		msg := &Message{Header: MessageHeader{MsgID: msgID}}
		if !hub.is1045Message(msg) {
			t.Errorf("expected 0x%04X to be 1045 message", msgID)
		}
	}

	non1045 := &Message{Header: MessageHeader{MsgID: 0x0100}}
	if hub.is1045Message(non1045) {
		t.Error("0x0100 should not be 1045 message")
	}
}