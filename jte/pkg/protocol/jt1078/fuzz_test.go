package jt1078

// ====================================================================
// [P2-补充] JT/T 1078 协议编解码模糊测试
// 覆盖：RTPPacket、RealtimeRequestMessage、DownloadRequestMessage
// ====================================================================

import (
	"encoding/binary"
	"testing"
)

// FuzzRTPPacketParse 模糊测试 ParseRTPPacket
func FuzzRTPPacketParse(f *testing.F) {
	// 合法 RTP 包（12B 头 + 4B payload）
	seed := make([]byte, 16)
	seed[0] = 0x80 // V=2, P=0, X=0, CC=0
	seed[1] = 0x60 // M=0, PT=96
	binary.BigEndian.PutUint16(seed[2:4], 1)
	binary.BigEndian.PutUint32(seed[4:8], 1000)
	binary.BigEndian.PutUint32(seed[8:12], 0x01020304)
	f.Add(seed)

	f.Add(make([]byte, 0))   // 空
	f.Add(make([]byte, 1))   // 单字节
	f.Add(make([]byte, 11))  // 不足 12B 头
	f.Add(make([]byte, 12))  // 仅头
	f.Add(make([]byte, 255)) // 超长

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseRTPPacket(data)
		// 确保不 panic
	})
}

// FuzzRealtimeRequestUnmarshal 模糊测试 RealtimeRequestMessage.Unmarshal
func FuzzRealtimeRequestUnmarshal(f *testing.F) {
	// 标准 21B
	seed21 := make([]byte, 21)
	copy(seed21[0:9], []byte("127.0.0.1"))
	binary.BigEndian.PutUint16(seed21[16:18], 8080)
	f.Add(seed21)

	// 扩展 22B
	seed22 := make([]byte, 22)
	copy(seed22[0:9], []byte("127.0.0.1"))
	binary.BigEndian.PutUint16(seed22[16:18], 8080)
	seed22[21] = 1 // TransportMode=TCP
	f.Add(seed22)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 20)) // 不足
	f.Add(make([]byte, 255))

	f.Fuzz(func(t *testing.T, data []byte) {
		msg := &RealtimeRequestMessage{}
		_ = msg.Unmarshal(data)
	})
}

// FuzzHeaderParse 模糊测试 JT1078Codec.ParseHeader
func FuzzHeaderParse(f *testing.F) {
	// 标准 12B 头
	seed := make([]byte, 12)
	binary.BigEndian.PutUint16(seed[0:2], 0x9101)
	binary.BigEndian.PutUint16(seed[2:4], 21)
	copy(seed[4:10], []byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00})
	binary.BigEndian.PutUint16(seed[10:12], 1)
	f.Add(seed)

	// 2019 版 13B 头
	seed19 := make([]byte, 13)
	binary.BigEndian.PutUint16(seed19[0:2], 0x9101)
	binary.BigEndian.PutUint16(seed19[2:4], 21)
	seed19[4] = 1 // ProtocolVer
	copy(seed19[5:11], []byte{0x01, 0x38, 0x00, 0x00, 0x00, 0x00})
	binary.BigEndian.PutUint16(seed19[11:13], 1)
	f.Add(seed19)

	f.Add(make([]byte, 0))
	f.Add(make([]byte, 11))
	f.Add(make([]byte, 255))

	f.Fuzz(func(t *testing.T, data []byte) {
		codec := &JT1078Codec{}
		_, _, _ = codec.ParseHeader(data)
	})
}
