package jt808

// ====================================================================
// [P2-修复] 协议编解码模糊测试
// 重点测试边界条件：空数据、单字节、超大数据、全 0xFF 数据
// 运行方式：go test -fuzz=FuzzParseHeader -fuzztime=30s
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// FuzzParseHeader 模糊测试 ParseHeader，确保不会 panic
func FuzzParseHeader(f *testing.F) {
	// 种子语料
	f.Add([]byte{0x02, 0x00, 0x00, 0x26, 0x01, 0x38, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}) // 合法心跳
	f.Add([]byte{})              // 空数据
	f.Add([]byte{0xFF})          // 单字节全1
	f.Add(make([]byte, 0))       // 零长度
	f.Add(make([]byte, 1024))    // 超大数据全0
	f.Add(make([]byte, 4096))    // 超大

	f.Fuzz(func(t *testing.T, data []byte) {
		codec := &JT808Codec{}
		header, offset, err := codec.ParseHeader(data)
		if err != nil {
			// 正常：畸形数据应返回 error 而非 panic
			return
		}
		if header == nil {
			t.Error("ParseHeader returned nil header without error")
		}
		if offset <= 0 {
			t.Error("ParseHeader returned non-positive offset")
		}
	})
}

// FuzzParseBody 模糊测试 ParseBody，确保不会 panic
func FuzzParseBody(f *testing.F) {
	// 种子语料：合法消息 ID + 合法 body
	f.Add(uint16(MsgIDHeartbeat), []byte{})
	f.Add(uint16(MsgIDLocation), make([]byte, 28))
	f.Add(uint16(MsgIDRegister), make([]byte, 50))
	f.Add(uint16(0xFFFF), make([]byte, 100)) // 未知消息 ID

	f.Fuzz(func(t *testing.T, msgID uint16, data []byte) {
		codec := &JT808Codec{}
		body, err := codec.ParseBody(msgID, data)
		if err != nil {
			// 正常：畸形数据应返回 error 而非 panic
			return
		}
		if body == nil {
			t.Error("ParseBody returned nil body without error")
		}
	})
}

// FuzzEscapeUnescape 模糊测试 Escape/Unescape 往返一致性
func FuzzEscapeUnescape(f *testing.F) {
	f.Add([]byte{0x01, 0x7E, 0x02, 0x7D, 0x03})
	f.Add([]byte{0x7E, 0x7D})
	f.Add(make([]byte, 0))
	f.Add(make([]byte, 256))

	f.Fuzz(func(t *testing.T, original []byte) {
		escaped := Escape(original)
		unescaped, err := Unescape(escaped)
		if err != nil {
			t.Fatalf("Unescape(Escape(x)) failed: %v", err)
		}
		// 验证往返一致性
		if len(unescaped) != len(original) {
			t.Errorf("round-trip length mismatch: got %d, want %d", len(unescaped), len(original))
			return
		}
		for i := range original {
			if unescaped[i] != original[i] {
				t.Errorf("round-trip mismatch at byte %d: got 0x%02X, want 0x%02X", i, unescaped[i], original[i])
				return
			}
		}
	})
}

// FuzzStringToBCD 模糊测试 StringToBCD 输入校验
func FuzzStringToBCD(f *testing.F) {
	f.Add("013800000000")
	f.Add("20260101000000")
	f.Add("2026-01-01 00:00:00")
	f.Add("")
	f.Add("abcdef")

	f.Fuzz(func(t *testing.T, s string) {
		bcd, err := StringToBCD6(s)
		if err != nil {
			// 正常：无数字字符时返回 error
			return
		}
		if len(bcd) != 6 {
			t.Errorf("BCD length: got %d, want 6", len(bcd))
		}
		// 验证往返：BCDToString 应不 panic
		_, _ = BCDToString(bcd)
	})
}

// FuzzCommandMarshalDeterminism 模糊测试 CommandMessage.Marshal 确定性
func FuzzCommandMarshalDeterminism(f *testing.F) {
	// 用 []byte 作为种子，解析为多个 paramID
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x03})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 || len(data)%4 != 0 {
			return
		}
		msg := &CommandMessage{
			Params: make(map[uint32][]byte),
		}
		for i := 0; i+4 <= len(data); i += 4 {
			id := binary.BigEndian.Uint32(data[i : i+4])
			msg.Params[id] = []byte{data[i]}
		}
		if len(msg.Params) == 0 {
			return
		}
		// 编码两次，结果应相同（确定性）
		data1, err := msg.Marshal()
		if err != nil {
			return
		}
		data2, err := msg.Marshal()
		if err != nil {
			t.Fatalf("second Marshal failed: %v", err)
		}
		if len(data1) != len(data2) {
			t.Errorf("non-deterministic: length mismatch %d vs %d", len(data1), len(data2))
			return
		}
		for i := range data1 {
			if data1[i] != data2[i] {
				t.Errorf("non-deterministic: byte %d mismatch 0x%02X vs 0x%02X", i, data1[i], data2[i])
				return
			}
		}
	})
}

// FuzzMultimediaUnmarshal 模糊测试 MultimediaMessage.Unmarshal 边界条件
func FuzzMultimediaUnmarshal(f *testing.F) {
	// 合法 40 字节最小帧
	seed := make([]byte, 40)
	seed[0] = 0x00; seed[1] = 0x00; seed[2] = 0x00; seed[3] = 0x01 // MultimediaID
	binary.BigEndian.PutUint16(seed[4:6], 0x0001)                    // SpeedLimit+Duration
	f.Add(seed)
	f.Add(make([]byte, 0))  // 空数据
	f.Add(make([]byte, 39)) // 不足 40 字节
	f.Add(make([]byte, 100)) // 带附加信息

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &MultimediaMessage{}
		err := msg.Unmarshal(data)
		if err != nil {
			// 正常：畸形数据应返回 error 而非 panic
			return
		}
		// 验证字段在合法范围
		if msg.MultimediaType > 4 {
			t.Errorf("MultimediaType out of range: %d", msg.MultimediaType)
		}
	})
}
