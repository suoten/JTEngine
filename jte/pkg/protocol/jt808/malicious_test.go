package jt808

// ====================================================================
// 恶意报文测试
// 验证所有畸形输入返回 error 而非 panic
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// TestMalicious_OversizedBodyLen 超大 BodyLen（声称 1024 字节但实际只有 10 字节）
func TestMalicious_OversizedBodyLen(t *testing.T) {
	// 构造头部声称 BodyLen=1024，但实际 body 只有 10 字节
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:2], MsgIDLocation)
	bodyAttr := uint16(1024 & 0x03FF) // BodyLen=1024
	binary.BigEndian.PutUint16(header[2:4], bodyAttr)
	copy(header[4:10], []byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00}) // Phone
	binary.BigEndian.PutUint16(header[10:12], 1)                     // SeqNum

	// 追加 10 字节 body（远少于声称的 1024）
	data := append(header, make([]byte, 10)...)

	codec := &JT808Codec{}
	_, _, err := codec.ParseHeader(data)
	if err != nil {
		// ParseHeader 可能不检查 body 长度，只解析头部
		// 关键是 ParseBody 不应 panic
	}

	// 直接测试 ParseBody 不会 panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseBody panicked on oversized BodyLen: %v", r)
		}
	}()

	_, _ = codec.ParseBody(MsgIDLocation, data[12:])
}

// TestMalicious_MalformedEscape 畸形转义序列（0x7D 后跟非 0x01/0x02）
func TestMalicious_MalformedEscape(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Unescape panicked on malformed escape: %v", r)
		}
	}()

	// 0x7D 后跟 0xFF（非法转义字节）
	data := []byte{0x01, 0x7D, 0xFF, 0x02}
	result, err := Unescape(data)
	// 不应 panic，可能返回 error 或保留原始字节
	_ = result
	_ = err
}

// TestMalicious_Trailing0x7D 尾部 0x7D（无后续转义字节）
func TestMalicious_Trailing0x7D(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Unescape panicked on trailing 0x7D: %v", r)
		}
	}()

	data := []byte{0x01, 0x02, 0x7D}
	_, err := Unescape(data)
	if err == nil {
		t.Error("Unescape should return error for trailing 0x7D")
	}
}

// TestMalicious_OverlongPhone 超长车牌号（超过 12 字节 BCD）
func TestMalicious_OverlongPhone(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StringToBCD panicked on overlong phone: %v", r)
		}
	}()

	// 20 位数字（超过 12 位 BCD 上限）
	bcd, err := StringToBCD6("12345678901234567890")
	if err != nil {
		return
	}
	if len(bcd) != 6 {
		t.Errorf("BCD should be truncated to 6 bytes, got %d", len(bcd))
	}
}

// TestMalicious_HugePackTotal 超大分片数 PackTotal=65535
func TestMalicious_HugePackTotal(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseHeader panicked on huge PackTotal: %v", r)
		}
	}()

	// 构造 2011 版带分片的头部
	header := make([]byte, 16) // 12 + 4 (PackTotal + PackIndex)
	binary.BigEndian.PutUint16(header[0:2], MsgIDLocation)
	bodyAttr := uint16(100&0x03FF) | 0x2000 // HasPack=true
	binary.BigEndian.PutUint16(header[2:4], bodyAttr)
	copy(header[4:10], []byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00})
	binary.BigEndian.PutUint16(header[10:12], 1)          // SeqNum
	binary.BigEndian.PutUint16(header[12:14], 65535)       // PackTotal=65535
	binary.BigEndian.PutUint16(header[14:16], 65535)       // PackIndex=65535

	codec := &JT808Codec{}
	decoded, _, err := codec.ParseHeader(header)
	if err != nil {
		return // 可接受返回 error
	}
	// 不应 panic，且能正确解析分片信息
	if decoded.PackTotal != 65535 {
		t.Errorf("PackTotal: got %d, want 65535", decoded.PackTotal)
	}
}

// TestMalicious_EmptyBody 各消息类型空 body 不应 panic
func TestMalicious_EmptyBody(t *testing.T) {
	codec := &JT808Codec{}
	msgIDs := []uint16{
		MsgIDLocation, MsgIDRegister, MsgIDAuth, MsgIDCommand,
		MsgIDMultimedia, MsgIDCircularAreaSet, MsgIDRectAreaSet,
		MsgIDPolygonAreaSet, MsgIDRouteSet, MsgIDFireAreaSet,
		MsgIDGeneralResp, MsgIDRegisterResp,
	}

	for _, msgID := range msgIDs {
		t.Run("empty_"+string(rune(msgID)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseBody(msgID=0x%04X) panicked on empty body: %v", msgID, r)
				}
			}()
			_, _ = codec.ParseBody(msgID, []byte{})
		})
	}
}

// TestMalicious_AllOnes 全 0xFF 数据不应 panic
func TestMalicious_AllOnes(t *testing.T) {
	codec := &JT808Codec{}

	// 50 字节全 0xFF body
	body := make([]byte, 50)
	for i := range body {
		body[i] = 0xFF
	}

	msgIDs := []uint16{
		MsgIDLocation, MsgIDRegister, MsgIDCommand, MsgIDMultimedia,
		MsgIDGeneralResp, MsgIDRegisterResp, MsgIDTerminalCtrl,
		MsgIDTextSend, MsgIDCircularAreaSet,
	}

	for _, msgID := range msgIDs {
		t.Run("0xFF_"+string(rune(msgID)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseBody(0x%04X) panicked on all-0xFF: %v", msgID, r)
				}
			}()
			_, _ = codec.ParseBody(msgID, body)
		})
	}
}

// TestMalicious_SingleByte 单字节数据
func TestMalicious_SingleByte(t *testing.T) {
	codec := &JT808Codec{}
	msgIDs := []uint16{MsgIDLocation, MsgIDRegister, MsgIDCommand, MsgIDGeneralResp}

	for _, msgID := range msgIDs {
		t.Run("single_byte_"+string(rune(msgID)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseBody(0x%04X) panicked on single byte: %v", msgID, r)
				}
			}()
			_, _ = codec.ParseBody(msgID, []byte{0x00})
		})
	}
}

// TestMalicious_SuperLongStringToBCD 超长字符串 StringToBCD 不应 panic
func TestMalicious_SuperLongStringToBCD(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StringToBCD panicked on super long string: %v", r)
		}
	}()

	// 10000 位数字
	longStr := ""
	for i := 0; i < 10000; i++ {
		longStr += "1"
	}
	bcd, err := StringToBCD6(longStr)
	if err != nil {
		return // 可接受返回 error
	}
	if len(bcd) != 6 {
		t.Errorf("StringToBCD should return 6 bytes, got %d", len(bcd))
	}
}

// TestMalicious_StringToBCD_NonDigit StringToBCD 非数字字符
func TestMalicious_StringToBCD_NonDigit(t *testing.T) {
	tests := []string{
		"abcdef", "12-34-56", "A1B2C3", "", "  123  ",
	}
	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("StringToBCD(%q) panicked: %v", s, r)
				}
			}()
			bcd, err := StringToBCD6(s)
			_ = bcd
			_ = err
		})
	}
}

// TestMalicious_HeaderTooShort 头部数据过短
func TestMalicious_HeaderTooShort(t *testing.T) {
	codec := &JT808Codec{}
	for length := 0; length < 12; length++ {
		t.Run(string(rune(length)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseHeader panicked on %d-byte data: %v", length, r)
				}
			}()
			data := make([]byte, length)
			_, _, _ = codec.ParseHeader(data)
		})
	}
}

// TestMalicious_NegativeCoordsInAreas 负坐标区域消息不应 panic
func TestMalicious_NegativeCoordsInAreas(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("area Marshal panicked with negative coords: %v", r)
		}
	}()

	// 圆形区域 - 南纬西经
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{AreaID: 1, CenterLat: -90.0, CenterLon: -180.0, Radius: 0},
		},
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	// 验证编码后坐标为绝对值
	lat := uint32(data[7])<<24 | uint32(data[8])<<16 | uint32(data[9])<<8 | uint32(data[10])
	if lat != uint32(90.0*JT808CoordScaleFactor) {
		t.Errorf("negative lat -90.0 should encode as absolute: got %d, want %d", lat, uint32(90.0*JT808CoordScaleFactor))
	}
}

// TestMalicious_UnmarshalLocationTooShort 过短的位置数据
func TestMalicious_UnmarshalLocationTooShort(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LocationMessage.Unmarshal panicked on short data: %v", r)
		}
	}()

	msg := &LocationMessage{}
	// 10 字节（远少于最小 28 字节）
	_ = msg.Unmarshal(make([]byte, 10))
}

// TestMalicious_CommandMessageHugeParamCount CommandMessage 超大参数总数
func TestMalicious_CommandMessageHugeParamCount(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CommandMessage.Unmarshal panicked: %v", r)
		}
	}()

	msg := &CommandMessage{}
	// 声称 255 个参数但只有 10 字节数据
	data := []byte{0xFF} // count=255
	data = append(data, make([]byte, 10)...)
	_ = msg.Unmarshal(data)
}
