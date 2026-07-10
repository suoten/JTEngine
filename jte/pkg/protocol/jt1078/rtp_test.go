package jt1078

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ===================================================================
// ParseJT1078Packet 单元测试
// ===================================================================

func TestParseJT1078Packet_Minimal(t *testing.T) {
	// 最小包：起始字节(1) + SIM(6) + 通道(1) + 数据类型(1) + 体长度(2) + 体(4)
	// 无可选字段，DataType=0x00 (视频I帧)
	body := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	pkt := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 1,
		DataType:     0x00, // I帧，无可选字段
		Body:         body,
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	parsed, consumed, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if consumed != len(data) {
		t.Errorf("consumed = %d, want %d", consumed, len(data))
	}
	if parsed.SIM != "013800000000" {
		t.Errorf("SIM = %q, want %q", parsed.SIM, "013800000000")
	}
	if parsed.LogicChannel != 1 {
		t.Errorf("LogicChannel = %d, want 1", parsed.LogicChannel)
	}
	if parsed.DataType != 0x00 {
		t.Errorf("DataType = 0x%02X, want 0x00", parsed.DataType)
	}
	if parsed.HasTimestamp {
		t.Error("HasTimestamp should be false")
	}
	if parsed.HasLastIFrame {
		t.Error("HasLastIFrame should be false")
	}
	if parsed.HasLastFrame {
		t.Error("HasLastFrame should be false")
	}
	if parsed.BodyLength != uint16(len(body)) {
		t.Errorf("BodyLength = %d, want %d", parsed.BodyLength, len(body))
	}
	if len(parsed.Body) != len(body) {
		t.Errorf("Body length = %d, want %d", len(parsed.Body), len(body))
	}
	for i, b := range parsed.Body {
		if b != body[i] {
			t.Errorf("Body[%d] = 0x%02X, want 0x%02X", i, b, body[i])
		}
	}
}

func TestParseJT1078Packet_WithTimestamp(t *testing.T) {
	// DataType bit4=1，有4字节时间戳
	body := []byte{0x01, 0x02}
	pkt := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 3,
		DataType:     0x10, // I帧 + 时间戳标志
		HasTimestamp: true,
		Timestamp:    0x12345678,
		Body:         body,
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	// 验证头部长度：9(固定) + 4(时间戳) + 2(体长度) = 15
	expectedHeaderLen := 15
	if len(data) != expectedHeaderLen+len(body) {
		t.Fatalf("data length = %d, want %d", len(data), expectedHeaderLen+len(body))
	}

	parsed, consumed, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if consumed != len(data) {
		t.Errorf("consumed = %d, want %d", consumed, len(data))
	}
	if !parsed.HasTimestamp {
		t.Error("HasTimestamp should be true")
	}
	if parsed.Timestamp != 0x12345678 {
		t.Errorf("Timestamp = 0x%08X, want 0x12345678", parsed.Timestamp)
	}
	if parsed.HasLastIFrame {
		t.Error("HasLastIFrame should be false")
	}
	if parsed.HasLastFrame {
		t.Error("HasLastFrame should be false")
	}
}

func TestParseJT1078Packet_WithLastIFrame(t *testing.T) {
	// DataType bit5=1，有1字节Last I Frame标记
	body := []byte{0xAA}
	pkt := &JT1078Packet{
		SIM:           "013800000000",
		LogicChannel:  1,
		DataType:      0x20, // I帧 + Last I Frame标志
		HasLastIFrame: true,
		LastIFrame:    0x01,
		Body:          body,
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	// 验证头部长度：9(固定) + 1(Last I Frame) + 2(体长度) = 12
	expectedHeaderLen := 12
	if len(data) != expectedHeaderLen+len(body) {
		t.Fatalf("data length = %d, want %d", len(data), expectedHeaderLen+len(body))
	}

	parsed, _, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if !parsed.HasLastIFrame {
		t.Error("HasLastIFrame should be true")
	}
	if parsed.LastIFrame != 0x01 {
		t.Errorf("LastIFrame = 0x%02X, want 0x01", parsed.LastIFrame)
	}
	if parsed.HasTimestamp {
		t.Error("HasTimestamp should be false")
	}
}

func TestParseJT1078Packet_WithLastFrame(t *testing.T) {
	// DataType bit6=1，有1字节Last Frame标记
	body := []byte{0xBB}
	pkt := &JT1078Packet{
		SIM:           "013800000000",
		LogicChannel:  1,
		DataType:      0x40, // I帧 + Last Frame标志
		HasLastFrame:  true,
		LastFrame:     0x02,
		Body:          body,
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	// 验证头部长度：9(固定) + 1(Last Frame) + 2(体长度) = 12
	expectedHeaderLen := 12
	if len(data) != expectedHeaderLen+len(body) {
		t.Fatalf("data length = %d, want %d", len(data), expectedHeaderLen+len(body))
	}

	parsed, _, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if !parsed.HasLastFrame {
		t.Error("HasLastFrame should be true")
	}
	if parsed.LastFrame != 0x02 {
		t.Errorf("LastFrame = 0x%02X, want 0x02", parsed.LastFrame)
	}
}

func TestParseJT1078Packet_AllOptionalFields(t *testing.T) {
	// 所有可选字段都存在：时间戳 + Last I Frame + Last Frame
	body := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	pkt := &JT1078Packet{
		SIM:           "013800000000",
		LogicChannel:  5,
		DataType:      0x73, // bit4+bit5+bit6 + 0x03(音频)
		HasTimestamp:  true,
		Timestamp:     0xAABBCCDD,
		HasLastIFrame: true,
		LastIFrame:    0x01,
		HasLastFrame:  true,
		LastFrame:     0x02,
		Body:          body,
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	// 验证头部长度：9(固定) + 4(时间戳) + 1(Last I Frame) + 1(Last Frame) + 2(体长度) = 17
	expectedHeaderLen := 17
	if len(data) != expectedHeaderLen+len(body) {
		t.Fatalf("data length = %d, want %d", len(data), expectedHeaderLen+len(body))
	}

	parsed, consumed, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if consumed != len(data) {
		t.Errorf("consumed = %d, want %d", consumed, len(data))
	}
	if !parsed.HasTimestamp {
		t.Error("HasTimestamp should be true")
	}
	if parsed.Timestamp != 0xAABBCCDD {
		t.Errorf("Timestamp = 0x%08X, want 0xAABBCCDD", parsed.Timestamp)
	}
	if !parsed.HasLastIFrame {
		t.Error("HasLastIFrame should be true")
	}
	if parsed.LastIFrame != 0x01 {
		t.Errorf("LastIFrame = 0x%02X, want 0x01", parsed.LastIFrame)
	}
	if !parsed.HasLastFrame {
		t.Error("HasLastFrame should be true")
	}
	if parsed.LastFrame != 0x02 {
		t.Errorf("LastFrame = 0x%02X, want 0x02", parsed.LastFrame)
	}
	if parsed.FrameType() != 3 {
		t.Errorf("FrameType = %d, want 3 (音频)", parsed.FrameType())
	}
	if !parsed.IsAudio() {
		t.Error("IsAudio should be true")
	}
}

func TestParseJT1078Packet_IncompleteData(t *testing.T) {
	tests := []struct {
		name string
		// 构建一个完整包后截断到指定长度
		truncateTo int
	}{
		{"empty", 0},
		{"only_start_byte", 1},
		{"partial_sim", 5},
		{"fixed_header_only", 9},
		{"missing_body_length", 10}, // 有时间戳但缺体长度
		{"partial_body", 14},        // 有体长度但体不完整
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := &JT1078Packet{
				SIM:          "013800000000",
				LogicChannel: 1,
				DataType:     0x00,
				Body:         []byte{0x01, 0x02, 0x03, 0x04},
			}
			fullData, _ := BuildJT1078Packet(pkt)

			truncated := fullData
			if tt.truncateTo < len(truncated) {
				truncated = truncated[:tt.truncateTo]
			}

			_, _, err := ParseJT1078Packet(truncated)
			if err != ErrIncompletePacket {
				t.Errorf("expected ErrIncompletePacket, got %v", err)
			}
		})
	}
}

func TestParseJT1078Packet_IncompleteData_WithOptionalFields(t *testing.T) {
	// 有时间戳字段的包，在时间戳中间截断
	pkt := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 1,
		DataType:     0x10,
		HasTimestamp: true,
		Timestamp:    0x12345678,
		Body:         []byte{0x01, 0x02},
	}
	fullData, _ := BuildJT1078Packet(pkt)

	// 截断到时间戳中间：9(固定头) + 2 = 11
	truncated := fullData[:11]
	_, _, err := ParseJT1078Packet(truncated)
	if err != ErrIncompletePacket {
		t.Errorf("expected ErrIncompletePacket, got %v", err)
	}
}

func TestParseJT1078Packet_InvalidStartByte(t *testing.T) {
	pkt := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 1,
		DataType:     0x00,
		Body:         []byte{0x01},
	}
	data, _ := BuildJT1078Packet(pkt)

	// 篡改起始字节
	data[0] = 0xFF

	_, _, err := ParseJT1078Packet(data)
	if err == nil {
		t.Fatal("expected error for invalid start byte, got nil")
	}
}

func TestParseJT1078Packet_EmptyBody(t *testing.T) {
	pkt := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 1,
		DataType:     0x00,
		Body:         []byte{},
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	parsed, consumed, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if parsed.BodyLength != 0 {
		t.Errorf("BodyLength = %d, want 0", parsed.BodyLength)
	}
	if len(parsed.Body) != 0 {
		t.Errorf("Body length = %d, want 0", len(parsed.Body))
	}
	if consumed != len(data) {
		t.Errorf("consumed = %d, want %d", consumed, len(data))
	}
}

// ===================================================================
// BuildJT1078Packet 单元测试
// ===================================================================

func TestBuildJT1078Packet_RoundTrip_Minimal(t *testing.T) {
	original := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 1,
		DataType:     0x00,
		Body:         []byte{0x01, 0x02, 0x03},
	}
	data, err := BuildJT1078Packet(original)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	parsed, consumed, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if consumed != len(data) {
		t.Errorf("consumed = %d, want %d", consumed, len(data))
	}
	if parsed.SIM != original.SIM {
		t.Errorf("SIM = %q, want %q", parsed.SIM, original.SIM)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if parsed.FrameType() != original.FrameType() {
		t.Errorf("FrameType = %d, want %d", parsed.FrameType(), original.FrameType())
	}
	if len(parsed.Body) != len(original.Body) {
		t.Fatalf("Body length = %d, want %d", len(parsed.Body), len(original.Body))
	}
	for i, b := range original.Body {
		if parsed.Body[i] != b {
			t.Errorf("Body[%d] = 0x%02X, want 0x%02X", i, parsed.Body[i], b)
		}
	}
}

func TestBuildJT1078Packet_RoundTrip_AllFields(t *testing.T) {
	original := &JT1078Packet{
		SIM:           "123456789012",
		LogicChannel:  7,
		DataType:      0x71, // bit4+bit5+bit6 + 0x01(视频P帧)
		HasTimestamp:  true,
		Timestamp:     0xCAFEBABE,
		HasLastIFrame: true,
		LastIFrame:    0x03,
		HasLastFrame:  true,
		LastFrame:     0x04,
		Body:          make([]byte, 256),
	}
	for i := range original.Body {
		original.Body[i] = byte(i)
	}

	data, err := BuildJT1078Packet(original)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	parsed, _, err := ParseJT1078Packet(data)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if parsed.SIM != original.SIM {
		t.Errorf("SIM = %q, want %q", parsed.SIM, original.SIM)
	}
	if parsed.LogicChannel != original.LogicChannel {
		t.Errorf("LogicChannel = %d, want %d", parsed.LogicChannel, original.LogicChannel)
	}
	if !parsed.HasTimestamp || parsed.Timestamp != original.Timestamp {
		t.Errorf("Timestamp = 0x%08X (has=%v), want 0x%08X (has=true)", parsed.Timestamp, parsed.HasTimestamp, original.Timestamp)
	}
	if !parsed.HasLastIFrame || parsed.LastIFrame != original.LastIFrame {
		t.Errorf("LastIFrame = 0x%02X (has=%v), want 0x%02X (has=true)", parsed.LastIFrame, parsed.HasLastIFrame, original.LastIFrame)
	}
	if !parsed.HasLastFrame || parsed.LastFrame != original.LastFrame {
		t.Errorf("LastFrame = 0x%02X (has=%v), want 0x%02X (has=true)", parsed.LastFrame, parsed.HasLastFrame, original.LastFrame)
	}
	if len(parsed.Body) != len(original.Body) {
		t.Fatalf("Body length = %d, want %d", len(parsed.Body), len(original.Body))
	}
	for i, b := range original.Body {
		if parsed.Body[i] != b {
			t.Errorf("Body[%d] = 0x%02X, want 0x%02X", i, parsed.Body[i], b)
			break
		}
	}
}

func TestBuildJT1078Packet_DataTypeConsistency(t *testing.T) {
	// 验证 BuildJT1078Packet 根据 Has* 标志重建 DataType
	// DataType 低4位保留，高4位由 Has* 标志决定
	pkt := &JT1078Packet{
		SIM:           "013800000000",
		LogicChannel:  1,
		DataType:      0x03, // 音频(低4位=3)，但 Has* 标志全为 true
		HasTimestamp:  true,
		HasLastIFrame: true,
		HasLastFrame:  true,
		Body:          []byte{0x01},
	}
	data, err := BuildJT1078Packet(pkt)
	if err != nil {
		t.Fatalf("BuildJT1078Packet failed: %v", err)
	}

	// DataType 应为 0x73 (bit4+bit5+bit6 + 0x03)
	if data[8] != 0x73 {
		t.Errorf("DataType byte = 0x%02X, want 0x73", data[8])
	}

	parsed, _, _ := ParseJT1078Packet(data)
	if parsed.FrameType() != 3 {
		t.Errorf("FrameType = %d, want 3 (音频)", parsed.FrameType())
	}
}

func TestBuildJT1078Packet_BodyTooLong(t *testing.T) {
	pkt := &JT1078Packet{
		SIM:          "013800000000",
		LogicChannel: 1,
		DataType:     0x00,
		Body:         make([]byte, 65536), // 超过 uint16 最大值
	}
	_, err := BuildJT1078Packet(pkt)
	if err == nil {
		t.Fatal("expected error for body too long, got nil")
	}
}

// ===================================================================
// 辅助方法测试
// ===================================================================

func TestJT1078Packet_FrameType(t *testing.T) {
	tests := []struct {
		dataType byte
		want     byte
		isVideo  bool
		isAudio  bool
	}{
		{0x00, 0, true, false},  // I帧
		{0x01, 1, false, false}, // P帧
		{0x02, 2, false, false}, // B帧
		{0x03, 3, false, true},  // 音频
		{0x04, 4, false, false}, // 透传
		{0x10, 0, true, false},  // I帧 + 时间戳
		{0x73, 3, false, true},  // 音频 + 全部可选字段
	}

	for _, tt := range tests {
		p := &JT1078Packet{DataType: tt.dataType}
		if p.FrameType() != tt.want {
			t.Errorf("DataType=0x%02X: FrameType = %d, want %d", tt.dataType, p.FrameType(), tt.want)
		}
		if p.IsVideoIFrame() != tt.isVideo {
			t.Errorf("DataType=0x%02X: IsVideoIFrame = %v, want %v", tt.dataType, p.IsVideoIFrame(), tt.isVideo)
		}
		if p.IsAudio() != tt.isAudio {
			t.Errorf("DataType=0x%02X: IsAudio = %v, want %v", tt.dataType, p.IsAudio(), tt.isAudio)
		}
	}
}

func TestJT1078Packet_HeaderLength(t *testing.T) {
	tests := []struct {
		name     string
		pkt      *JT1078Packet
		expected int
	}{
		{"no optional", &JT1078Packet{DataType: 0x00}, 11},                        // 9+2
		{"timestamp only", &JT1078Packet{DataType: 0x10, HasTimestamp: true}, 15}, // 9+4+2
		{"lastIFrame only", &JT1078Packet{DataType: 0x20, HasLastIFrame: true}, 12}, // 9+1+2
		{"lastFrame only", &JT1078Packet{DataType: 0x40, HasLastFrame: true}, 12}, // 9+1+2
		{"timestamp+lastIFrame", &JT1078Packet{DataType: 0x30, HasTimestamp: true, HasLastIFrame: true}, 16}, // 9+4+1+2
		{"all optional", &JT1078Packet{DataType: 0x70, HasTimestamp: true, HasLastIFrame: true, HasLastFrame: true}, 17}, // 9+4+1+1+2
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pkt.HeaderLength(); got != tt.expected {
				t.Errorf("HeaderLength = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestJT1078Packet_TotalLength(t *testing.T) {
	pkt := &JT1078Packet{
		DataType:     0x00,
		Body:         make([]byte, 100),
	}
	// HeaderLength = 11, Body = 100, Total = 111
	if pkt.TotalLength() != 111 {
		t.Errorf("TotalLength = %d, want 111", pkt.TotalLength())
	}

	pktWithTs := &JT1078Packet{
		DataType:     0x10,
		HasTimestamp: true,
		Body:         make([]byte, 50),
	}
	// HeaderLength = 15, Body = 50, Total = 65
	if pktWithTs.TotalLength() != 65 {
		t.Errorf("TotalLength = %d, want 65", pktWithTs.TotalLength())
	}
}

// ===================================================================
// Wrap/Unwrap 测试
// ===================================================================

func TestWrapUnwrapJT1078RTP_RoundTrip(t *testing.T) {
	sim := "013800000000"
	channel := byte(3)
	dataType := byte(0x00) // I帧
	rtpData := []byte{0x80, 0x60, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x12, 0x34, 0x56, 0x78}

	wrapped := WrapJT1078RTP(sim, channel, dataType, rtpData)

	parsedSim, parsedChannel, parsedDataType, parsedRtp, err := UnwrapJT1078RTP(wrapped)
	if err != nil {
		t.Fatalf("UnwrapJT1078RTP failed: %v", err)
	}
	if parsedSim != sim {
		t.Errorf("SIM = %q, want %q", parsedSim, sim)
	}
	if parsedChannel != channel {
		t.Errorf("LogicChannel = %d, want %d", parsedChannel, channel)
	}
	if parsedDataType != dataType {
		t.Errorf("DataType = 0x%02X, want 0x%02X", parsedDataType, dataType)
	}
	if len(parsedRtp) != len(rtpData) {
		t.Fatalf("RTP length = %d, want %d", len(parsedRtp), len(rtpData))
	}
	for i, b := range rtpData {
		if parsedRtp[i] != b {
			t.Errorf("RTP[%d] = 0x%02X, want 0x%02X", i, parsedRtp[i], b)
			break
		}
	}
}

func TestUnwrapJT1078RTP_InvalidData(t *testing.T) {
	// 无效起始字节
	_, _, _, _, err := UnwrapJT1078RTP([]byte{0xFF, 0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for invalid data, got nil")
	}
}

func TestUnwrapJT1078RTP_IncompleteData(t *testing.T) {
	// 数据不完整
	_, _, _, _, err := UnwrapJT1078RTP([]byte{0x30, 0x01})
	if err == nil {
		t.Fatal("expected error for incomplete data, got nil")
	}
}

// ===================================================================
// TCP 分帧单元测试（使用 bufio.Reader 模拟流式读取）
// ===================================================================

// chunkReader 模拟 TCP 分片读取，每次 Read 最多返回 chunkSize 字节。
type chunkReader struct {
	data      []byte
	offset    int
	chunkSize int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunkSize
	if n > len(p) {
		n = len(p)
	}
	remaining := len(r.data) - r.offset
	if n > remaining {
		n = remaining
	}
	copy(p, r.data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

// TestTCPFraming_ConcatenatedPackets 测试粘包：多个包拼接在一起，逐个解析。
// 模拟 TCP 一次 Read 返回多个 1078 包的场景。
func TestTCPFraming_ConcatenatedPackets(t *testing.T) {
	sim := "013800000000"

	// 构建两个不同可选字段组合的包
	pkt1 := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x00, // 无可选字段
		Body:         []byte{0x01, 0x02},
	}
	pkt2 := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x10, // 有时间戳
		HasTimestamp: true,
		Timestamp:    0x12345678,
		Body:         []byte{0x03, 0x04, 0x05},
	}

	data1, _ := BuildJT1078Packet(pkt1)
	data2, _ := BuildJT1078Packet(pkt2)

	// 拼接两个包（粘包）
	combined := append(append([]byte{}, data1...), data2...)

	// 逐个解析
	parsed1, consumed1, err := ParseJT1078Packet(combined)
	if err != nil {
		t.Fatalf("Parse first packet failed: %v", err)
	}
	if consumed1 != len(data1) {
		t.Fatalf("first consumed = %d, want %d", consumed1, len(data1))
	}
	if parsed1.BodyLength != 2 {
		t.Errorf("first BodyLength = %d, want 2", parsed1.BodyLength)
	}

	parsed2, consumed2, err := ParseJT1078Packet(combined[consumed1:])
	if err != nil {
		t.Fatalf("Parse second packet failed: %v", err)
	}
	if consumed2 != len(data2) {
		t.Fatalf("second consumed = %d, want %d", consumed2, len(data2))
	}
	if !parsed2.HasTimestamp {
		t.Error("second packet should have timestamp")
	}
	if parsed2.Timestamp != 0x12345678 {
		t.Errorf("second Timestamp = 0x%08X, want 0x12345678", parsed2.Timestamp)
	}
	if parsed2.BodyLength != 3 {
		t.Errorf("second BodyLength = %d, want 3", parsed2.BodyLength)
	}

	// 验证总消费量等于两个包之和
	if consumed1+consumed2 != len(combined) {
		t.Errorf("total consumed = %d, want %d", consumed1+consumed2, len(combined))
	}
}

// TestTCPFraming_ConcatenatedPackets_ThreePackets 测试三个不同可选字段组合的包拼接。
func TestTCPFraming_ConcatenatedPackets_ThreePackets(t *testing.T) {
	sim := "013800000000"
	pkts := []*JT1078Packet{
		{SIM: sim, LogicChannel: 1, DataType: 0x00, Body: []byte{0x01}},
		{SIM: sim, LogicChannel: 1, DataType: 0x30, HasTimestamp: true, Timestamp: 0x11111111, HasLastIFrame: true, LastIFrame: 0x01, Body: []byte{0x02, 0x03}},
		{SIM: sim, LogicChannel: 1, DataType: 0x40, HasLastFrame: true, LastFrame: 0x02, Body: []byte{0x04, 0x05, 0x06, 0x07}},
	}

	var combined []byte
	for _, p := range pkts {
		data, _ := BuildJT1078Packet(p)
		combined = append(combined, data...)
	}

	offset := 0
	for i, expected := range pkts {
		parsed, consumed, err := ParseJT1078Packet(combined[offset:])
		if err != nil {
			t.Fatalf("packet %d parse failed: %v", i, err)
		}
		if parsed.SIM != expected.SIM {
			t.Errorf("packet %d SIM = %q, want %q", i, parsed.SIM, expected.SIM)
		}
		if parsed.HasTimestamp != expected.HasTimestamp {
			t.Errorf("packet %d HasTimestamp = %v, want %v", i, parsed.HasTimestamp, expected.HasTimestamp)
		}
		if parsed.HasLastIFrame != expected.HasLastIFrame {
			t.Errorf("packet %d HasLastIFrame = %v, want %v", i, parsed.HasLastIFrame, expected.HasLastIFrame)
		}
		if parsed.HasLastFrame != expected.HasLastFrame {
			t.Errorf("packet %d HasLastFrame = %v, want %v", i, parsed.HasLastFrame, expected.HasLastFrame)
		}
		if len(parsed.Body) != len(expected.Body) {
			t.Errorf("packet %d Body length = %d, want %d", i, len(parsed.Body), len(expected.Body))
		}
		offset += consumed
	}

	if offset != len(combined) {
		t.Errorf("total consumed = %d, want %d", offset, len(combined))
	}
}

// TestTCPFraming_FragmentedPacket 测试拆包：使用 bufio.Reader 模拟分片读取。
// chunkReader 每次只返回 1 字节，模拟极端 TCP 分片场景。
func TestTCPFraming_FragmentedPacket(t *testing.T) {
	sim := "013800000000"
	pkt := &JT1078Packet{
		SIM:           sim,
		LogicChannel:  1,
		DataType:      0x73, // 全部可选字段
		HasTimestamp:  true,
		Timestamp:     0xCAFEBABE,
		HasLastIFrame: true,
		LastIFrame:    0x01,
		HasLastFrame:  true,
		LastFrame:     0x02,
		Body:          []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE},
	}
	data, _ := BuildJT1078Packet(pkt)

	// 每次只返回 1 字节（极端分片）
	reader := &chunkReader{data: data, chunkSize: 1}
	bufioReader := bufio.NewReaderSize(reader, 128*1024)

	// 复刻 handleStreamConnection 的分帧逻辑
	// 1) 同步起始字节
	firstByte, err := bufioReader.Peek(1)
	if err != nil {
		t.Fatalf("Peek start byte failed: %v", err)
	}
	if firstByte[0] != JT1078StartByte {
		t.Fatalf("start byte = 0x%02X, want 0x%02X", firstByte[0], JT1078StartByte)
	}

	// 2) Peek 固定头部
	fixedHeader, err := bufioReader.Peek(JT1078FixedHeaderLen)
	if err != nil {
		t.Fatalf("Peek fixed header failed: %v", err)
	}

	// 3) 计算完整包头长度
	dataType := fixedHeader[8]
	headerLen := JT1078FixedHeaderLen
	if dataType&JT1078HasTimestamp != 0 {
		headerLen += 4
	}
	if dataType&JT1078HasLastIFrame != 0 {
		headerLen += 1
	}
	if dataType&JT1078HasLastFrame != 0 {
		headerLen += 1
	}
	headerLen += 2 // 体长度字段

	// 4) Peek 完整包头
	fullHeader, err := bufioReader.Peek(headerLen)
	if err != nil {
		t.Fatalf("Peek full header failed: %v", err)
	}

	bodyLen := int(binary.BigEndian.Uint16(fullHeader[headerLen-2 : headerLen]))
	totalLen := headerLen + bodyLen

	// 5) Peek 完整包
	pktData, err := bufioReader.Peek(totalLen)
	if err != nil {
		t.Fatalf("Peek complete packet failed: %v", err)
	}

	// 6) 解析
	parsed, consumed, err := ParseJT1078Packet(pktData)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}

	// 7) 消费
	bufioReader.Discard(consumed)

	// 验证
	if parsed.SIM != sim {
		t.Errorf("SIM = %q, want %q", parsed.SIM, sim)
	}
	if !parsed.HasTimestamp {
		t.Error("HasTimestamp should be true")
	}
	if parsed.Timestamp != 0xCAFEBABE {
		t.Errorf("Timestamp = 0x%08X, want 0xCAFEBABE", parsed.Timestamp)
	}
	if !parsed.HasLastIFrame || parsed.LastIFrame != 0x01 {
		t.Errorf("LastIFrame = 0x%02X (has=%v), want 0x01 (has=true)", parsed.LastIFrame, parsed.HasLastIFrame)
	}
	if !parsed.HasLastFrame || parsed.LastFrame != 0x02 {
		t.Errorf("LastFrame = 0x%02X (has=%v), want 0x02 (has=true)", parsed.LastFrame, parsed.HasLastFrame)
	}
	if len(parsed.Body) != 5 {
		t.Fatalf("Body length = %d, want 5", len(parsed.Body))
	}
}

// TestTCPFraming_FragmentedPacket_MultiplePackets 测试拆包+粘包混合场景：
// 多个包分片到达，每片 2 字节。
func TestTCPFraming_FragmentedPacket_MultiplePackets(t *testing.T) {
	sim := "013800000000"
	pkt1 := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x00,
		Body:         []byte{0x01, 0x02, 0x03},
	}
	pkt2 := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x10,
		HasTimestamp: true,
		Timestamp:    0x22222222,
		Body:         []byte{0x04, 0x05},
	}

	data1, _ := BuildJT1078Packet(pkt1)
	data2, _ := BuildJT1078Packet(pkt2)
	combined := append(append([]byte{}, data1...), data2...)

	// 每次返回 2 字节
	reader := &chunkReader{data: combined, chunkSize: 2}
	bufioReader := bufio.NewReaderSize(reader, 128*1024)

	var parsedPackets []*JT1078Packet

	for len(parsedPackets) < 2 {
		// 1) 同步起始字节
		_, err := bufioReader.Peek(1)
		if err != nil {
			t.Fatalf("Peek start byte failed: %v", err)
		}

		// 2) Peek 固定头部
		fixedHeader, err := bufioReader.Peek(JT1078FixedHeaderLen)
		if err != nil {
			t.Fatalf("Peek fixed header failed: %v", err)
		}

		// 3) 计算包头长度
		dt := fixedHeader[8]
		hl := JT1078FixedHeaderLen
		if dt&JT1078HasTimestamp != 0 {
			hl += 4
		}
		if dt&JT1078HasLastIFrame != 0 {
			hl += 1
		}
		if dt&JT1078HasLastFrame != 0 {
			hl += 1
		}
		hl += 2

		// 4) Peek 完整包头
		fh, err := bufioReader.Peek(hl)
		if err != nil {
			t.Fatalf("Peek full header failed: %v", err)
		}

		bl := int(binary.BigEndian.Uint16(fh[hl-2 : hl]))
		tl := hl + bl

		// 5) Peek 完整包
		pktData, err := bufioReader.Peek(tl)
		if err != nil {
			t.Fatalf("Peek complete packet failed: %v", err)
		}

		// 6) 解析
		parsed, consumed, err := ParseJT1078Packet(pktData)
		if err != nil {
			t.Fatalf("ParseJT1078Packet failed: %v", err)
		}

		// 7) 消费
		bufioReader.Discard(consumed)
		parsedPackets = append(parsedPackets, parsed)
	}

	// 验证第一个包
	if parsedPackets[0].BodyLength != 3 {
		t.Errorf("packet 1 BodyLength = %d, want 3", parsedPackets[0].BodyLength)
	}
	if parsedPackets[0].HasTimestamp {
		t.Error("packet 1 should not have timestamp")
	}

	// 验证第二个包
	if !parsedPackets[1].HasTimestamp {
		t.Error("packet 2 should have timestamp")
	}
	if parsedPackets[1].Timestamp != 0x22222222 {
		t.Errorf("packet 2 Timestamp = 0x%08X, want 0x22222222", parsedPackets[1].Timestamp)
	}
	if parsedPackets[1].BodyLength != 2 {
		t.Errorf("packet 2 BodyLength = %d, want 2", parsedPackets[1].BodyLength)
	}
}

// TestTCPFraming_ReSync 测试流中间有无效字节时重新同步。
// 模拟 TCP 流中偶尔出现的噪声字节。
func TestTCPFraming_ReSync(t *testing.T) {
	sim := "013800000000"
	pkt := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x00,
		Body:         []byte{0x01, 0x02},
	}
	data, _ := BuildJT1078Packet(pkt)

	// 在有效包前插入 3 个无效字节
	noisyData := append([]byte{0xFF, 0x00, 0xAB}, data...)

	reader := &chunkReader{data: noisyData, chunkSize: 128}
	bufioReader := bufio.NewReaderSize(reader, 128*1024)

	// 复刻 handleStreamConnection 的同步逻辑
	synced := false
	attempts := 0
	for !synced && attempts < 100 {
		attempts++
		firstByte, err := bufioReader.Peek(1)
		if err != nil {
			t.Fatalf("Peek failed: %v", err)
		}
		if firstByte[0] != JT1078StartByte {
			bufioReader.ReadByte() // 跳过无效字节
			continue
		}
		synced = true

		// 解析包
		fixedHeader, err := bufioReader.Peek(JT1078FixedHeaderLen)
		if err != nil {
			t.Fatalf("Peek fixed header failed: %v", err)
		}
		dt := fixedHeader[8]
		hl := JT1078FixedHeaderLen + 2 // 无可选字段
		if dt&JT1078HasTimestamp != 0 {
			hl += 4
		}
		if dt&JT1078HasLastIFrame != 0 {
			hl += 1
		}
		if dt&JT1078HasLastFrame != 0 {
			hl += 1
		}

		fh, err := bufioReader.Peek(hl)
		if err != nil {
			t.Fatalf("Peek full header failed: %v", err)
		}
		bl := int(binary.BigEndian.Uint16(fh[hl-2 : hl]))
		tl := hl + bl

		pktData, err := bufioReader.Peek(tl)
		if err != nil {
			t.Fatalf("Peek complete packet failed: %v", err)
		}

		parsed, consumed, err := ParseJT1078Packet(pktData)
		if err != nil {
			t.Fatalf("ParseJT1078Packet failed: %v", err)
		}
		bufioReader.Discard(consumed)

		if parsed.SIM != sim {
			t.Errorf("SIM = %q, want %q", parsed.SIM, sim)
		}
		if parsed.BodyLength != 2 {
			t.Errorf("BodyLength = %d, want 2", parsed.BodyLength)
		}
	}

	if !synced {
		t.Fatal("failed to sync to start byte")
	}
}

// TestTCPFraming_LargeBody 测试大体包（接近最大值）的分帧。
func TestTCPFraming_LargeBody(t *testing.T) {
	sim := "013800000000"
	largeBody := make([]byte, 60000)
	for i := range largeBody {
		largeBody[i] = byte(i % 256)
	}
	pkt := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x00,
		Body:         largeBody,
	}
	data, _ := BuildJT1078Packet(pkt)

	// 使用小分片读取
	reader := &chunkReader{data: data, chunkSize: 512}
	bufioReader := bufio.NewReaderSize(reader, 128*1024)

	// 同步并解析
	_, err := bufioReader.Peek(1)
	if err != nil {
		t.Fatalf("Peek start byte failed: %v", err)
	}
	fixedHeader, err := bufioReader.Peek(JT1078FixedHeaderLen)
	if err != nil {
		t.Fatalf("Peek fixed header failed: %v", err)
	}
	dt := fixedHeader[8]
	hl := JT1078FixedHeaderLen + 2
	if dt&JT1078HasTimestamp != 0 {
		hl += 4
	}

	fh, err := bufioReader.Peek(hl)
	if err != nil {
		t.Fatalf("Peek full header failed: %v", err)
	}
	bl := int(binary.BigEndian.Uint16(fh[hl-2 : hl]))
	tl := hl + bl

	pktData, err := bufioReader.Peek(tl)
	if err != nil {
		t.Fatalf("Peek complete packet failed: %v", err)
	}

	parsed, consumed, err := ParseJT1078Packet(pktData)
	if err != nil {
		t.Fatalf("ParseJT1078Packet failed: %v", err)
	}
	if consumed != len(data) {
		t.Errorf("consumed = %d, want %d", consumed, len(data))
	}
	if parsed.BodyLength != 60000 {
		t.Errorf("BodyLength = %d, want 60000", parsed.BodyLength)
	}
	if len(parsed.Body) != 60000 {
		t.Fatalf("Body length = %d, want 60000", len(parsed.Body))
	}
	// 验证体数据完整性
	for i, b := range largeBody {
		if parsed.Body[i] != b {
			t.Errorf("Body[%d] = 0x%02X, want 0x%02X", i, parsed.Body[i], b)
			break
		}
	}
}

// ===================================================================
// TCP 分帧集成测试（net.Pipe + handleStreamConnection）
// ===================================================================

// TestHandleStreamConnection_MultiplePackets 测试粘包场景：
// 多个 JT1078 包一次写入，验证 handleStreamConnection 正确分帧并处理。
func TestHandleStreamConnection_MultiplePackets(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	eng.Start()
	defer eng.Stop()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		eng.handleStreamConnection(serverConn)
		close(done)
	}()

	sim := "013800000000"

	// 构建 3 个 JT1078 包，内含有效 RTP 数据
	rtp1 := buildTestRTP(1, 1000, false)
	rtp2 := buildTestRTP(2, 2000, false)
	rtp3 := buildTestRTP(3, 3000, true) // marker=true

	pkt1 := &JT1078Packet{SIM: sim, LogicChannel: 1, DataType: 0x00, Body: rtp1}
	pkt2 := &JT1078Packet{SIM: sim, LogicChannel: 1, DataType: 0x01, Body: rtp2}
	pkt3 := &JT1078Packet{SIM: sim, LogicChannel: 1, DataType: 0x00, Body: rtp3}

	data1, _ := BuildJT1078Packet(pkt1)
	data2, _ := BuildJT1078Packet(pkt2)
	data3, _ := BuildJT1078Packet(pkt3)

	// 拼接 3 个包，一次写入（粘包）
	combined := append(append(append([]byte{}, data1...), data2...), data3...)

	// 在 goroutine 中写入（net.Pipe 的 Write 会阻塞直到对端 Read）
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(combined)
		writeDone <- err
	}()

	// 等待写入完成
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Write timed out")
	}

	// 等待处理完成
	time.Sleep(200 * time.Millisecond)

	// 验证 session 收到了 3 个包
	sessionID := fmt.Sprintf("%s_ch%d", sim, 1)
	session := eng.GetSession(sessionID)
	if session == nil {
		t.Fatal("session not created")
	}
	if session.Packets != 3 {
		t.Errorf("Packets = %d, want 3", session.Packets)
	}

	// 关闭连接，结束 handler
	clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit after connection close")
	}
}

// TestHandleStreamConnection_FragmentedPacket 测试拆包场景：
// 一个 JT1078 包分多次写入，验证 handleStreamConnection 正确组装。
func TestHandleStreamConnection_FragmentedPacket(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	eng.Start()
	defer eng.Stop()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		eng.handleStreamConnection(serverConn)
		close(done)
	}()

	sim := "013800000000"
	rtpData := buildTestRTP(10, 5000, true)
	pkt := &JT1078Packet{
		SIM:          sim,
		LogicChannel: 1,
		DataType:     0x10, // 有时间戳
		HasTimestamp: true,
		Timestamp:    0x11223344,
		Body:         rtpData,
	}
	data, _ := BuildJT1078Packet(pkt)

	// 分 3 段写入（拆包）
	seg1 := data[:len(data)/3]
	seg2 := data[len(data)/3 : 2*len(data)/3]
	seg3 := data[2*len(data)/3:]

	writeDone := make(chan error, 1)
	go func() {
		_, err1 := clientConn.Write(seg1)
		time.Sleep(50 * time.Millisecond)
		_, err2 := clientConn.Write(seg2)
		time.Sleep(50 * time.Millisecond)
		_, err3 := clientConn.Write(seg3)
		if err1 != nil {
			writeDone <- err1
		} else if err2 != nil {
			writeDone <- err2
		} else {
			writeDone <- err3
		}
	}()

	// 等待写入完成
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Write timed out")
	}

	// 等待处理完成
	time.Sleep(200 * time.Millisecond)

	// 验证
	sessionID := fmt.Sprintf("%s_ch%d", sim, 1)
	session := eng.GetSession(sessionID)
	if session == nil {
		t.Fatal("session not created")
	}
	if session.Packets != 1 {
		t.Errorf("Packets = %d, want 1 (fragmented packet should be assembled)", session.Packets)
	}

	// 关闭连接
	clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit after connection close")
	}
}

// TestHandleStreamConnection_ReSync 测试流中有无效字节时重新同步。
func TestHandleStreamConnection_ReSync(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	eng.Start()
	defer eng.Stop()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		eng.handleStreamConnection(serverConn)
		close(done)
	}()

	sim := "013800000000"
	rtpData := buildTestRTP(1, 100, false)
	pkt := &JT1078Packet{SIM: sim, LogicChannel: 1, DataType: 0x00, Body: rtpData}
	data, _ := BuildJT1078Packet(pkt)

	// 在有效包前插入无效字节
	noisyData := append([]byte{0xFF, 0x00, 0xAB, 0xCD}, data...)

	writeDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(noisyData)
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Write timed out")
	}

	// 等待处理完成
	time.Sleep(300 * time.Millisecond)

	// 验证：无效字节被跳过，有效包被处理
	sessionID := fmt.Sprintf("%s_ch%d", sim, 1)
	session := eng.GetSession(sessionID)
	if session == nil {
		t.Fatal("session not created (re-sync failed)")
	}
	if session.Packets != 1 {
		t.Errorf("Packets = %d, want 1 (after re-sync)", session.Packets)
	}

	// 关闭连接
	clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit after connection close")
	}
}

// TestHandleStreamConnection_MixedOptionalFields 测试不同可选字段组合的包混合传输。
func TestHandleStreamConnection_MixedOptionalFields(t *testing.T) {
	logger := zap.NewNop()
	eng := NewVideoEngine(logger, "")
	eng.Start()
	defer eng.Stop()

	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		eng.handleStreamConnection(serverConn)
		close(done)
	}()

	sim := "013800000000"

	// 包1：无可选字段
	rtp1 := buildTestRTP(1, 1000, false)
	pkt1 := &JT1078Packet{SIM: sim, LogicChannel: 1, DataType: 0x00, Body: rtp1}
	data1, _ := BuildJT1078Packet(pkt1)

	// 包2：有时间戳
	rtp2 := buildTestRTP(2, 2000, false)
	pkt2 := &JT1078Packet{
		SIM: sim, LogicChannel: 1, DataType: 0x10,
		HasTimestamp: true, Timestamp: 0x12345678,
		Body: rtp2,
	}
	data2, _ := BuildJT1078Packet(pkt2)

	// 包3：有全部可选字段
	rtp3 := buildTestRTP(3, 3000, true)
	pkt3 := &JT1078Packet{
		SIM: sim, LogicChannel: 1, DataType: 0x73,
		HasTimestamp: true, Timestamp: 0xAABBCCDD,
		HasLastIFrame: true, LastIFrame: 0x01,
		HasLastFrame: true, LastFrame: 0x02,
		Body: rtp3,
	}
	data3, _ := BuildJT1078Packet(pkt3)

	// 拼接并一次写入
	combined := append(append(append([]byte{}, data1...), data2...), data3...)

	writeDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(combined)
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Write timed out")
	}

	// 等待处理完成
	time.Sleep(200 * time.Millisecond)

	// 验证
	sessionID := fmt.Sprintf("%s_ch%d", sim, 1)
	session := eng.GetSession(sessionID)
	if session == nil {
		t.Fatal("session not created")
	}
	if session.Packets != 3 {
		t.Errorf("Packets = %d, want 3 (mixed optional fields)", session.Packets)
	}

	// 关闭连接
	clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not exit after connection close")
	}
}
