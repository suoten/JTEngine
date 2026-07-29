package jt808

// ====================================================================
// 性能基准测试
// 验证解析性能满足 10 万设备并发要求
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// BenchmarkLocationParse100W 0x0200 位置消息解析 100 万次
func BenchmarkLocationParse100W(b *testing.B) {
	// 构造标准位置消息（28 字节最小体）
	msg := &LocationMessage{
		AlarmFlag:  0x01,
		StatusFlag: 0x02,
		Latitude:   39.9,
		Longitude:  116.4,
		Altitude:   500,
		Speed:      60,
		Direction:  180,
		Time:       "240721120000",
	}
	data, err := msg.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		decoded := &LocationMessage{}
		_ = decoded.Unmarshal(data)
	}
}

// BenchmarkLocationMarshal100W 0x0200 位置消息编码 100 万次
func BenchmarkLocationMarshal100W(b *testing.B) {
	msg := &LocationMessage{
		AlarmFlag:  0x01,
		StatusFlag: 0x02,
		Latitude:   39.9,
		Longitude:  116.4,
		Altitude:   500,
		Speed:      60,
		Direction:  180,
		Time:       "240721120000",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = msg.Marshal()
	}
}

// BenchmarkFullPacketParse 完整 808 报文解析（头部+体+校验）
func BenchmarkFullPacketParse(b *testing.B) {
	// 构造完整报文：头部(12B) + 体(28B) + 校验(1B) = 41B
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:2], MsgIDLocation)
	binary.BigEndian.PutUint16(header[2:4], 28) // BodyLen=28
	copy(header[4:10], []byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00})
	binary.BigEndian.PutUint16(header[10:12], 1)

	body := &LocationMessage{
		Latitude: 39.9, Longitude: 116.4,
		Altitude: 500, Speed: 60, Direction: 180,
		Time: "240721120000",
	}
	bodyData, _ := body.Marshal()

	packet := append(header, bodyData...)
	// 追加校验码
	checksum := CalcChecksum(packet)
	packet = append(packet, checksum)
	// 转义
	escaped := Escape(packet)

	codec := &JT808Codec{}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		unescaped, _ := Unescape(escaped)
		_, offset, _ := codec.ParseHeader(unescaped)
		_, _ = codec.ParseBody(MsgIDLocation, unescaped[offset:len(unescaped)-1])
	}
}

// BenchmarkCommandMarshalDeterminism CommandMessage 编码性能（含排序）
func BenchmarkCommandMarshalDeterminism(b *testing.B) {
	msg := &CommandMessage{
		Params: map[uint32][]byte{
			0x0001: {0x01, 0x02},
			0x0002: {0x03},
			0x0003: {0x04, 0x05, 0x06},
			0x0100: {0xFF},
			0x0101: {0xAA, 0xBB},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = msg.Marshal()
	}
}

// BenchmarkStringToBCD100W StringToBCD 编码 100 万次
func BenchmarkStringToBCD100W(b *testing.B) {
	phone := "013800000000"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = StringToBCD6(phone)
	}
}

// BenchmarkCircularAreaMarshal100W 圆形区域编码 100 万次
func BenchmarkCircularAreaMarshal100W(b *testing.B) {
	msg := &CircularAreaSetMessage{
		SetType: 0x01,
		Areas: []CircularArea{
			{AreaID: 1, CenterLat: 39.9, CenterLon: 116.4, Radius: 500, SpeedLimit: 80, Duration: 30, MaxSpeed: 120, NightMaxSpeed: 60},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = msg.Marshal()
	}
}
