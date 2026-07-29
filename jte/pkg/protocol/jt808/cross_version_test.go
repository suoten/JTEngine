package jt808

// ====================================================================
// 跨版本兼容性测试
// JT/T 808-2011 vs 2019 头部格式互操作
// ====================================================================

import (
	"encoding/binary"
	"testing"

	"github.com/suoten/jt-engine/pkg/protocol"
)

// TestCrossVersion_2011Header 编码 2011 版头部 → 解码 → 验证字段
func TestCrossVersion_2011Header(t *testing.T) {
	header := &protocol.MessageHeader{
		MsgID:         MsgIDLocation,
		BodyLen:       28,
		Phone:         "013800000000",
		SeqNum:        1,
		EncryptMethod: 0,
		Version2019:   false,
	}

	codec := &JT808Codec{}
	encoded, err := codec.EncodeHeader(header)
	if err != nil {
		t.Fatalf("EncodeHeader 2011 failed: %v", err)
	}

	// 2011 版头部 = 2(MsgID) + 2(BodyAttr) + 6(Phone BCD) + 2(SeqNum) = 12 字节
	if len(encoded) != 12 {
		t.Errorf("2011 header length: got %d, want 12", len(encoded))
	}

	// 验证 Bit15 = 0（非 2019 版本）
	bodyAttr := binary.BigEndian.Uint16(encoded[2:4])
	if bodyAttr&0x8000 != 0 {
		t.Error("2011 header should have Bit15=0")
	}

	// 解码
	decoded, offset, err := codec.ParseHeader(encoded)
	if err != nil {
		t.Fatalf("ParseHeader 2011 failed: %v", err)
	}
	if offset != 12 {
		t.Errorf("2011 offset: got %d, want 12", offset)
	}
	if decoded.MsgID != header.MsgID {
		t.Errorf("MsgID: got 0x%04X, want 0x%04X", decoded.MsgID, header.MsgID)
	}
	if decoded.Phone != header.Phone {
		t.Errorf("Phone: got %q, want %q", decoded.Phone, header.Phone)
	}
	if decoded.Version2019 {
		t.Error("Version2019 should be false for 2011 header")
	}
}

// TestCrossVersion_2019Header 编码 2019 版头部 → 解码 → 验证字段
func TestCrossVersion_2019Header(t *testing.T) {
	header := &protocol.MessageHeader{
		MsgID:         MsgIDLocation,
		BodyLen:       28,
		Phone:         "013800000000",
		SeqNum:        1,
		EncryptMethod: 0,
		Version2019:   true,
		ProtocolVer:   1,
	}

	codec := &JT808Codec{}
	encoded, err := codec.EncodeHeader(header)
	if err != nil {
		t.Fatalf("EncodeHeader 2019 failed: %v", err)
	}

	// 2019 版头部 = 2(MsgID) + 2(BodyAttr) + 1(ProtocolVer) + 6(Phone BCD) + 2(SeqNum) = 13 字节
	if len(encoded) != 13 {
		t.Errorf("2019 header length: got %d, want 13", len(encoded))
	}

	// 验证 Bit15 = 1（2019 版本）
	bodyAttr := binary.BigEndian.Uint16(encoded[2:4])
	if bodyAttr&0x8000 == 0 {
		t.Error("2019 header should have Bit15=1")
	}

	// 验证 ProtocolVer 字节
	if encoded[4] != 1 {
		t.Errorf("ProtocolVer: got %d, want 1", encoded[4])
	}

	// 解码
	decoded, offset, err := codec.ParseHeader(encoded)
	if err != nil {
		t.Fatalf("ParseHeader 2019 failed: %v", err)
	}
	if offset != 13 {
		t.Errorf("2019 offset: got %d, want 13", offset)
	}
	if !decoded.Version2019 {
		t.Error("Version2019 should be true")
	}
	if decoded.ProtocolVer != 1 {
		t.Errorf("ProtocolVer: got %d, want 1", decoded.ProtocolVer)
	}
}

// TestCrossVersion_HeaderWithPack 分片头部互操作
func TestCrossVersion_HeaderWithPack(t *testing.T) {
	tests := []struct {
		name    string
		version bool
	}{
		{"2011分片", false},
		{"2019分片", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := &protocol.MessageHeader{
				MsgID:       MsgIDLocation,
				BodyLen:     100,
				Phone:       "013800000000",
				SeqNum:      1,
				HasPack:     true,
				PackTotal:   5,
				PackIndex:   2,
				Version2019: tt.version,
				ProtocolVer: 1,
			}
			codec := &JT808Codec{}
			encoded, err := codec.EncodeHeader(header)
			if err != nil {
				t.Fatalf("EncodeHeader failed: %v", err)
			}
			decoded, _, err := codec.ParseHeader(encoded)
			if err != nil {
				t.Fatalf("ParseHeader failed: %v", err)
			}
			if decoded.PackTotal != header.PackTotal {
				t.Errorf("PackTotal: got %d, want %d", decoded.PackTotal, header.PackTotal)
			}
			if decoded.PackIndex != header.PackIndex {
				t.Errorf("PackIndex: got %d, want %d", decoded.PackIndex, header.PackIndex)
			}
		})
	}
}

// TestCrossVersion_EncryptMethod 加密方式字段互操作
func TestCrossVersion_EncryptMethod(t *testing.T) {
	encryptMethods := []uint8{0, 1, 2, 4, 7} // 0=不加密, 1=RSA, 2=SM2, 4=自定义, 7=最大值
	for _, em := range encryptMethods {
		header := &protocol.MessageHeader{
			MsgID:         MsgIDHeartbeat,
			BodyLen:       0,
			Phone:         "013800000000",
			SeqNum:        1,
			EncryptMethod: em,
		}
		codec := &JT808Codec{}
		encoded, err := codec.EncodeHeader(header)
		if err != nil {
			t.Fatalf("EncodeHeader failed for encrypt=%d: %v", em, err)
		}
		decoded, _, err := codec.ParseHeader(encoded)
		if err != nil {
			t.Fatalf("ParseHeader failed for encrypt=%d: %v", em, err)
		}
		if decoded.EncryptMethod != em {
			t.Errorf("EncryptMethod: got %d, want %d", decoded.EncryptMethod, em)
		}
	}
}
