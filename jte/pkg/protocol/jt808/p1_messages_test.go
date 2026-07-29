package jt808

// ===================================================================
// FIXED-2026-07-23 [P1]: jt808 messages 确定性编码与安全上限测试
// P1-4: CommandRespMessage 确定性编码
// P1-5: AlarmAttachmentMessage Size 上限
// ===================================================================

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestP1_CommandRespMessage_DeterministicMarshal 验证多次 Marshal 输出一致
func TestP1_CommandRespMessage_DeterministicMarshal(t *testing.T) {
	msg := &CommandRespMessage{
		RespSeqNum: 0x0100,
		RespCount:  3,
		Params: map[uint32][]byte{
			0x00030001: {0x01},
			0x00010001: {0x02, 0x03},
			0x00020001: {0x04, 0x05, 0x06},
		},
	}

	// 多次 Marshal，结果应完全一致
	first, err := msg.Marshal()
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}
	for i := 0; i < 10; i++ {
		data, err := msg.Marshal()
		if err != nil {
			t.Fatalf("Marshal %d: %v", i, err)
		}
		if !bytes.Equal(first, data) {
			t.Fatalf("non-deterministic encoding at iteration %d:\n  first:  %x\n  iter%d: %x", i, first, i, data)
		}
	}

	// 验证参数按 ID 升序排列
	// 3 params: 0x00010001, 0x00020001, 0x00030001
	// Each: 4B ID + 1B len + val
	// header: 2B RespSeqNum + 1B RespCount = 3B
	if len(first) < 3+3*5 {
		t.Fatalf("encoded too short: %d", len(first))
	}
	offset := 3
	expectedIDs := []uint32{0x00010001, 0x00020001, 0x00030001}
	for i, expectedID := range expectedIDs {
		id := binary.BigEndian.Uint32(first[offset : offset+4])
		if id != expectedID {
			t.Errorf("param %d ID = 0x%08X, want 0x%08X (not sorted)", i, id, expectedID)
		}
		valLen := int(first[offset+4])
		offset += 5 + valLen
	}
}

// TestP1_CommandRespMessage_RoundTrip 验证 Marshal→Unmarshal 往返正确
func TestP1_CommandRespMessage_RoundTrip(t *testing.T) {
	orig := &CommandRespMessage{
		RespSeqNum: 0x0200,
		RespCount:  2,
		Params: map[uint32][]byte{
			0x00010001: {0xAA, 0xBB},
			0x00020002: {0xCC},
		},
	}
	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed := &CommandRespMessage{}
	if err := parsed.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.RespSeqNum != orig.RespSeqNum {
		t.Errorf("RespSeqNum: got 0x%04X, want 0x%04X", parsed.RespSeqNum, orig.RespSeqNum)
	}
	if len(parsed.Params) != 2 {
		t.Fatalf("Params count: got %d, want 2", len(parsed.Params))
	}
	if !bytes.Equal(parsed.Params[0x00010001], orig.Params[0x00010001]) {
		t.Error("param 0x00010001 mismatch")
	}
	if !bytes.Equal(parsed.Params[0x00020002], orig.Params[0x00020002]) {
		t.Error("param 0x00020002 mismatch")
	}
}

// TestP1_AlarmAttachmentMessage_SizeLimit 验证附件 Size 超过 10MB 返回 error
func TestP1_AlarmAttachmentMessage_SizeLimit(t *testing.T) {
	// 构造一个 Size=11MB 的恶意数据
	const maxSize = 10 * 1024 * 1024
	data := make([]byte, 10) // AlarmID(4B) + AttCount(1B) + Type(1B) + Size(4B)
	// AlarmID
	binary.BigEndian.PutUint32(data[0:4], 0x12345678)
	// AttCount = 1
	data[4] = 1
	// Type
	data[5] = 0x01
	// Size = 11MB (exceeds limit)
	binary.BigEndian.PutUint32(data[6:10], maxSize+1)

	msg := &AlarmAttachmentMessage{}
	err := msg.Unmarshal(data)
	if err == nil {
		t.Fatal("Unmarshal should return error for attachment size > 10MB")
	}
}

// TestP1_AlarmAttachmentMessage_SizeWithinLimit 验证正常 Size 不受影响
func TestP1_AlarmAttachmentMessage_SizeWithinLimit(t *testing.T) {
	payload := []byte("test attachment data")
	data := make([]byte, 0, 5+5+len(payload))
	// AlarmID
	aidBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(aidBytes, 0x12345678)
	data = append(data, aidBytes...)
	// AttCount = 1
	data = append(data, 1)
	// Type
	data = append(data, 0x01)
	// Size
	sizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(sizeBytes, uint32(len(payload)))
	data = append(data, sizeBytes...)
	// Data
	data = append(data, payload...)

	msg := &AlarmAttachmentMessage{}
	err := msg.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal should succeed for normal attachment: %v", err)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
	}
	if msg.Attachments[0].Size != uint32(len(payload)) {
		t.Errorf("Size: got %d, want %d", msg.Attachments[0].Size, len(payload))
	}
	if string(msg.Attachments[0].Data) != string(payload) {
		t.Errorf("Data mismatch")
	}
}

// TestP1_AlarmAttachmentMessage_PartialData 验证分包场景（Size > 剩余数据）保留现有逻辑
func TestP1_AlarmAttachmentMessage_PartialData(t *testing.T) {
	// Size=100 但只提供 50 字节数据（分包场景）
	const declaredSize = 100
	partialData := make([]byte, 50)
	for i := range partialData {
		partialData[i] = byte(i)
	}

	data := make([]byte, 0, 5+5+len(partialData))
	aidBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(aidBytes, 0x12345678)
	data = append(data, aidBytes...)
	data = append(data, 1)   // AttCount
	data = append(data, 0x02) // Type
	sizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(sizeBytes, declaredSize)
	data = append(data, sizeBytes...)
	data = append(data, partialData...)

	msg := &AlarmAttachmentMessage{}
	err := msg.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal should succeed for partial data (分包): %v", err)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
	}
	if len(msg.Attachments[0].Data) != len(partialData) {
		t.Errorf("partial Data length: got %d, want %d", len(msg.Attachments[0].Data), len(partialData))
	}
}
