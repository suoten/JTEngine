// FIXED: [P2] ParseRTPPacket 未处理RTP padding：padding位=1时末尾字节指示填充长度，需从payload中剔除 [2026-07-17]
package jt1078

import (
	"encoding/binary"
	"fmt"
)

const (
	RTPHeaderMinLen    = 12
	RTPVersion         = 2
	PayloadTypeH264    = 96
	PayloadTypeH265    = 97
	PayloadTypeAAC     = 98
	PayloadTypeG711    = 99  // G.711 泛型（向后兼容）
	// FIXED [P2]: 补充 JT/T 1078-2016 标准中缺失的音频负载类型常量
	// 动态负载类型 (96-127) 按 JTE 内部约定分配，与 ZLMediaKit 侧 PT 映射对齐
	PayloadTypeG726    = 100 // G.726
	PayloadTypeG722    = 101 // G.722.1
	PayloadTypeG723    = 102 // G.723
	PayloadTypeG729    = 103 // G.729
	PayloadTypeMP3     = 104 // MP3
	PayloadTypeG711A   = 105 // G.711A (PCMA)
	PayloadTypeG711U   = 106 // G.711U (PCMU)
)

// JT/T 1078-2016 音频编码类型常量（用于 AVAudioParam.AudioType 字段）
const (
	AudioEncodeTypeG711A  = 0 // G.711A (PCMA)
	AudioEncodeTypeG711U  = 1 // G.711U (PCMU)
	AudioEncodeTypeG726   = 2 // G.726
	AudioEncodeTypeG722   = 3 // G.722.1
	AudioEncodeTypeG723   = 4 // G.723
	AudioEncodeTypeG729   = 5 // G.729
	AudioEncodeTypeAAC    = 6 // AAC
	AudioEncodeTypeMP3    = 7 // MP3
)

// AudioEncodeTypeToPayloadType 将 JT/T 1078 音频编码类型映射为 RTP 动态负载类型。
// FIXED [P2]: 补全 G.726/G.722/G.723/G.729/MP3/G.711A/G.711U 的 PT 映射，
// 原 PayloadTypeG711=99 保留为 G.711 泛型（向后兼容），G.711A/G.711U 使用独立 PT。
// 未知类型返回 PayloadTypeG711（99）作为安全兜底。
func AudioEncodeTypeToPayloadType(audioType byte) byte {
	switch audioType {
	case AudioEncodeTypeG711A:
		return PayloadTypeG711A
	case AudioEncodeTypeG711U:
		return PayloadTypeG711U
	case AudioEncodeTypeG726:
		return PayloadTypeG726
	case AudioEncodeTypeG722:
		return PayloadTypeG722
	case AudioEncodeTypeG723:
		return PayloadTypeG723
	case AudioEncodeTypeG729:
		return PayloadTypeG729
	case AudioEncodeTypeAAC:
		return PayloadTypeAAC
	case AudioEncodeTypeMP3:
		return PayloadTypeMP3
	default:
		return PayloadTypeG711
	}
}

type RTPHeader struct {
	Version        byte
	Padding        bool
	Extension      bool
	CSRCCount      byte
	Marker         bool
	PayloadType    byte
	SeqNum         uint16
	Timestamp      uint32
	SSRC           uint32
	CSRC           []uint32
	ExtensionID    uint16
	ExtensionLen   uint16
	ExtensionData  []byte
}

type RTPPacket struct {
	Header  RTPHeader
	Payload []byte
}

func ParseRTPPacket(data []byte) (*RTPPacket, error) {
	if len(data) < RTPHeaderMinLen {
		return nil, fmt.Errorf("rtp packet too short: %d", len(data))
	}

	pkt := &RTPPacket{}
	h := &pkt.Header

	h.Version = (data[0] >> 6) & 0x03
	if h.Version != RTPVersion {
		return nil, fmt.Errorf("invalid rtp version: %d", h.Version)
	}

	h.Padding = (data[0]&0x20) != 0
	h.Extension = (data[0]&0x10) != 0
	h.CSRCCount = data[0] & 0x0F

	h.Marker = (data[1]&0x80) != 0
	h.PayloadType = data[1] & 0x7F

	h.SeqNum = binary.BigEndian.Uint16(data[2:4])
	h.Timestamp = binary.BigEndian.Uint32(data[4:8])
	h.SSRC = binary.BigEndian.Uint32(data[8:12])

	offset := 12

	if h.CSRCCount > 0 {
		csrcEnd := offset + int(h.CSRCCount)*4
		if csrcEnd > len(data) {
			return nil, fmt.Errorf("rtp csrc out of bounds")
		}
		h.CSRC = make([]uint32, h.CSRCCount)
		for i := 0; i < int(h.CSRCCount); i++ {
			h.CSRC[i] = binary.BigEndian.Uint32(data[offset+i*4 : offset+i*4+4])
		}
		offset = csrcEnd
	}

	if h.Extension {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("rtp extension header out of bounds")
		}
		h.ExtensionID = binary.BigEndian.Uint16(data[offset : offset+2])
		h.ExtensionLen = binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
		extEnd := offset + int(h.ExtensionLen)*4
		if extEnd > len(data) {
			return nil, fmt.Errorf("rtp extension data out of bounds")
		}
		h.ExtensionData = make([]byte, int(h.ExtensionLen)*4)
		copy(h.ExtensionData, data[offset:extEnd])
		offset = extEnd
	}

	if offset > len(data) {
		return nil, fmt.Errorf("rtp payload offset out of bounds")
	}

	payloadEnd := len(data)
	// FIXED: [P2] RTP padding处理：padding位=1时，最后一个字节指示填充字节数（含自身）
	if h.Padding && payloadEnd > offset {
		padLen := int(data[payloadEnd-1])
		if padLen > payloadEnd-offset {
			return nil, fmt.Errorf("rtp padding length %d exceeds payload %d", padLen, payloadEnd-offset)
		}
		payloadEnd -= padLen
	}

	pkt.Payload = make([]byte, payloadEnd-offset)
	copy(pkt.Payload, data[offset:payloadEnd])

	return pkt, nil
}

func BuildRTPPacket(header *RTPHeader, payload []byte) ([]byte, error) {
	buf := make([]byte, 0, RTPHeaderMinLen+len(header.CSRC)*4+len(header.ExtensionData)+len(payload))

	b0 := byte(header.Version<<6) | (header.CSRCCount & 0x0F)
	if header.Padding {
		b0 |= 0x20
	}
	if header.Extension {
		b0 |= 0x10
	}
	buf = append(buf, b0)

	b1 := header.PayloadType & 0x7F
	if header.Marker {
		b1 |= 0x80
	}
	buf = append(buf, b1)

	seqBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(seqBytes, header.SeqNum)
	buf = append(buf, seqBytes...)

	tsBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(tsBytes, header.Timestamp)
	buf = append(buf, tsBytes...)

	ssrcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(ssrcBytes, header.SSRC)
	buf = append(buf, ssrcBytes...)

	for _, csrc := range header.CSRC {
		csrcBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(csrcBytes, csrc)
		buf = append(buf, csrcBytes...)
	}

	if header.Extension {
		extIDBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(extIDBytes, header.ExtensionID)
		buf = append(buf, extIDBytes...)
		extLenBytes := make([]byte, 2)
		binary.BigEndian.PutUint16(extLenBytes, header.ExtensionLen)
		buf = append(buf, extLenBytes...)
		buf = append(buf, header.ExtensionData...)
	}

	buf = append(buf, payload...)
	return buf, nil
}

// ===================================================================
// JT/T 1078-2022 RTP 包格式（完整实现）
// ===================================================================
//
// 标准包结构（变量长度头部 + 定长体长度 + 变长体）：
//   起始字节(1B=0x30) + SIM卡号(6B BCD) + 逻辑通道号(1B) + 数据类型(1B)
//   + 包时间戳(4B, 当 DataType bit4=1 时存在)
//   + Last I Frame 标记(1B, 当 DataType bit5=1 时存在)
//   + Last Frame 标记(1B, 当 DataType bit6=1 时存在)
//   + 数据体长度(2B, 大端)
//   + 数据体(变长)
//
// DataType 字节位定义：
//   bit0-3: 数据类型 (0=视频I帧 1=视频P帧 2=视频B帧 3=音频 4=透传)
//   bit4:   0=无时间戳 1=有时间戳(4B)
//   bit5:   0=无Last I Frame标识 1=有(1B)
//   bit6:   0=无Last Frame标识 1=有(1B)
//   bit7:   保留
//
// AUTO-FIX-2026-07-04 [P0]: 原实现完全忽略可选字段和体长度字段，
// 导致 TCP 流中多包粘连时无法正确分包，且 RTP 数据包含错误的体长度字节。

// JT1078RTPWrapper 保留向后兼容的旧结构体。
type JT1078RTPWrapper struct {
	SIMHeader   []byte
	LogicChannel byte
	DataType     byte
}

// DataType 位掩码常量
const (
	JT1078DataTypeMask   = 0x0F // 数据类型低4位
	JT1078HasTimestamp   = 0x10 // bit4=1 表示存在4字节时间戳
	JT1078HasLastIFrame  = 0x20 // bit5=1 表示存在1字节Last I Frame标识
	JT1078HasLastFrame   = 0x40 // bit6=1 表示存在1字节Last Frame标识

	JT1078StartByte      = 0x30 // 起始字节
	JT1078FixedHeaderLen = 9    // 起始字节(1) + SIM(6) + 通道(1) + 数据类型(1)
	JT1078MinPacketLen   = 11   // 固定头(9) + 体长度(2) = 最小包长（无可选字段时）
)

// JT1078Packet JT/T 1078-2022 完整 RTP 包结构。
type JT1078Packet struct {
	SIM          string // SIM 卡号（12位数字字符串，BCD 编码）
	LogicChannel byte   // 逻辑通道号
	DataType     byte   // 数据类型（含可选字段标志位）
	Timestamp    uint32 // 包时间戳（DataType bit4=1 时有效）
	HasTimestamp bool   // 是否存在时间戳字段
	LastIFrame   byte   // Last I Frame 标记（DataType bit5=1 时有效）
	HasLastIFrame bool  // 是否存在 Last I Frame 字段
	LastFrame    byte   // Last Frame 标记（DataType bit6=1 时有效）
	HasLastFrame bool   // 是否存在 Last Frame 字段
	BodyLength   uint16 // 数据体长度
	Body         []byte // 数据体（RTP 包或透传数据）
}

// FrameType 返回数据类型低4位（0=I帧 1=P帧 2=B帧 3=音频 4=透传）。
func (p *JT1078Packet) FrameType() byte {
	return p.DataType & JT1078DataTypeMask
}

// IsVideoIFrame 判断是否为视频 I 帧。
func (p *JT1078Packet) IsVideoIFrame() bool {
	return p.FrameType() == 0
}

// IsAudio 判断是否为音频数据。
func (p *JT1078Packet) IsAudio() bool {
	return p.FrameType() == 3
}

// HeaderLength 计算包头长度（含起始字节、SIM、通道、数据类型、可选字段、体长度）。
func (p *JT1078Packet) HeaderLength() int {
	n := JT1078FixedHeaderLen // 起始字节 + SIM + 通道 + 数据类型
	if p.HasTimestamp {
		n += 4
	}
	if p.HasLastIFrame {
		n += 1
	}
	if p.HasLastFrame {
		n += 1
	}
	n += 2 // 体长度字段
	return n
}

// TotalLength 返回整个包的总长度（包头 + 数据体）。
func (p *JT1078Packet) TotalLength() int {
	return p.HeaderLength() + len(p.Body)
}

// ParseJT1078Packet 从字节流中解析一个完整的 JT/T 1078 RTP 包。
// 返回解析后的包及实际消费的字节数。
// 如果 data 不足一个完整包，返回 ErrIncompletePacket。
func ParseJT1078Packet(data []byte) (*JT1078Packet, int, error) {
	if len(data) < JT1078MinPacketLen {
		return nil, 0, ErrIncompletePacket
	}

	// 校验起始字节
	if data[0] != JT1078StartByte {
		return nil, 0, fmt.Errorf("invalid jt1078 start byte: 0x%02X (expected 0x%02X)", data[0], JT1078StartByte)
	}

	pkt := &JT1078Packet{}
	offset := 1

	// SIM (6B BCD)
	pkt.SIM = bcdToStringSafe(data[offset : offset+6])
	offset += 6

	// 逻辑通道号
	pkt.LogicChannel = data[offset]
	offset++

	// 数据类型
	pkt.DataType = data[offset]
	offset++

	// 可选字段：时间戳 (bit4)
	pkt.HasTimestamp = (pkt.DataType & JT1078HasTimestamp) != 0
	if pkt.HasTimestamp {
		if offset+4 > len(data) {
			return nil, 0, ErrIncompletePacket
		}
		pkt.Timestamp = binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
	}

	// 可选字段：Last I Frame 标记 (bit5)
	pkt.HasLastIFrame = (pkt.DataType & JT1078HasLastIFrame) != 0
	if pkt.HasLastIFrame {
		if offset+1 > len(data) {
			return nil, 0, ErrIncompletePacket
		}
		pkt.LastIFrame = data[offset]
		offset++
	}

	// 可选字段：Last Frame 标记 (bit6)
	pkt.HasLastFrame = (pkt.DataType & JT1078HasLastFrame) != 0
	if pkt.HasLastFrame {
		if offset+1 > len(data) {
			return nil, 0, ErrIncompletePacket
		}
		pkt.LastFrame = data[offset]
		offset++
	}

	// 数据体长度 (2B 大端)
	if offset+2 > len(data) {
		return nil, 0, ErrIncompletePacket
	}
	pkt.BodyLength = binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2

	// 数据体
	bodyEnd := offset + int(pkt.BodyLength)
	if bodyEnd > len(data) {
		return nil, 0, ErrIncompletePacket
	}
	pkt.Body = make([]byte, pkt.BodyLength)
	copy(pkt.Body, data[offset:bodyEnd])

	return pkt, bodyEnd, nil
}

// ErrIncompletePacket 表示数据不足以解析一个完整的 1078 包（TCP 流中可能需要继续读取）。
var ErrIncompletePacket = fmt.Errorf("incomplete jt1078 packet")

// BuildJT1078Packet 将 JT1078Packet 序列化为字节流。
func BuildJT1078Packet(pkt *JT1078Packet) ([]byte, error) {
	if len(pkt.Body) > 65535 {
		return nil, fmt.Errorf("jt1078 body too long: %d bytes (max 65535)", len(pkt.Body))
	}

	totalLen := pkt.HeaderLength() + len(pkt.Body)
	buf := make([]byte, 0, totalLen)

	// 起始字节
	buf = append(buf, JT1078StartByte)

	// SIM (6B BCD)
	simBCD, err := stringToBCD6(pkt.SIM)
	if err != nil {
		return nil, err
	}
	buf = append(buf, simBCD...)

	// 逻辑通道号
	buf = append(buf, pkt.LogicChannel)

	// 数据类型（确保可选字段标志位与 Has* 字段一致）
	dt := pkt.DataType & JT1078DataTypeMask // 保留低4位
	if pkt.HasTimestamp {
		dt |= JT1078HasTimestamp
	}
	if pkt.HasLastIFrame {
		dt |= JT1078HasLastIFrame
	}
	if pkt.HasLastFrame {
		dt |= JT1078HasLastFrame
	}
	buf = append(buf, dt)

	// 可选字段：时间戳
	if pkt.HasTimestamp {
		tsBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(tsBytes, pkt.Timestamp)
		buf = append(buf, tsBytes...)
	}

	// 可选字段：Last I Frame 标记
	if pkt.HasLastIFrame {
		buf = append(buf, pkt.LastIFrame)
	}

	// 可选字段：Last Frame 标记
	if pkt.HasLastFrame {
		buf = append(buf, pkt.LastFrame)
	}

	// 数据体长度 (2B 大端)
	bodyLenBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(bodyLenBytes, uint16(len(pkt.Body)))
	buf = append(buf, bodyLenBytes...)

	// 数据体
	buf = append(buf, pkt.Body...)

	return buf, nil
}

// WrapJT1078RTP 将 RTP 包封装为 JT/T 1078 格式（向后兼容接口）。
// AUTO-FIX-2026-07-04 [P0]: 补全体长度字段（2B 大端），原实现直接拼接 RTP 数据
// 导致 TCP 流中无法分包。dataType 低4位为帧类型，可选字段标志位由调用方设置。
func WrapJT1078RTP(sim string, logicChannel byte, dataType byte, rtpPacket []byte) []byte {
	pkt := &JT1078Packet{
		SIM:          sim,
		LogicChannel: logicChannel,
		DataType:     dataType,
		Body:         rtpPacket,
	}
	buf, _ := BuildJT1078Packet(pkt)
	return buf
}

// UnwrapJT1078RTP 从字节流中解封装 JT/T 1078 格式包，提取 RTP 数据。
// AUTO-FIX-2026-07-04 [P0]: 使用 ParseJT1078Packet 正确解析可选字段和体长度，
// 原实现直接取 data[9:] 作为 RTP 数据，导致：
//   1) 当存在时间戳/Last I Frame/Last Frame 字段时，RTP 数据包含错误的头部字节
//   2) TCP 流中多包粘连时，RTP 数据包含下一个包的头部
//   3) 缺失体长度校验，无法检测截断包
func UnwrapJT1078RTP(data []byte) (sim string, logicChannel byte, dataType byte, rtpData []byte, err error) {
	pkt, consumed, err := ParseJT1078Packet(data)
	if err != nil {
		return "", 0, 0, nil, err
	}
	_ = consumed // UnwrapJT1078RTP 仅返回 RTP 数据，不暴露 consumed
	return pkt.SIM, pkt.LogicChannel, pkt.DataType, pkt.Body, nil
}