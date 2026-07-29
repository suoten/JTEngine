package jt1078

// ====================================================================
// JT/T 1078 跨版本兼容性测试
// 标准 21B vs 扩展 22B 报文兼容
// ====================================================================

import (
	"encoding/binary"
	"testing"

	"github.com/suoten/jt-engine/pkg/protocol"
)

// TestCrossVersion_1078StandardHeader 标准 12B 头部编解码
func TestCrossVersion_1078StandardHeader(t *testing.T) {
	header := &protocol.MessageHeader{
		MsgID:         MsgIDRealtimeRequest,
		BodyLen:       21,
		Phone:         "013800000000",
		SeqNum:        1,
		EncryptMethod: 0,
		Version2019:   false,
	}

	codec := &JT1078Codec{}
	encoded, err := codec.EncodeHeader(header)
	if err != nil {
		t.Fatalf("EncodeHeader standard failed: %v", err)
	}
	if len(encoded) != 12 {
		t.Errorf("standard header length: got %d, want 12", len(encoded))
	}

	decoded, offset, err := codec.ParseHeader(encoded)
	if err != nil {
		t.Fatalf("ParseHeader standard failed: %v", err)
	}
	if offset != 12 {
		t.Errorf("standard offset: got %d, want 12", offset)
	}
	if decoded.MsgID != header.MsgID {
		t.Errorf("MsgID: got 0x%04X, want 0x%04X", decoded.MsgID, header.MsgID)
	}
}

// TestCrossVersion_1078V2019Header 1078-2019 版 13B 头部编解码
func TestCrossVersion_1078V2019Header(t *testing.T) {
	header := &protocol.MessageHeader{
		MsgID:         MsgIDRealtimeRequest,
		BodyLen:       21,
		Phone:         "013800000000",
		SeqNum:        1,
		Version2019:   true,
		ProtocolVer:   1,
	}

	codec := &JT1078Codec{}
	encoded, err := codec.EncodeHeader(header)
	if err != nil {
		t.Fatalf("EncodeHeader 2019 failed: %v", err)
	}
	if len(encoded) != 13 {
		t.Errorf("2019 header length: got %d, want 13", len(encoded))
	}

	decoded, _, err := codec.ParseHeader(encoded)
	if err != nil {
		t.Fatalf("ParseHeader 2019 failed: %v", err)
	}
	if !decoded.Version2019 {
		t.Error("Version2019 should be true")
	}
	if decoded.ProtocolVer != 1 {
		t.Errorf("ProtocolVer: got %d, want 1", decoded.ProtocolVer)
	}
}

// TestCrossVersion_1078Standard21BBody 标准 21B 消息体
func TestCrossVersion_1078Standard21BBody(t *testing.T) {
	body := make([]byte, 21)
	copy(body[0:9], []byte("127.0.0.1"))
	binary.BigEndian.PutUint16(body[16:18], 8080)
	body[18] = 1 // ChannelID
	body[19] = 0 // MediaType
	body[20] = 0 // StreamType

	msg := &RealtimeRequestMessage{}
	if err := msg.Unmarshal(body); err != nil {
		t.Fatalf("Unmarshal 21B standard failed: %v", err)
	}
	if msg.LogicChannel != 1 {
		t.Errorf("LogicChannel: got %d, want 1", msg.LogicChannel)
	}
}

// TestCrossVersion_1078Extended22BBody 扩展 22B 消息体（含 TransportMode）
func TestCrossVersion_1078Extended22BBody(t *testing.T) {
	body := make([]byte, 22)
	copy(body[0:9], []byte("127.0.0.1"))
	binary.BigEndian.PutUint16(body[16:18], 8080)
	body[18] = 1  // ChannelID
	body[19] = 0  // MediaType
	body[20] = 0  // StreamType
	body[21] = 1  // TransportMode = TCP

	msg := &RealtimeRequestMessage{}
	if err := msg.Unmarshal(body); err != nil {
		t.Fatalf("Unmarshal 22B extended failed: %v", err)
	}
	if msg.TransportMode != 1 {
		t.Errorf("TransportMode: got %d, want 1", msg.TransportMode)
	}
}

// TestCrossVersion_1078BothFormatsRoundTrip 21B/22B 双格式往返
func TestCrossVersion_1078BothFormatsRoundTrip(t *testing.T) {
	msg := &RealtimeRequestMessage{
		IPAddress:    "127.0.0.1",
		Port:         8080,
		LogicChannel: 1,
		MediaType:    0,
		StreamType:   0,
	}

	// 标准 21B（TransportMode=0）
	msg.TransportMode = 0
	data21, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal 21B failed: %v", err)
	}
	if len(data21) != 21 {
		t.Errorf("21B length: got %d, want 21", len(data21))
	}
	decoded21 := &RealtimeRequestMessage{}
	if err := decoded21.Unmarshal(data21); err != nil {
		t.Fatalf("Unmarshal 21B failed: %v", err)
	}
	if decoded21.LogicChannel != msg.LogicChannel {
		t.Errorf("21B LogicChannel: got %d, want %d", decoded21.LogicChannel, msg.LogicChannel)
	}

	// 扩展 22B（TransportMode=1）
	msg.TransportMode = 1
	data22, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal 22B failed: %v", err)
	}
	if len(data22) != 22 {
		t.Errorf("22B length: got %d, want 22", len(data22))
	}
	decoded22 := &RealtimeRequestMessage{}
	if err := decoded22.Unmarshal(data22); err != nil {
		t.Fatalf("Unmarshal 22B failed: %v", err)
	}
	if decoded22.TransportMode != 1 {
		t.Errorf("22B TransportMode: got %d, want 1", decoded22.TransportMode)
	}
}
